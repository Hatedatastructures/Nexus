// Package tool URL 安全检查功能的单元测试。
// 覆盖 SSRF 防护、私有 IP 检测、全局检查器行为。
package tool

import (
	"net"
	"testing"
)

// ───────────────────────────── isPrivateIP 测试 ─────────────────────────────

// TestIsPrivateIP 使用表驱动测试验证私有 IP 地址检测。
func TestIsPrivateIP(t *testing.T) {
	tests := []struct {
		name     string // 测试用例名称
		ip       string // IP 地址字符串
		expected bool   // 是否为私有 IP
	}{
		// RFC 1918 私有地址范围
		{
			name:     "10.0.0.1 (10.0.0.0/8)",
			ip:       "10.0.0.1",
			expected: true,
		},
		{
			name:     "10.255.255.255 (10.0.0.0/8 边界)",
			ip:       "10.255.255.255",
			expected: true,
		},
		{
			name:     "172.16.0.1 (172.16.0.0/12 起始)",
			ip:       "172.16.0.1",
			expected: true,
		},
		{
			name:     "172.31.255.255 (172.16.0.0/12 结束)",
			ip:       "172.31.255.255",
			expected: true,
		},
		{
			name:     "192.168.1.1 (192.168.0.0/16)",
			ip:       "192.168.1.1",
			expected: true,
		},
		{
			name:     "192.168.0.0 (192.168.0.0/16 起始)",
			ip:       "192.168.0.0",
			expected: true,
		},
		// 非私有地址
		{
			name:     "8.8.8.8 (公网 DNS)",
			ip:       "8.8.8.8",
			expected: false,
		},
		{
			name:     "1.1.1.1 (Cloudflare DNS)",
			ip:       "1.1.1.1",
			expected: false,
		},
		{
			name:     "172.32.0.1 (172.16.0.0/12 之外)",
			ip:       "172.32.0.1",
			expected: false,
		},
		// IPv6 ULA (fc00::/7)
		{
			name:     "fd00::1 (IPv6 ULA)",
			ip:       "fd00::1",
			expected: true,
		},
		{
			name:     "fc00::1 (IPv6 ULA 起始)",
			ip:       "fc00::1",
			expected: true,
		},
		// IPv6 全局地址
		{
			name:     "2001:db8::1 (文档地址，非私有)",
			ip:       "2001:db8::1",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("无法解析 IP: %s", tt.ip)
			}
			result := isPrivateIP(ip)
			if result != tt.expected {
				t.Errorf("isPrivateIP(%s) = %v, 期望 %v",
					tt.ip, result, tt.expected)
			}
		})
	}
}

// ───────────────────────────── isCGNAT 测试 ─────────────────────────────

// TestIsCGNAT 使用表驱动测试验证 CGNAT 地址检测 (100.64.0.0/10)。
func TestIsCGNAT(t *testing.T) {
	tests := []struct {
		name     string
		ip       string
		expected bool
	}{
		{
			name:     "100.64.0.1 (CGNAT 起始)",
			ip:       "100.64.0.1",
			expected: true,
		},
		{
			name:     "100.127.255.255 (CGNAT 结束)",
			ip:       "100.127.255.255",
			expected: true,
		},
		{
			name:     "100.65.0.0 (CGNAT 范围内)",
			ip:       "100.65.0.0",
			expected: true,
		},
		{
			name:     "100.63.0.1 (CGNAT 之前)",
			ip:       "100.63.0.1",
			expected: false,
		},
		{
			name:     "100.128.0.1 (CGNAT 之后)",
			ip:       "100.128.0.1",
			expected: false,
		},
		{
			name:     "8.8.8.8 (公网，非 CGNAT)",
			ip:       "8.8.8.8",
			expected: false,
		},
		// IPv6 不属于 CGNAT
		{
			name:     "fd00::1 (IPv6，非 CGNAT)",
			ip:       "fd00::1",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("无法解析 IP: %s", tt.ip)
			}
			result := isCGNAT(ip)
			if result != tt.expected {
				t.Errorf("isCGNAT(%s) = %v, 期望 %v",
					tt.ip, result, tt.expected)
			}
		})
	}
}

// ───────────────────────────── IsSafeURL 测试 ─────────────────────────────

