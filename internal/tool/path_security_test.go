package tool

import (
	"os"
	"path/filepath"
	"testing"
)

// ───────────────────────────── HasTraversalComponent 测试 ─────────────────────────────

// TestHasTraversalComponent 使用表驱动测试验证路径遍历组件检测。
func TestHasTraversalComponent(t *testing.T) {
	tests := []struct {
		name     string // 测试用例名称
		path     string // 输入路径
		expected bool   // 是否应检测到遍历组件
	}{
		// 正常路径 — 不应检测到遍历
		{
			name:     "简单文件名",
			path:     "file.txt",
			expected: false,
		},
		{
			name:     "嵌套目录路径",
			path:     "a/b/c/file.txt",
			expected: false,
		},
		{
			name:     "当前目录引用",
			path:     "./file.txt",
			expected: false,
		},
		{
			name:     "绝对路径",
			path:     "/home/user/file.txt",
			expected: false,
		},
		// 含 .. 的路径 — 应检测到遍历
		{
			name:     "简单的 .. 遍历",
			path:     "../file.txt",
			expected: true,
		},
		{
			name:     "目录中的 .. 遍历",
			path:     "a/b/../../../etc/passwd",
			expected: true,
		},
		{
			name:     "混合路径中的 .. (清理后无遍历)",
			path:     "a/b/../c/d/../../e",
			expected: false, // filepath.Clean 后变为 "a/e"，无遍历组件
		},
		{
			name:     "含无法完全清理的 ..",
			path:     "a/b/../../c/../../../d",
			expected: true,
		},
		{
			name:     "开头连续遍历",
			path:     "../../secret.txt",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := HasTraversalComponent(tt.path)
			if result != tt.expected {
				t.Errorf("HasTraversalComponent(%q) = %v, 期望 %v",
					tt.path, result, tt.expected)
			}
		})
	}
}

// ───────────────────────────── SanitizePath 测试 ─────────────────────────────

// TestSanitizePath 使用表驱动测试验证路径净化功能。
func TestSanitizePath(t *testing.T) {
	tests := []struct {
		name     string // 测试用例名称
		input    string // 输入路径
		expected string // 期望的净化结果
	}{
		// 正常路径 — 不应被修改
		{
			name:     "简单文件名",
			input:    "file.txt",
			expected: "file.txt",
		},
		{
			name:     "嵌套路径",
			input:    "a/b/c.txt",
			expected: "a/b/c.txt",
		},
		// 遍历组件移除
		{
			name:     "移除前导遍历",
			input:    "../file.txt",
			expected: "file.txt",
		},
		{
			name:     "移除中间遍历",
			input:    "a/../b/file.txt",
			expected: "a/b/file.txt",
		},
		{
			name:     "移除所有遍历组件",
			input:    "../../etc/passwd",
			expected: "etc/passwd",
		},
		// 当前目录引用移除
		{
			name:     "移除当前目录引用",
			input:    "./a/./b/./file.txt",
			expected: "a/b/file.txt",
		},
		// 空路径处理
		{
			name:     "全部为遍历组件",
			input:    "../..",
			expected: ".",
		},
		{
			name:     "空字符串",
			input:    "",
			expected: ".",
		},
		// 反斜杠统一分隔符
		{
			name:     "反斜杠转正斜杠",
			input:    `a\b\c.txt`,
			expected: "a/b/c.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SanitizePath(tt.input)
			if result != tt.expected {
				t.Errorf("SanitizePath(%q) = %q, 期望 %q",
					tt.input, result, tt.expected)
			}
		})
	}
}

// TestSanitizePathInvisibleChars 使用程序化构建的字符串测试不可见 Unicode 字符移除。
// 使用 string(rune(...)) 避免源文件中嵌入不可见字符导致编译器报错。
func TestSanitizePathInvisibleChars(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "移除零宽空格 (U+200B)",
			input:    "file" + string(rune(0x200B)) + ".txt",
			expected: "file.txt",
		},
		{
			name:     "移除 BOM 零宽不换行空格 (U+FEFF)",
			input:    string(rune(0xFEFF)) + "hidden.txt",
			expected: "hidden.txt",
		},
		{
			name:     "移除零宽非连接符 (U+200C)",
			input:    "a" + string(rune(0x200C)) + "b/c.txt",
			expected: "ab/c.txt", // U+200C 被移除后 a 和 b 直接拼接
		},
		{
			name:     "移除零宽连接符 (U+200D)",
			input:    "a" + string(rune(0x200D)) + "b.txt",
			expected: "ab.txt",
		},
		{
			name:     "移除词连接符 (U+2060)",
			input:    "word" + string(rune(0x2060)) + ".txt",
			expected: "word.txt",
		},
		{
			name:     "普通空白字符应被保留",
			input:    "file name.txt",
			expected: "file name.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SanitizePath(tt.input)
			if result != tt.expected {
				t.Errorf("SanitizePath(%q) = %q, 期望 %q",
					tt.input, result, tt.expected)
			}
		})
	}
}

