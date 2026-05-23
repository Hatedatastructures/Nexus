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
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
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
	if strings.HasPrefix(query, "github:") {
		repo := strings.TrimPrefix(query, "github:")
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
	url := fmt.Sprintf("https://api.github.com/search/code?q=%s&per_page=10", searchQuery)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub API 请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GitHub API 返回 %d: %s", resp.StatusCode, string(body))
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
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("解析 GitHub API 响应失败: %w", err)
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
		return nil, fmt.Errorf("无效的仓库格式: %s (格式: owner/repo)", repo)
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s", parts[0], parts[1])
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil
	}

	var repoInfo struct {
		FullName    string `json:"full_name"`
		HTMLURL     string `json:"html_url"`
		Description string `json:"description"`
		Topics      []string `json:"topics"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&repoInfo); err != nil {
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

// ───────────────────────────── 安装 ─────────────────────────────

// Install 通过标识符安装技能。
//
// 支持的标识符格式:
//   - "github:owner/repo" — 从 GitHub 仓库安装
//   - "github:owner/repo/path" — 从 GitHub 仓库的子路径安装
//   - "https://..." — 从 URL 下载 SKILL.md
//
// 安装流程:
//  1. 解析标识符并下载 SKILL.md 内容
//  2. 解析内容验证技能结构
//  3. 安全扫描 (当前为存根实现)
//  4. 移动到技能目录
//  5. 记录安装来源到 lock.json
func (h *SkillsHub) Install(ctx context.Context, identifier string) (*Skill, error) {
	var content []byte
	var source string
	var err error

	// 按标识符类型下载
	switch {
	case strings.HasPrefix(identifier, "github:"):
		content, source, err = h.downloadFromGitHub(ctx, identifier)
	case strings.HasPrefix(identifier, "http://") || strings.HasPrefix(identifier, "https://"):
		content, source, err = h.downloadFromURL(ctx, identifier)
	default:
		return nil, fmt.Errorf("不支持的标识符格式: %s", identifier)
	}

	if err != nil {
		return nil, fmt.Errorf("下载失败: %w", err)
	}

	// 2. 解析技能
	skill, err := ParseSkillMarkdown(content)
	if err != nil {
		return nil, fmt.Errorf("解析 SKILL.md 失败: %w", err)
	}

	// 3. 安全扫描 (存根 — 未来实现)
	if warn := securityScan(skill, content); warn != "" {
		slog.Warn("skill: security scan warning", "name", skill.Name, "warning", warn)
	}

	// 4. 安装到技能目录
	targetDir := filepath.Join(h.skillsDir, skill.Name)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return nil, fmt.Errorf("创建技能目录失败: %w", err)
	}

	targetFile := filepath.Join(targetDir, "SKILL.md")
	if err := os.WriteFile(targetFile, content, 0644); err != nil {
		return nil, fmt.Errorf("写入 SKILL.md 失败: %w", err)
	}

	// 5. 记录安装来源
	if err := h.writeLockFile(skill.Name, source, identifier); err != nil {
		slog.Warn("skill: failed to write lock.json", "name", skill.Name, "error", err)
	}

	skill.Path = targetFile
	slog.Info("skill: installed", "name", skill.Name, "source", source)

	return skill, nil
}

// ───────────────────────────── 内部方法 ─────────────────────────────

// downloadFromGitHub 从 GitHub 仓库下载 SKILL.md。
func (h *SkillsHub) downloadFromGitHub(ctx context.Context, identifier string) ([]byte, string, error) {
	// 解析 github:owner/repo[/path]
	rest := strings.TrimPrefix(identifier, "github:")
	parts := strings.SplitN(rest, "/", 3)

	if len(parts) < 2 {
		return nil, "", fmt.Errorf("无效的 GitHub 标识符: %s", identifier)
	}

	owner, repo := parts[0], parts[1]
	subPath := ""
	if len(parts) == 3 {
		subPath = parts[2]
	}

	// 构建原始内容 URL
	ref := "main"
	filePath := "SKILL.md"
	if subPath != "" {
		filePath = subPath + "/SKILL.md"
	}

	url := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s", owner, repo, ref, filePath)

	// 如果 main 分支不存在，尝试 master
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		// 尝试 master 分支
		url = fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/master/%s", owner, repo, filePath)
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, "", err
		}
		resp, err = h.client.Do(req)
		if err != nil {
			return nil, "", err
		}
		defer resp.Body.Close()
	}

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("GitHub raw 返回 %d", resp.StatusCode)
	}

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}

	source := fmt.Sprintf("https://github.com/%s/%s", owner, repo)
	if subPath != "" {
		source += "/" + subPath
	}

	return content, source, nil
}

// downloadFromURL 从 HTTP(S) URL 下载 SKILL.md。
func (h *SkillsHub) downloadFromURL(ctx context.Context, url string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	content, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 限制 1MB
	if err != nil {
		return nil, "", err
	}

	return content, url, nil
}

// securityScan 对技能内容进行安全扫描。
// 返回风险评级字符串: "" (safe) / 警告信息 (warning) / 危险信息 (dangerous)。
func securityScan(skill *Skill, content []byte) string {
	text := string(content)

	// 1. 内容大小限制 (100KB)
	if len(content) > 100*1024 {
		return "dangerous: 技能内容过大 (" + fmt.Sprintf("%d 字节", len(content)) + "), 可能包含恶意载荷"
	}

	// 2. 危险命令检测 (direct dangerous commands)
	dangerousPatterns := []struct {
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
		// 编码混淆检测
		{regexp.MustCompile(`(?i)base64\s+(-d|--decode)`), "base64 解码 (可能用于混淆恶意载荷)"},
		{regexp.MustCompile(`\\x[0-9a-fA-F]{2}`), "十六进制编码 (可能用于混淆)"},
		// 命令注入模式
		{regexp.MustCompile(`\$\{[^}]+\}`), "环境变量扩展 (可能用于命令注入)"},
		{regexp.MustCompile(`\$\([^)]+\)`), "命令替换 $(...) (可能用于命令注入)"},
		// 常见混淆函数
		{regexp.MustCompile(`(?i)\b(eval|exec|spawn|subprocess)\s*[\(\[]`), "动态代码执行函数"},
		// 反弹 shell 模式
		{regexp.MustCompile(`(?i)\bnc\s+(-l|-lk|-lp)`), "netcat 监听 (可能的反弹 shell)"},
		{regexp.MustCompile(`(?i)\bncat\s+(-l|--listen)`), "ncat 监听 (可能的反弹 shell)"},
		{regexp.MustCompile(`(?i)\bsocat\s+`), "socat 网络转发 (可能的反弹 shell)"},
	}

	for _, p := range dangerousPatterns {
		if p.re.MatchString(text) {
			return "dangerous: 技能包含潜在危险命令模式: " + p.msg
		}
	}

	// 3. 中等风险检测 (warning level)
	warningPatterns := []struct {
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

	var warnings []string
	for _, p := range warningPatterns {
		if p.re.MatchString(text) {
			warnings = append(warnings, p.msg)
		}
	}

	// 4. 危险组合检测 (如 wget + sudo)
	hasDownload := regexp.MustCompile(`(?i)(?:wget|curl)\s+`).MatchString(text)
	hasSudo := regexp.MustCompile(`(?i)\bsudo\b`).MatchString(text)
	if hasDownload && hasSudo {
		warnings = append(warnings, "下载 + sudo 提权组合 (可能下载并执行恶意脚本)")
	}

	// 5. 文件系统操作检测
	hasFSOps := regexp.MustCompile(`(?i)(?:rm\s+|mv\s+|cp\s+|mkdir\s+|touch\s+|ln\s+)`).MatchString(text)
	if hasFSOps {
		warnings = append(warnings, "包含文件系统操作命令")
	}

	if len(warnings) > 0 {
		return "warning: " + strings.Join(warnings, "; ")
	}

	slog.Debug("skill: security scan passed", "name", skill.Name)
	return ""
}

// ───────────────────────────── Lock 文件 ─────────────────────────────

// lockEntry 表示 lock.json 中的一条记录。
type lockEntry struct {
	Name       string `json:"name"`
	Source     string `json:"source"`
	Identifier string `json:"identifier"`
	InstalledAt string `json:"installed_at"`
}

// lockFile 表示 lock.json 文件的结构。
type lockFile struct {
	Skills []lockEntry `json:"skills"`
}

// readLockFile 读取技能中心的 lock.json 文件。
func (h *SkillsHub) readLockFile() (*lockFile, error) {
	path := filepath.Join(h.skillsDir, ".hub", "lock.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &lockFile{}, nil
		}
		return nil, err
	}

	var lf lockFile
	if err := json.Unmarshal(data, &lf); err != nil {
		return &lockFile{}, nil
	}
	return &lf, nil
}

// writeLockFile 记录技能安装来源到 lock.json。
func (h *SkillsHub) writeLockFile(name, source, identifier string) error {
	hubDir := filepath.Join(h.skillsDir, ".hub")
	if err := os.MkdirAll(hubDir, 0755); err != nil {
		return err
	}

	lf, err := h.readLockFile()
	if err != nil {
		return err
	}

	// 更新或追加条目
	found := false
	for i, entry := range lf.Skills {
		if entry.Name == name {
			lf.Skills[i] = lockEntry{
				Name:        name,
				Source:      source,
				Identifier:  identifier,
				InstalledAt: time.Now().UTC().Format(time.RFC3339),
			}
			found = true
			break
		}
	}
	if !found {
		lf.Skills = append(lf.Skills, lockEntry{
			Name:        name,
			Source:      source,
			Identifier:  identifier,
			InstalledAt: time.Now().UTC().Format(time.RFC3339),
		})
	}

	data, err := json.MarshalIndent(lf, "", "  ")
	if err != nil {
		return err
	}

	path := filepath.Join(hubDir, "lock.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
