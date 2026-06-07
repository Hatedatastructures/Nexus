// Package skill 提供技能中心 (Skills Hub) 集成功能。
// 支持搜索和安装来自 GitHub 和 URL 源的技能。
package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	pkgerrors "nexus-agent/internal/errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ───────────────────────────── 安全扫描预编译正则 ─────────────────────────────

// 危险命令模式 (dangerous level)
var dangerousPatterns = []struct {
	re  *regexp.Regexp
	msg string
}{
	{regexp.MustCompile(`(?i)rm\s+(-[rf]+\s+){1,2}/`), "rm -rf / 类命令"},
	{regexp.MustCompile(`(?i)rm\s+-rf\s+\*`), "rm -rf * 类命令"},
	{regexp.MustCompile(`(?i)mkfs\.`), "mkfs 格式化文件系统"},
	{regexp.MustCompile(`(?i)dd\s+if=`), "dd 原始磁盘写入"},
	{regexp.MustCompile(`(?i)>\s*/dev/sd[a-z]`), "直接写入块设备"},
	{regexp.MustCompile(`(?i)>\s*/dev/[hv]da`), "直接写入磁盘设备"},
	{regexp.MustCompile(`(?i)(?:chmod|chown)\s+777\s+/`), "对根目录设置 777 权限"},
	{regexp.MustCompile(`(?i):\(\)\s*\{\s*:\|\s*:&\s*\}\s*;`), "Shell Fork Bomb"},
	{regexp.MustCompile(`(?i)(?:wget|curl)\s+.*\|\s*(?:sudo\s+)?(?:ba)?sh`), "curl/wget | bash 管道执行"},
	{regexp.MustCompile(`(?i)(?:wget|curl)\s+.*\|\s*bash`), "远程脚本管道执行"},
	{regexp.MustCompile(`(?i)eval\s*` + "`"), "eval 反引号命令执行"},
	{regexp.MustCompile(`(?i)exec\s*\(`), "exec 系统调用"},
	{regexp.MustCompile(`(?i)base64\s+(-d|--decode)`), "base64 解码 (可能用于混淆恶意载荷)"},
	{regexp.MustCompile(`\\x[0-9a-fA-F]{2}`), "十六进制编码 (可能用于混淆)"},
	{regexp.MustCompile(`\$\{[^}]+\}`), "环境变量扩展 (可能用于命令注入)"},
	{regexp.MustCompile(`\$\([^)]+\)`), "命令替换 $(...) (可能用于命令注入)"},
	{regexp.MustCompile(`(?i)\b(eval|exec|spawn|subprocess)\s*[\(\[]`), "动态代码执行函数"},
	{regexp.MustCompile(`(?i)\bnc\s+(-l|-lk|-lp)`), "netcat 监听 (可能的反弹 shell)"},
	{regexp.MustCompile(`(?i)\bncat\s+(-l|--listen)`), "ncat 监听 (可能的反弹 shell)"},
	{regexp.MustCompile(`(?i)\bsocat\s+`), "socat 网络转发 (可能的反弹 shell)"},
}

// 中等风险模式 (warning level)
var warningPatterns = []struct {
	re  *regexp.Regexp
	msg string
}{
	{regexp.MustCompile(`(?i)\bsudo\b`), "使用 sudo 提权"},
	{regexp.MustCompile(`(?i)\bwget\s+`), "使用 wget 下载"},
	{regexp.MustCompile(`(?i)\bcurl\s+.*-[oO]\b`), "使用 curl 下载文件"},
	{regexp.MustCompile(`(?i)chmod\s+[0-7]{3,4}`), "文件权限修改"},
	{regexp.MustCompile(`(?i)(?:/etc/|/var/|/usr/|/boot/)`), "访问系统目录"},
	{regexp.MustCompile(`(?i)(?:\.env\b|credentials|\.secret)`), "可能访问敏感文件"},
}

// 组合检测模式
var (
	reDownload = regexp.MustCompile(`(?i)(?:wget|curl)\s+`)
	reSudo     = regexp.MustCompile(`(?i)\bsudo\b`)
	reFSOps    = regexp.MustCompile(`(?i)(?:rm\s+|mv\s+|cp\s+|mkdir\s+|touch\s+|ln\s+)`)
)

// ───────────────────────────── 技能搜索结果 ─────────────────────────────

// SkillMeta 技能搜索结果的元信息。
type SkillMeta struct {
	Name        string   `json:"name"`        // 技能名称
	Description string   `json:"description"` // 技能描述
	Source      string   `json:"source"`      // 来源: "official" / "github" / "community"
	Identifier  string   `json:"identifier"`  // 安装标识符 (如 github:owner/repo/path)
	Repo        string   `json:"repo"`        // 仓库 URL (如 https://github.com/owner/repo)
	Tags        []string `json:"tags"`        // 标签列表
}

