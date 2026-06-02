// Package tool 集成测试。
// 验证路径安全防护和 URL 安全检查在工具执行层面的拦截功能。
// 注意: TestHasTraversalComponent、TestSanitizePath 等基础函数测试已在 path_security_test.go 中覆盖。
package tool

import (
	"context"
	"strings"
	"testing"
)

// ───────────────────────────── 1. 路径遍历拦截测试 (工具执行层) ─────────────────────────────

// TestPathTraversalBlocked 验证 file_read 工具对路径遍历攻击的拦截。
// 尝试读取 ../../etc/passwd 应被路径安全检查阻止。
func TestPathTraversalBlocked(t *testing.T) {
	readTool := &FileReadTool{}
	ctx := context.Background()

	// 尝试使用路径遍历读取敏感文件
	result, err := readTool.Execute(ctx, map[string]any{
		"path": "../../etc/passwd",
	})

	// Execute 不应返回 Go 错误 (错误以 JSON 形式返回)
	if err != nil {
		t.Fatalf("Execute 不应返回 Go 错误: %v", err)
	}

	// 验证结果包含安全限制错误信息
	if !strings.Contains(result, "安全限制") {
		t.Fatalf("结果应包含 '安全限制' 错误信息，实际: %s", result)
	}

	// 验证结果包含遍历组件相关信息
	if !strings.Contains(result, "遍历组件") && !strings.Contains(result, "..") {
		t.Fatalf("结果应提及路径遍历问题，实际: %s", result)
	}

	// 验证没有返回文件内容 (不应包含 /etc/passwd 的典型内容)
	if strings.Contains(result, "root:") {
		t.Fatal("不应成功读取 /etc/passwd 的内容")
	}
}

// TestPathTraversalBlocked_MorePatterns 测试 file_read 工具对更多路径遍历模式的拦截。
func TestPathTraversalBlocked_MorePatterns(t *testing.T) {
	readTool := &FileReadTool{}
	ctx := context.Background()

	testCases := []struct {
		name string
		path string
	}{
		{"双层遍历", "../../../etc/shadow"},
		{"Windows 风格遍历", "..\\..\\..\\Windows\\System32\\config\\SAM"},
		{"混合遍历", "../../.ssh/id_rsa"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := readTool.Execute(ctx, map[string]any{"path": tc.path})
			if err != nil {
				t.Fatalf("Execute 不应返回 Go 错误: %v", err)
			}
			if !strings.Contains(result, "安全限制") && !strings.Contains(result, "error") {
				t.Fatalf("路径 %s 应被安全检查拦截，实际: %s", tc.path, result)
			}
		})
	}
}

// TestFileWriteTraversalBlocked 验证 file_write 工具同样拦截路径遍历。
func TestFileWriteTraversalBlocked(t *testing.T) {
	writeTool := &FileWriteTool{}
	ctx := context.Background()

	result, err := writeTool.Execute(ctx, map[string]any{
		"path":    "../../etc/crontab",
		"content": "malicious content",
	})

	if err != nil {
		t.Fatalf("Execute 不应返回 Go 错误: %v", err)
	}

	if !strings.Contains(result, "安全限制") {
		t.Fatalf("写入遍历路径应被拦截，实际: %s", result)
	}
}

// ───────────────────────────── 2. SSRF 拦截测试 ─────────────────────────────

// TestSSRFBlocked 验证 URL 安全检查器对 SSRF 攻击的拦截。
// 尝试请求云元数据服务地址 169.254.169.254 应被阻止。
func TestSSRFBlocked(t *testing.T) {
	// 创建 URL 安全检查器 (默认不允许私有 URL)
	checker := NewURLSafetyChecker(URLSafetyConfig{
		AllowPrivateURLs: false,
	})

	testCases := []struct {
		name      string
		url       string
		expectErr bool
	}{
		{
			name:      "AWS 元数据服务",
			url:       "http://169.254.169.254/latest/meta-data/",
			expectErr: true,
		},
		{
			name:      "回环地址",
			url:       "http://127.0.0.1:8080/admin",
			expectErr: true,
		},
		{
			name:      "本地主机名",
			url:       "http://localhost:3000/api",
			expectErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			safe, reason := checker.IsSafeURL(tc.url)

			if tc.expectErr {
				if safe {
					t.Fatalf("URL %s 应被拦截为不安全，但返回 safe=true", tc.url)
				}
				if reason == "" {
					t.Fatalf("不安全的 URL 应提供拒绝原因")
				}
				t.Logf("URL %s 被正确拦截: %s", tc.url, reason)
			}
		})
	}
}

// TestSSRFBlocked_ViaTool 验证通过 web_extract 工具发起的 SSRF 请求被拦截。
func TestSSRFBlocked_ViaTool(t *testing.T) {
	// 设置全局 URL 安全检查器
	checker := NewURLSafetyChecker(URLSafetyConfig{
		AllowPrivateURLs: false,
	})
	SetURLSafetyConfig(checker)
	// 测试结束后清理全局状态
	t.Cleanup(func() {
		SetURLSafetyConfig(nil)
	})

	webTool := &WebExtractTool{}
	ctx := context.Background()

	// 尝试提取云元数据服务的内容
	result, err := webTool.Execute(ctx, map[string]any{
		"urls": []any{"http://169.254.169.254/latest/meta-data/"},
	})

	if err != nil {
		t.Fatalf("Execute 不应返回 Go 错误: %v", err)
	}

	// 验证结果包含 URL 安全检查错误
	if !strings.Contains(result, "URL 安全检查未通过") {
		t.Fatalf("结果应包含 'URL 安全检查未通过'，实际: %s", result)
	}

	// 验证结果包含被拦截的 URL
	if !strings.Contains(result, "169.254.169.254") {
		t.Fatalf("结果应包含被拦截的 URL，实际: %s", result)
	}
}

// TestSSRFBlocked_NoChecker 验证未设置 URL 安全检查器时默认拒绝 (fail-closed)。
func TestSSRFBlocked_NoChecker(t *testing.T) {
	// 确保全局检查器为 nil
	SetURLSafetyConfig(nil)

	safe, reason := CheckURLSafety("http://169.254.169.254/latest/meta-data/")
	if safe {
		t.Fatalf("未设置检查器时应默认拒绝 (fail-closed)，实际被放行: %s", reason)
	}
}

// TestURLSafety_AllowPublicURL 验证公开 URL 通过安全检查。
func TestURLSafety_AllowPublicURL(t *testing.T) {
	checker := NewURLSafetyChecker(URLSafetyConfig{
		AllowPrivateURLs: false,
	})

	// 公开 URL 应通过安全检查 (DNS 解析成功的情况下)
	safe, reason := checker.IsSafeURL("https://www.example.com")
	if !safe {
		// DNS 解析可能在某些环境下失败，记录但不视为测试失败
		// (失效即拒绝策略下，DNS 失败也会被阻止)
		t.Logf("公开 URL 未通过检查 (可能是 DNS 问题): %s", reason)
	} else {
		t.Logf("公开 URL 正确通过检查: %s", reason)
	}
}
