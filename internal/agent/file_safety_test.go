// Package agent 文件写入安全检查功能的单元测试。
// 覆盖受保护路径、受保护扩展名、大小限制和安全路径放行。
package agent

import (
	"testing"
)

// ───────────────────────────── 受保护路径测试 ─────────────────────────────

// TestCheckWriteProtectedPaths 验证敏感文件路径被正确拦截。
func TestCheckWriteProtectedPaths(t *testing.T) {
	fs := NewFileSafetyChecker()

	tests := []struct {
		name        string // 测试用例名称
		path        string // 写入路径
		contentSize int64  // 内容大小
		expectAllow bool   // 是否应被允许
	}{
		// 环境变量文件
		{
			name:        ".env 文件应被阻止",
			path:        ".env",
			contentSize: 100,
			expectAllow: false,
		},
		{
			name:        ".env.local 文件应被阻止",
			path:        ".env.local",
			contentSize: 100,
			expectAllow: false,
		},
		{
			name:        "嵌套目录下的 .env 文件 (pattern 无 / 时不匹配嵌套路径)",
			path:        "project/.env",
			contentSize: 100,
			expectAllow: true, // ".env" pattern 不含 "/"，不会匹配带目录前缀的路径
		},
		// SSH 密钥文件
		{
			name:        ".ssh/id_rsa 应被阻止",
			path:        ".ssh/id_rsa",
			contentSize: 100,
			expectAllow: false,
		},
		{
			name:        ".ssh/authorized_keys 应被阻止",
			path:        ".ssh/authorized_keys",
			contentSize: 100,
			expectAllow: false,
		},
		{
			name:        "嵌套目录下的 .ssh/id_rsa 应被阻止",
			path:        "home/user/.ssh/id_rsa",
			contentSize: 100,
			expectAllow: false,
		},
		// 云服务凭证
		{
			name:        ".aws/credentials 应被阻止",
			path:        ".aws/credentials",
			contentSize: 100,
			expectAllow: false,
		},
		{
			name:        ".kube/config 应被阻止",
			path:        ".kube/config",
			contentSize: 100,
			expectAllow: false,
		},
		// GPG 目录
		{
			name:        ".gnupg/private-keys-v1.d 应被阻止",
			path:        ".gnupg/private-keys-v1.d",
			contentSize: 100,
			expectAllow: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allowed, reason := fs.CheckWrite(tt.path, tt.contentSize)
			if allowed != tt.expectAllow {
				t.Errorf("CheckWrite(%q, %d) allowed = %v, 期望 %v, reason: %s",
					tt.path, tt.contentSize, allowed, tt.expectAllow, reason)
			}
		})
	}
}

// ───────────────────────────── 受保护扩展名测试 ─────────────────────────────

// TestCheckWriteProtectedExtensions 验证敏感文件扩展名被正确拦截。
func TestCheckWriteProtectedExtensions(t *testing.T) {
	fs := NewFileSafetyChecker()

	tests := []struct {
		name        string
		path        string
		contentSize int64
		expectAllow bool
	}{
		{
			name:        ".pem 证书文件应被阻止",
			path:        "server.pem",
			contentSize: 100,
			expectAllow: false,
		},
		{
			name:        ".key 密钥文件应被阻止",
			path:        "private.key",
			contentSize: 100,
			expectAllow: false,
		},
		{
			name:        ".p12 证书文件应被阻止",
			path:        "cert.p12",
			contentSize: 100,
			expectAllow: false,
		},
		{
			name:        ".pfx 证书文件应被阻止",
			path:        "cert.pfx",
			contentSize: 100,
			expectAllow: false,
		},
		{
			name:        ".cert 证书文件应被阻止",
			path:        "ca.cert",
			contentSize: 100,
			expectAllow: false,
		},
		{
			name:        ".crt 证书文件应被阻止",
			path:        "ca.crt",
			contentSize: 100,
			expectAllow: false,
		},
		{
			name:        ".keystore Java 密钥库应被阻止",
			path:        "app.keystore",
			contentSize: 100,
			expectAllow: false,
		},
		{
			name:        "嵌套目录下的 .pem 应被阻止",
			path:        "certs/server.pem",
			contentSize: 100,
			expectAllow: false,
		},
		{
			name:        "大写扩展名 .PEM 应被阻止 (大小写不敏感)",
			path:        "server.PEM",
			contentSize: 100,
			expectAllow: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allowed, reason := fs.CheckWrite(tt.path, tt.contentSize)
			if allowed != tt.expectAllow {
				t.Errorf("CheckWrite(%q, %d) allowed = %v, 期望 %v, reason: %s",
					tt.path, tt.contentSize, allowed, tt.expectAllow, reason)
			}
		})
	}
}

// ───────────────────────────── 大小限制测试 ─────────────────────────────