// TestIsSafeURL 使用表驱动测试验证 URL 安全检查。
// 注意: 涉及 DNS 解析的用例可能受网络环境影响。
func TestIsSafeURL(t *testing.T) {
	// 创建默认配置的检查器 (不允许私有 URL)
	checker := NewURLSafetyChecker(URLSafetyConfig{
		AllowPrivateURLs: false,
	})

	tests := []struct {
		name      string // 测试用例名称
		url       string // 输入 URL
		expectSafe bool  // 是否应被判定为安全
	}{
		// 不允许的协议
		{
			name:       "FTP 协议应被拒绝",
			url:        "ftp://example.com/file",
			expectSafe: false,
		},
		{
			name:       "File 协议应被拒绝",
			url:        "file:///etc/passwd",
			expectSafe: false,
		},
		// 始终屏蔽的 IP
		{
			name:       "AWS 元数据地址应被拒绝",
			url:        "http://169.254.169.254/latest/meta-data/",
			expectSafe: false,
		},
		// 回环地址
		{
			name:       "127.0.0.1 应被拒绝",
			url:        "http://127.0.0.1:8080/api",
			expectSafe: false,
		},
		{
			name:       "localhost 应被拒绝 (回环)",
			url:        "http://localhost:3000",
			expectSafe: false,
		},
		// 私有 IP (在不允许私有 URL 的配置下)
		{
			name:       "10.x.x.x 应被拒绝",
			url:        "http://10.0.0.1:8080",
			expectSafe: false,
		},
		{
			name:       "192.168.x.x 应被拒绝",
			url:        "http://192.168.1.1",
			expectSafe: false,
		},
		// CGNAT 地址
		{
			name:       "100.64.x.x (CGNAT) 应被拒绝",
			url:        "http://100.64.0.1",
			expectSafe: false,
		},
		// 缺少主机名
		{
			name:       "缺少主机名应被拒绝",
			url:        "http://",
			expectSafe: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			safe, _ := checker.IsSafeURL(tt.url)
			if safe != tt.expectSafe {
				t.Errorf("IsSafeURL(%q) safe = %v, 期望 %v",
					tt.url, safe, tt.expectSafe)
			}
		})
	}
}

// TestIsSafeURLAllowPrivate 测试允许私有 URL 时的行为。
func TestIsSafeURLAllowPrivate(t *testing.T) {
	// 创建允许私有 URL 的检查器
	checker := NewURLSafetyChecker(URLSafetyConfig{
		AllowPrivateURLs: true,
	})

	tests := []struct {
		name       string
		url        string
		expectSafe bool
	}{
		{
			name:       "允许私有 URL 时 10.x 可通过",
			url:        "http://10.0.0.1:8080",
			expectSafe: true,
		},
		{
			name:       "允许私有 URL 时 192.168.x 可通过",
			url:        "http://192.168.1.1",
			expectSafe: true,
		},
		// 即使允许私有 URL，以下地址仍应被拦截
		{
			name:       "即使允许私有 URL，169.254.169.254 仍被拦截",
			url:        "http://169.254.169.254/latest/meta-data/",
			expectSafe: false,
		},
		{
			name:       "即使允许私有 URL，127.0.0.1 仍被拦截",
			url:        "http://127.0.0.1:8080",
			expectSafe: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			safe, _ := checker.IsSafeURL(tt.url)
			if safe != tt.expectSafe {
				t.Errorf("IsSafeURL(%q) safe = %v, 期望 %v",
					tt.url, safe, tt.expectSafe)
			}
		})
	}
}

// ───────────────────────────── CheckURLSafety 全局函数测试 ─────────────────────────────

// TestCheckURLSafetyDefaultSafe 测试全局检查器未初始化时默认返回安全。
func TestCheckURLSafetyDefaultSafe(t *testing.T) {
	// 保存原始值，测试后恢复
	original := globalURLSafety
	globalURLSafety = nil
	defer func() { globalURLSafety = original }()

	safe, reason := CheckURLSafety("http://169.254.169.254/latest/meta-data/")
	if !safe {
		t.Errorf("全局检查器未初始化时 CheckURLSafety 应返回 safe=true, 但返回了 false, reason: %s", reason)
	}
}

// TestCheckURLSafetyInitialized 测试全局检查器初始化后正确拦截危险 URL。
func TestCheckURLSafetyInitialized(t *testing.T) {
	// 保存原始值，测试后恢复
	original := globalURLSafety
	defer func() { globalURLSafety = original }()

	// 初始化全局检查器
	SetURLSafetyConfig(NewURLSafetyChecker(URLSafetyConfig{
		AllowPrivateURLs: false,
	}))

	safe, reason := CheckURLSafety("http://169.254.169.254/latest/meta-data/")
	if safe {
		t.Errorf("初始化后 CheckURLSafety 应拦截元数据 URL, 但返回了 safe=true, reason: %s", reason)
	}
}

// ───────────────────────────── ExtraBlockedIPs 测试 ─────────────────────────────

// TestExtraBlockedIPs 测试自定义屏蔽 IP 列表。
func TestExtraBlockedIPs(t *testing.T) {
	checker := NewURLSafetyChecker(URLSafetyConfig{
		AllowPrivateURLs: true,
		BlockedIPs:       []string{"10.0.0.1"},
	})

	// 验证自定义 IP 在检查器中被标记为屏蔽
	if !checker.extraBlocked["10.0.0.1"] {
		t.Error("10.0.0.1 应在 extraBlocked 集合中")
	}
	if checker.extraBlocked["10.0.0.2"] {
		t.Error("10.0.0.2 不应在 extraBlocked 集合中")
	}
}
