package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	pkgerrors "nexus-agent/internal/errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

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
		return nil, pkgerrors.New(pkgerrors.SkillIO, fmt.Sprintf("不支持的标识符格式: %s", identifier))
	}

	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.NetworkHTTP, "下载失败", err)
	}

	// 2. 解析技能
	skill, err := ParseSkillMarkdown(content)
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.SkillIO, "解析 SKILL.md 失败", err)
	}

	// 3. 安全扫描 — 阻断危险技能
	if warn := securityScan(skill, content); warn != "" {
		slog.Warn("skill: security scan warning", "name", skill.Name, "warning", warn)
		if strings.HasPrefix(warn, "dangerous:") {
			return nil, pkgerrors.New(pkgerrors.SkillIO, fmt.Sprintf("安全扫描拒绝安装: %s", warn))
		}
	}

	// 3.5 验证技能名称安全性 (防止路径遍历)
	if err := sanitizeSkillName(skill.Name); err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.SkillIO, "无效的技能名称", err)
	}

	// 4. 安装到技能目录
	targetDir := filepath.Join(h.skillsDir, skill.Name)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.SkillIO, "创建技能目录失败", err)
	}

	targetFile := filepath.Join(targetDir, "SKILL.md")
	if err := os.WriteFile(targetFile, content, 0644); err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.SkillIO, "写入 SKILL.md 失败", err)
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
		return nil, "", pkgerrors.New(pkgerrors.SkillIO, fmt.Sprintf("无效的 GitHub 标识符: %s", identifier))
	}

	owner, repo := parts[0], parts[1]
	subPath := ""
	if len(parts) == 3 {
		subPath = parts[2]
	}

	// 验证 owner/repo 不含路径遍历字符
	for _, p := range []string{owner, repo} {
		if strings.ContainsAny(p, "/\\?#&") || strings.Contains(p, "..") {
			return nil, "", pkgerrors.New(pkgerrors.SkillIO, fmt.Sprintf("无效的 GitHub 标识符组件: %s", p))
		}
	}
	if subPath != "" && (strings.Contains(subPath, "..") || strings.HasPrefix(subPath, "/")) {
		return nil, "", pkgerrors.New(pkgerrors.SkillIO, fmt.Sprintf("无效的 GitHub 子路径: %s", subPath))
	}

	// 构建原始内容 URL (使用 PathEscape 防止 URL 注入)
	ref := "main"
	filePath := "SKILL.md"
	if subPath != "" {
		filePath = subPath + "/SKILL.md"
	}

	rawURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s",
		url.PathEscape(owner), url.PathEscape(repo), ref, filePath)

	// 如果 main 分支不存在，尝试 master
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, "", err
	}

	if resp.StatusCode == 404 {
		// 关闭第一个响应体，再尝试 master 分支
		_ = resp.Body.Close()
		rawURL = fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/master/%s",
			url.PathEscape(owner), url.PathEscape(repo), filePath)
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, "", err
		}
		resp, err = h.client.Do(req)
		if err != nil {
			return nil, "", err
		}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, "", pkgerrors.New(pkgerrors.NetworkHTTP, fmt.Sprintf("GitHub raw 返回 %d", resp.StatusCode))
	}

	content, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
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
// 限制目标为 HTTPS 且禁止访问内网地址 (SSRF 防护)。
func (h *SkillsHub) downloadFromURL(ctx context.Context, rawURL string) ([]byte, string, error) {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return nil, "", pkgerrors.Wrap(pkgerrors.NetworkHTTP, "无效的 URL", err)
	}

	// 仅允许 HTTPS
	if parsedURL.Scheme != "https" {
		return nil, "", pkgerrors.New(pkgerrors.NetworkHTTP, fmt.Sprintf("仅支持 HTTPS URL，收到: %s", parsedURL.Scheme))
	}

	// SSRF 防护: 解析主机名并检查是否为内网地址
	host := parsedURL.Hostname()
	if host == "" {
		return nil, "", pkgerrors.New(pkgerrors.NetworkHTTP, "URL 缺少主机名")
	}

	resolver := net.Resolver{}
	ips, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, "", pkgerrors.Wrap(pkgerrors.NetworkHTTP, "无法解析主机名", err)
	}
	for _, ip := range ips {
		if ip.IP.IsLoopback() || ip.IP.IsPrivate() || ip.IP.IsLinkLocalUnicast() || ip.IP.IsLinkLocalMulticast() {
			return nil, "", pkgerrors.New(pkgerrors.NetworkHTTP, fmt.Sprintf("目标地址 %s 为内网/回环地址，不允许访问", ip.IP))
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", err
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, "", pkgerrors.Wrap(pkgerrors.NetworkHTTP, "HTTP 请求失败", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, "", pkgerrors.New(pkgerrors.NetworkHTTP, fmt.Sprintf("HTTP %d", resp.StatusCode))
	}

	content, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 限制 1MB
	if err != nil {
		return nil, "", err
	}

	return content, rawURL, nil
}

// securityScan 对技能内容进行安全扫描。
// 返回风险评级字符串: "" (safe) / 警告信息 (warning) / 危险信息 (dangerous)。
func securityScan(skill *Skill, content []byte) string {
	text := string(content)

	// 1. 内容大小限制 (100KB)
	if len(content) > 100*1024 {
		return "dangerous: 技能内容过大 (" + fmt.Sprintf("%d 字节", len(content)) + "), 可能包含恶意载荷"
	}

	// 2. 危险命令检测 (使用预编译正则)
	for _, p := range dangerousPatterns {
		if p.re.MatchString(text) {
			return "dangerous: 技能包含潜在危险命令模式: " + p.msg
		}
	}

	// 3. 中等风险检测 (使用预编译正则)
	var warnings []string
	for _, p := range warningPatterns {
		if p.re.MatchString(text) {
			warnings = append(warnings, p.msg)
		}
	}

	// 4. 危险组合检测 (如 wget + sudo)
	if reDownload.MatchString(text) && reSudo.MatchString(text) {
		warnings = append(warnings, "下载 + sudo 提权组合 (可能下载并执行恶意脚本)")
	}

	// 5. 文件系统操作检测
	if reFSOps.MatchString(text) {
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
	Name        string `json:"name"`
	Source      string `json:"source"`
	Identifier  string `json:"identifier"`
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
