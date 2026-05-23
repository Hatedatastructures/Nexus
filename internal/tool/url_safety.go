// Package tool 提供 URL 安全检查工具。
// 包含 SSRF 防护、DNS 解析验证、IP 分类等功能。
// 采用失效即拒绝 (fail-closed) 设计: DNS 失败时阻止请求。
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
)

// ───────────────────────────── 常量与配置 ─────────────────────────────

// _ALWAYS_BLOCKED_IPS 包含始终被屏蔽的危险 IP 地址。
// 这些地址与云元数据服务、链路本地地址等关联，存在 SSRF 风险。
var _ALWAYS_BLOCKED_IPS = map[string]bool{
	"169.254.169.254": true, // AWS/GCP/Azure 元数据服务
	"fd00:ec2::254":   true, // AWS IPv6 元数据服务
	"fe80::1":         true, // 链路本地网关
	"0.0.0.0":         true, // 未指定地址
}

// URLSafetyConfig 定义 URL 安全检查的配置。
// 与 config.URLSafetyConfig 对应，支持额外的自定义屏蔽 IP。
type URLSafetyConfig struct {
	AllowPrivateURLs bool     `json:"allow_private_urls"` // 是否允许访问私有 IP URL
	BlockedIPs       []string `json:"blocked_ips"`        // 额外屏蔽的 IP 列表
}

// ───────────────────────────── 全局 URL 安全检查器 ─────────────────────────────

// 包级 URL 安全检查器单例，用于中间件模式的主动拦截。
// 在代理初始化时通过 SetURLSafetyConfig() 设置。
var globalURLSafety *URLSafetyChecker

// SetURLSafetyConfig 设置全局 URL 安全检查器。
// 应在代理启动时调用，在所有需要 URL 安全检查的工具 Execute 调用之前。
func SetURLSafetyConfig(checker *URLSafetyChecker) {
	globalURLSafety = checker
}

// CheckURLSafety 使用全局检查器检查给定 URL 是否安全。
// 返回 (是否安全, 原因说明)。若全局检查器未初始化，则默认安全。
// 供 web/browser 等工具在执行 HTTP 请求前调用。
func CheckURLSafety(rawURL string) (bool, string) {
	if globalURLSafety == nil {
		return true, "URL 安全检查器未初始化，跳过检查"
	}
	return globalURLSafety.IsSafeURL(rawURL)
}

// ───────────────────────────── URL 安全检查器 ─────────────────────────────

// URLSafetyChecker 实现 URL 安全检查功能。
// 防止 SSRF 攻击，验证目标地址不属于内部网络。
type URLSafetyChecker struct {
	config       URLSafetyConfig
	extraBlocked map[string]bool // 额外屏蔽的 IP 集合
}

// NewURLSafetyChecker 创建一个新的 URL 安全检查器。
func NewURLSafetyChecker(cfg URLSafetyConfig) *URLSafetyChecker {
	extra := make(map[string]bool, len(cfg.BlockedIPs))
	for _, ip := range cfg.BlockedIPs {
		extra[ip] = true
	}
	return &URLSafetyChecker{
		config:       cfg,
		extraBlocked: extra,
	}
}

// IsSafeURL 检查给定的 URL 是否安全可访问。
// 返回 (是否安全, 原因说明)。
// 失效即拒绝: 任何 DNS 解析或 URL 解析失败都会阻止请求。
func (c *URLSafetyChecker) IsSafeURL(rawURL string) (bool, string) {
	// ── URL 解析 ──
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false, fmt.Sprintf("URL 解析失败: %v", err)
	}

	// 仅允许 http/https 协议
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return false, fmt.Sprintf("不允许的协议: %s", parsed.Scheme)
	}

	// 提取主机名
	hostname := parsed.Hostname()
	if hostname == "" {
		return false, "URL 缺少主机名"
	}

	// ── DNS 解析 (失效即拒绝) ──
	ips, err := net.LookupIP(hostname)
	if err != nil {
		slog.Warn("URL safety check: DNS resolution failed, blocking request", "host", hostname, "err", err)
		return false, fmt.Sprintf("DNS 解析失败 (安全策略阻止): %v", err)
	}

	if len(ips) == 0 {
		return false, "DNS 解析返回空结果"
	}

	// ── 逐个 IP 检查 ──
	for _, ip := range ips {
		if reason, safe := c.checkIP(ip); !safe {
			return false, reason
		}
	}

	return true, "URL 安全检查通过"
}

