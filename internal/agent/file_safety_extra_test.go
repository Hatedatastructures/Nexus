package agent

import (
	"os"
	"testing"
)

// ───────────────────────────── SetHermesPaths 测试 ─────────────────────────────

func TestSetHermesPaths(t *testing.T) {
	fs := NewFileSafetyChecker()
	dir := t.TempDir()

	fs.SetHermesPaths(dir, dir)
	if fs.hermesHome == "" {
		t.Error("hermesHome should not be empty after SetHermesPaths")
	}
	if fs.hermesRoot == "" {
		t.Error("hermesRoot should not be empty after SetHermesPaths")
	}
}

func TestSetHermesPaths_EmptyStrings(t *testing.T) {
	fs := NewFileSafetyChecker()
	fs.SetHermesPaths("", "")
	if fs.hermesHome != "" {
		t.Error("hermesHome should be empty with empty input")
	}
	if fs.hermesRoot != "" {
		t.Error("hermesRoot should be empty with empty input")
	}
}

func TestSetHermesPaths_NonExistentPath(t *testing.T) {
	fs := NewFileSafetyChecker()
	fs.SetHermesPaths("/nonexistent/path/abc", "/nonexistent/path/def")
	// 应使用 abs 路径 (EvalSymlinks 失败时回退)
	if fs.hermesHome == "" {
		t.Error("hermesHome should fall back to abs path")
	}
	if fs.hermesRoot == "" {
		t.Error("hermesRoot should fall back to abs path")
	}
}

// ───────────────────────────── CheckRead 测试 ─────────────────────────────

func TestCheckRead_SafePath(t *testing.T) {
	fs := NewFileSafetyChecker()
	dir := t.TempDir()
	safeFile := dir + "/readme.md"

	allowed, reason := fs.CheckRead(safeFile)
	if !allowed {
		t.Errorf("safe file should be allowed, got: %s", reason)
	}
}

func TestCheckRead_ProtectedPath(t *testing.T) {
	fs := NewFileSafetyChecker()
	dir := t.TempDir()

	allowed, reason := fs.CheckRead(dir + "/.ssh/id_rsa")
	if allowed {
		t.Error(".ssh path should be blocked for read")
	}
	if reason == "" {
		t.Error("reason should not be empty when blocked")
	}
}

func TestCheckRead_CredentialFile(t *testing.T) {
	dir := t.TempDir()
	fs := NewFileSafetyChecker()
	fs.SetHermesPaths(dir, dir)

	// 在 hermesHome 下创建 auth.json 路径
	authPath := dir + "/auth.json"

	allowed, reason := fs.CheckRead(authPath)
	if allowed {
		t.Error("auth.json under hermesHome should be blocked for read")
	}
	if reason == "" {
		t.Error("reason should not be empty for credential file")
	}
}

func TestCheckRead_McpTokens(t *testing.T) {
	dir := t.TempDir()
	fs := NewFileSafetyChecker()
	fs.SetHermesPaths(dir, dir)

	// 在 hermesHome 下创建 mcp-tokens 路径
	tokenPath := dir + "/mcp-tokens/token.json"

	allowed, reason := fs.CheckRead(tokenPath)
	if allowed {
		t.Error("mcp-tokens directory should be blocked for read")
	}
	if reason == "" {
		t.Error("reason should not be empty for mcp-tokens")
	}
}

func TestCheckRead_NonCredentialInHermesHome(t *testing.T) {
	dir := t.TempDir()
	fs := NewFileSafetyChecker()
	fs.SetHermesPaths(dir, dir)

	// 普通文件不受凭证拒绝限制
	safePath := dir + "/regular_file.txt"
	allowed, _ := fs.CheckRead(safePath)
	if !allowed {
		t.Error("regular file under hermesHome should be allowed for read")
	}
}

// ───────────────────────────── resolvePath 测试 ─────────────────────────────

func TestResolvePath_ExistingFile(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(dir + "/test.txt")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	resolved, err := resolvePath(dir + "/test.txt")
	if err != nil {
		t.Fatalf("resolvePath error: %v", err)
	}
	if resolved == "" {
		t.Error("resolved path should not be empty")
	}
}

func TestResolvePath_NonExistentFile(t *testing.T) {
	resolved, err := resolvePath("/nonexistent/path/file.txt")
	if err != nil {
		t.Fatalf("resolvePath should not error for nonexistent file, got: %v", err)
	}
	// 不存在的文件应返回 abs 路径
	if resolved == "" {
		t.Error("resolved should return abs path for nonexistent file")
	}
}

func TestResolvePath_RelativePath(t *testing.T) {
	resolved, err := resolvePath("some/relative/path.txt")
	if err != nil {
		t.Fatalf("resolvePath error: %v", err)
	}
	// 相对路径应被转换为绝对路径
	if resolved == "some/relative/path.txt" {
		t.Error("relative path should be converted to absolute")
	}
}

// ───────────────────────────── absOrResolvedPath 测试 ─────────────────────────────

func TestAbsOrResolvedPath_ExistingFile(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(dir + "/test.txt")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	result := absOrResolvedPath(dir + "/test.txt")
	if result == "" {
		t.Error("result should not be empty for existing file")
	}
}

func TestAbsOrResolvedPath_NonExistentFile(t *testing.T) {
	result := absOrResolvedPath("/nonexistent/path/file.txt")
	if result == "" {
		t.Error("result should fall back to abs path for nonexistent file")
	}
}

func TestAbsOrResolvedPath_RelativePath(t *testing.T) {
	result := absOrResolvedPath("some/relative/path.txt")
	if result == "some/relative/path.txt" {
		t.Error("relative path should be converted to absolute")
	}
}