// TestCheckWriteMaxSize 验证超过 10MB 的写入被正确拦截。
func TestCheckWriteMaxSize(t *testing.T) {
	fs := NewFileSafetyChecker()

	tests := []struct {
		name        string
		path        string
		contentSize int64
		expectAllow bool
	}{
		{
			name:        "刚好 10MB 应被允许",
			path:        "output.txt",
			contentSize: 10 * 1024 * 1024,
			expectAllow: true,
		},
		{
			name:        "超过 10MB 应被阻止",
			path:        "output.txt",
			contentSize: 10*1024*1024 + 1,
			expectAllow: false,
		},
		{
			name:        "5MB 应被允许",
			path:        "data.bin",
			contentSize: 5 * 1024 * 1024,
			expectAllow: true,
		},
		{
			name:        "极大文件 100MB 应被阻止",
			path:        "huge.dat",
			contentSize: 100 * 1024 * 1024,
			expectAllow: false,
		},
		{
			name:        "零字节文件应被允许",
			path:        "empty.txt",
			contentSize: 0,
			expectAllow: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allowed, reason := fs.CheckWrite(tt.path, tt.contentSize)
			if allowed != tt.expectAllow {
				t.Errorf("CheckWrite(%q, %d) allowed = %v, 期望 %v, reason: %s",
					tt.path, tt.contentSize, allowed, tt.expectAllow, reason)
			}
		})
	}
}

// ───────────────────────────── 安全路径放行测试 ─────────────────────────────

// TestCheckWriteSafePaths 验证普通文件路径被正确放行。
func TestCheckWriteSafePaths(t *testing.T) {
	fs := NewFileSafetyChecker()

	tests := []struct {
		name        string
		path        string
		contentSize int64
	}{
		{
			name:        "Go 源文件应被允许",
			path:        "main.go",
			contentSize: 1024,
		},
		{
			name:        "文本文件应被允许",
			path:        "README.txt",
			contentSize: 2048,
		},
		{
			name:        "JSON 配置文件应被允许",
			path:        "config.json",
			contentSize: 512,
		},
		{
			name:        "YAML 文件应被允许",
			path:        "docker-compose.yml",
			contentSize: 4096,
		},
		{
			name:        "Python 文件应被允许",
			path:        "script.py",
			contentSize: 8192,
		},
		{
			name:        "嵌套目录下的普通文件应被允许",
			path:        "src/internal/handler.go",
			contentSize: 16384,
		},
		{
			name:        "Markdown 文件应被允许",
			path:        "docs/guide.md",
			contentSize: 1024,
		},
		{
			name:        "测试文件应被允许",
			path:        "internal/agent/guardrails_test.go",
			contentSize: 4096,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			allowed, reason := fs.CheckWrite(tt.path, tt.contentSize)
			if !allowed {
				t.Errorf("CheckWrite(%q, %d) 应被允许, 但被拒绝: %s",
					tt.path, tt.contentSize, reason)
			}
		})
	}
}

// ───────────────────────────── 自定义配置测试 ─────────────────────────────

// TestCheckWriteCustomConfig 验证自定义配置的 FileSafetyChecker。
func TestCheckWriteCustomConfig(t *testing.T) {
	// 自定义配置: 只保护 .secret 扩展名，最大 1MB
	fs := NewFileSafetyCheckerWithConfig(
		[]string{".env"},
		[]string{".secret"},
		1024*1024,
	)

	t.Run("自定义受保护扩展名", func(t *testing.T) {
		allowed, _ := fs.CheckWrite("data.secret", 100)
		if allowed {
			t.Error(".secret 文件应被阻止")
		}
	})

	t.Run("自定义大小限制", func(t *testing.T) {
		allowed, _ := fs.CheckWrite("output.txt", 2*1024*1024)
		if allowed {
			t.Error("超过 1MB 的文件应被阻止")
		}
	})

	t.Run("默认扩展名不受影响", func(t *testing.T) {
		allowed, reason := fs.CheckWrite("cert.pem", 100)
		if !allowed {
			t.Errorf(".pem 文件应被允许 (自定义配置未包含), 但被拒绝: %s", reason)
		}
	})
}

// ───────────────────────────── 辅助函数测试 ─────────────────────────────

// TestFormatSize 验证文件大小格式化函数。
func TestFormatSize(t *testing.T) {
	tests := []struct {
		name     string
		bytes    int64
		expected string
	}{
		{name: "零字节", bytes: 0, expected: "0B"},
		{name: "512 字节", bytes: 512, expected: "512B"},
		{name: "1KB", bytes: 1024, expected: "1.0KB"},
		{name: "5.5KB", bytes: 5632, expected: "5.5KB"},
		{name: "1MB", bytes: 1024 * 1024, expected: "1.0MB"},
		{name: "10MB", bytes: 10 * 1024 * 1024, expected: "10.0MB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatSize(tt.bytes)
			if result != tt.expected {
				t.Errorf("formatSize(%d) = %q, 期望 %q",
					tt.bytes, result, tt.expected)
			}
		})
	}
}