// checkIP 对单个 IP 地址进行安全分类检查。
func (c *URLSafetyChecker) checkIP(ip net.IP) (string, bool) {
	ipStr := ip.String()

	// ── 始终屏蔽的 IP ──
	if _ALWAYS_BLOCKED_IPS[ipStr] {
		return fmt.Sprintf("IP %s 在始终屏蔽列表中", ipStr), false
	}

	// ── 额外屏蔽的 IP ──
	if c.extraBlocked[ipStr] {
		return fmt.Sprintf("IP %s 在自定义屏蔽列表中", ipStr), false
	}

	// ── 回环地址 (127.0.0.0/8, ::1) ──
	if ip.IsLoopback() {
		return fmt.Sprintf("IP %s 是回环地址", ipStr), false
	}

	// ── 链路本地地址 (169.254.0.0/16, fe80::/10) ──
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return fmt.Sprintf("IP %s 是链路本地地址", ipStr), false
	}

	// ── CGNAT 地址 (100.64.0.0/10) ──
	if isCGNAT(ip) {
		return fmt.Sprintf("IP %s 是 CGNAT 地址 (100.64.0.0/10)", ipStr), false
	}

	// ── 私有地址 (RFC 1918) ──
	if !c.config.AllowPrivateURLs && isPrivateIP(ip) {
		return fmt.Sprintf("IP %s 是私有地址 (配置不允许访问私有 URL)", ipStr), false
	}

	return "", true
}

// isPrivateIP 检查 IP 是否为私有地址 (RFC 1918/RFC 4193)。
func isPrivateIP(ip net.IP) bool {
	if ip4 := ip.To4(); ip4 != nil {
		// 10.0.0.0/8
		if ip4[0] == 10 {
			return true
		}
		// 172.16.0.0/12
		if ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31 {
			return true
		}
		// 192.168.0.0/16
		if ip4[0] == 192 && ip4[1] == 168 {
			return true
		}
		return false
	}
	// fc00::/7 (ULA)
	return len(ip) == net.IPv6len && (ip[0]&0xfe) == 0xfc
}

// isCGNAT 检查 IP 是否属于 CGNAT 地址段 (100.64.0.0/10)。
func isCGNAT(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	return ip4[0] == 100 && (ip4[1]&0xc0) == 64
}

// ───────────────────────────── URL 安全工具 ─────────────────────────────

// URLSafetyTool 提供 URL 安全检查的工具接口。
type URLSafetyTool struct {
	checker *URLSafetyChecker
}

// Name 返回工具名称。
func (t *URLSafetyTool) Name() string { return "url_safety_check" }

// Description 返回工具描述。
func (t *URLSafetyTool) Description() string {
	return "检查 URL 是否安全可访问。检测 SSRF 风险，验证目标地址不属于内部网络。"
}

// Toolset 返回工具所属工具集。
func (t *URLSafetyTool) Toolset() string { return "security" }

// Emoji 返回工具图标。
func (t *URLSafetyTool) Emoji() string { return "🛡️" }

// IsAvailable 始终可用。
func (t *URLSafetyTool) IsAvailable() bool { return true }

// MaxResultChars 返回结果最大字符数。
func (t *URLSafetyTool) MaxResultChars() int { return 5000 }

// Schema 返回工具的 JSON Schema。
func (t *URLSafetyTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "url_safety_check",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "要检查的 URL",
				},
			},
			"required": []string{"url"},
		},
	}
}

// Execute 执行 URL 安全检查。
func (t *URLSafetyTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	rawURL, ok := args["url"].(string)
	if !ok || rawURL == "" {
		return ToolError("参数 url 是必填项且必须为字符串"), nil
	}

	if t.checker == nil {
		t.checker = NewURLSafetyChecker(URLSafetyConfig{})
	}

	safe, reason := t.checker.IsSafeURL(rawURL)

	result, _ := json.Marshal(map[string]any{
		"url":    rawURL,
		"safe":   safe,
		"reason": reason,
	})

	return string(result), nil
}

// ───────────────────────────── init 注册 ─────────────────────────────

func init() {
	slog.Debug("registering URL safety check tool")
	GetRegistry().Register(&URLSafetyTool{})
}