// ───────────────────────────── 技能中心客户端 ─────────────────────────────

// SkillsHub 是技能中心客户端，用于在线搜索和安装技能。
type SkillsHub struct {
	skillsDir string       // 本地技能安装目录
	client    *http.Client // HTTP 客户端
}

// NewSkillsHub 创建技能中心客户端。
func NewSkillsHub(skillsDir string) *SkillsHub {
	return &SkillsHub{
		skillsDir: skillsDir,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// ───────────────────────────── 搜索 ─────────────────────────────

// Search 在技能中心搜索匹配查询的技能。
//
// 搜索策略:
//  1. GitHub 代码搜索 (搜索 SKILL.md 文件)
//  2. 如果 query 格式为 github:owner/repo，直接搜索该仓库
//
// 返回匹配的技能元信息列表。
func (h *SkillsHub) Search(ctx context.Context, query string) ([]*SkillMeta, error) {
	var results []*SkillMeta

	// 检查是否为显式的 GitHub 仓库引用
	if repo, ok := strings.CutPrefix(query, "github:"); ok {
		meta, err := h.searchGitHubRepo(ctx, repo)
		if err != nil {
			slog.Warn("skill hub: GitHub search failed", "repo", repo, "error", err)
		} else if meta != nil {
			results = append(results, meta)
		}
		return results, nil
	}

	// 通用 GitHub 代码搜索
	githubResults, err := h.searchGitHubCode(ctx, query)
	if err != nil {
		slog.Warn("skill hub: GitHub search failed", "query", query, "error", err)
	} else {
		results = append(results, githubResults...)
	}

	return results, nil
}

// searchGitHubCode 通过 GitHub API 搜索包含 SKILL.md 文件的仓库。
func (h *SkillsHub) searchGitHubCode(ctx context.Context, query string) ([]*SkillMeta, error) {
	searchQuery := fmt.Sprintf("SKILL.md %s in:path repo:nexus-agent/skills", query)
	apiURL := fmt.Sprintf("https://api.github.com/search/code?q=%s&per_page=10", url.QueryEscape(searchQuery))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.NetworkHTTP, "GitHub API 请求失败", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, pkgerrors.New(pkgerrors.NetworkHTTP, fmt.Sprintf("GitHub API 返回 HTTP %d", resp.StatusCode))
	}

	var result struct {
		Items []struct {
			Repository struct {
				FullName    string `json:"full_name"`
				HTMLURL     string `json:"html_url"`
				Description string `json:"description"`
			} `json:"repository"`
			Path string `json:"path"`
		} `json:"items"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&result); err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.NetworkHTTP, "解析 GitHub API 响应失败", err)
	}

	var metas []*SkillMeta
	seen := make(map[string]struct{})

	for _, item := range result.Items {
		key := item.Repository.FullName
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		// 提取技能名称 (从路径中推断)
		skillName := filepath.Base(filepath.Dir(item.Path))
		identifier := fmt.Sprintf("github:%s/%s", item.Repository.FullName, filepath.Dir(item.Path))

		metas = append(metas, &SkillMeta{
			Name:        skillName,
			Description: item.Repository.Description,
			Source:      "github",
			Identifier:  identifier,
			Repo:        item.Repository.HTMLURL,
			Tags:        []string{},
		})
	}

	return metas, nil
}

// searchGitHubRepo 搜索特定 GitHub 仓库中的技能。
func (h *SkillsHub) searchGitHubRepo(ctx context.Context, repo string) (*SkillMeta, error) {
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 {
		return nil, pkgerrors.New(pkgerrors.SkillIO, fmt.Sprintf("无效的仓库格式: %s (格式: owner/repo)", repo))
	}

	// 验证 owner/repo 不含路径遍历字符
	for _, p := range parts {
		if strings.ContainsAny(p, "/\\?#&") || strings.Contains(p, "..") {
			return nil, pkgerrors.New(pkgerrors.SkillIO, fmt.Sprintf("无效的仓库标识符: %s", p))
		}
	}

	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s", url.PathEscape(parts[0]), url.PathEscape(parts[1]))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, nil
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, nil
	}

	var repoInfo struct {
		FullName    string   `json:"full_name"`
		HTMLURL     string   `json:"html_url"`
		Description string   `json:"description"`
		Topics      []string `json:"topics"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&repoInfo); err != nil {
		return nil, nil
	}

	return &SkillMeta{
		Name:        repoInfo.FullName,
		Description: repoInfo.Description,
		Source:      "github",
		Identifier:  "github:" + repoInfo.FullName,
		Repo:        repoInfo.HTMLURL,
		Tags:        repoInfo.Topics,
	}, nil
}