// ───────────────────────────── ValidateWithinDir 测试 ─────────────────────────────

// TestValidateWithinDir 使用表驱动测试验证目录边界验证。
// 使用 t.TempDir 创建临时目录环境，测试完成后自动清理。
func TestValidateWithinDir(t *testing.T) {
	// 创建临时目录结构
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "sub")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("创建子目录失败: %v", err)
	}

	// 在子目录中创建测试文件
	safeFile := filepath.Join(subDir, "safe.txt")
	if err := os.WriteFile(safeFile, []byte("safe"), 0o644); err != nil {
		t.Fatalf("创建测试文件失败: %v", err)
	}

	// 在允许目录外创建文件
	outsideFile := filepath.Join(tmpDir, "..", "outside.txt")
	absOutside, _ := filepath.Abs(outsideFile)

	tests := []struct {
		name        string // 测试用例名称
		path        string // 待验证的路径
		allowedDir  string // 允许的目录
		expectError bool   // 是否应返回错误
	}{
		// 允许目录内的路径 — 应通过
		{
			name:        "允许目录本身",
			path:        tmpDir,
			allowedDir:  tmpDir,
			expectError: false,
		},
		{
			name:        "允许目录内的文件",
			path:        safeFile,
			allowedDir:  tmpDir,
			expectError: false,
		},
		{
			name:        "允许目录内的子目录",
			path:        subDir,
			allowedDir:  tmpDir,
			expectError: false,
		},
		// 允许目录外的路径 — 应被拒绝
		{
			name:        "绝对路径在允许目录外",
			path:        absOutside,
			allowedDir:  tmpDir,
			expectError: true,
		},
		// 不存在的文件 — 检查父目录
		{
			name:        "允许目录内不存在的文件",
			path:        filepath.Join(subDir, "new_file.txt"),
			allowedDir:  tmpDir,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateWithinDir(tt.path, tt.allowedDir)
			if tt.expectError && err == nil {
				t.Errorf("ValidateWithinDir(%q, %q) 应返回错误, 但返回了 nil",
					tt.path, tt.allowedDir)
			}
			if !tt.expectError && err != nil {
				t.Errorf("ValidateWithinDir(%q, %q) 不应返回错误, 但返回了: %v",
					tt.path, tt.allowedDir, err)
			}
		})
	}
}

// TestValidateWithinDirSymlinkEscape 测试符号链接逃逸防护。
// 创建一个符号链接指向允许目录外的目标，验证是否被正确拦截。
func TestValidateWithinDirSymlinkEscape(t *testing.T) {
	// 创建两个独立的目录
	allowedDir := t.TempDir()
	outsideDir := t.TempDir()

	// 在外部目录创建目标文件
	targetFile := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(targetFile, []byte("secret"), 0o644); err != nil {
		t.Fatalf("创建目标文件失败: %v", err)
	}

	// 在允许目录内创建指向外部文件的符号链接
	symlinkPath := filepath.Join(allowedDir, "link_to_secret")
	if err := os.Symlink(targetFile, symlinkPath); err != nil {
		t.Fatalf("创建符号链接失败: %v", err)
	}

	// 符号链接指向允许目录外 — 应被拦截
	err := ValidateWithinDir(symlinkPath, allowedDir)
	if err == nil {
		t.Error("ValidateWithinDir 应拦截符号链接逃逸, 但返回了 nil")
	}
}

// TestPathSecurityError 测试 PathSecurityError 的 Error() 和 Unwrap() 方法。
func TestPathSecurityError(t *testing.T) {
	t.Run("带底层错误", func(t *testing.T) {
		inner := os.ErrNotExist
		err := &PathSecurityError{
			Path:   "/test/path",
			Reason: "测试原因",
			Err:    inner,
		}
		msg := err.Error()
		if msg == "" {
			t.Error("Error() 不应返回空字符串")
		}
		if err.Unwrap() != inner {
			t.Errorf("Unwrap() = %v, 期望 %v", err.Unwrap(), inner)
		}
	})

	t.Run("不带底层错误", func(t *testing.T) {
		err := &PathSecurityError{
			Path:   "/test/path",
			Reason: "测试原因",
		}
		msg := err.Error()
		if msg == "" {
			t.Error("Error() 不应返回空字符串")
		}
		if err.Unwrap() != nil {
			t.Errorf("Unwrap() 应返回 nil, 但返回了 %v", err.Unwrap())
		}
	})
}
