package agent

import (
	"testing"
)

// ───────────────────────────── checkCredentialDenyRead 测试 ─────────────────────────────

func TestCheckCredentialDenyRead_NoHermesPaths(t *testing.T) {
	fs := NewFileSafetyChecker()
	// 没有设置 hermesHome，凭证文件不在 hermesHome 下 → 应放行
	reason := fs.checkCredentialDenyRead("/tmp/auth.json")
	if reason != "" {
		t.Errorf("credential file outside hermesHome should be allowed, got: %s", reason)
	}
}

func TestCheckCredentialDenyRead_CredentialUnderHome(t *testing.T) {
	dir := t.TempDir()
	fs := NewFileSafetyChecker()
	fs.SetHermesPaths(dir, dir)

	reason := fs.checkCredentialDenyRead(dir + "/auth.json")
	if reason == "" {
		t.Error("auth.json under hermesHome should be blocked")
	}
}

func TestCheckCredentialDenyRead_CredentialOutsideHome(t *testing.T) {
	dir := t.TempDir()
	otherDir := t.TempDir()
	fs := NewFileSafetyChecker()
	fs.SetHermesPaths(dir, dir)

	reason := fs.checkCredentialDenyRead(otherDir + "/auth.json")
	if reason != "" {
		t.Errorf("auth.json outside hermesHome should be allowed, got: %s", reason)
	}
}

func TestCheckCredentialDenyRead_McpTokensUnderHome(t *testing.T) {
	dir := t.TempDir()
	fs := NewFileSafetyChecker()
	fs.SetHermesPaths(dir, dir)

	reason := fs.checkCredentialDenyRead(dir + "/mcp-tokens/token.json")
	if reason == "" {
		t.Error("mcp-tokens under hermesHome should be blocked")
	}
}

func TestCheckCredentialDenyRead_RegularFile(t *testing.T) {
	dir := t.TempDir()
	fs := NewFileSafetyChecker()
	fs.SetHermesPaths(dir, dir)

	reason := fs.checkCredentialDenyRead(dir + "/regular.txt")
	if reason != "" {
		t.Errorf("regular file should be allowed, got: %s", reason)
	}
}

// ───────────────────────────── checkHermesDenyWrite 测试 ─────────────────────────────

func TestCheckHermesDenyWrite_ControlFile(t *testing.T) {
	dir := t.TempDir()
	fs := NewFileSafetyChecker()
	fs.SetHermesPaths(dir, dir)

	reason := fs.checkHermesDenyWrite(dir + "/config.yaml")
	if reason == "" {
		t.Error("config.yaml under hermesHome should be blocked for write")
	}
}

func TestCheckHermesDenyWrite_RegularFile(t *testing.T) {
	dir := t.TempDir()
	fs := NewFileSafetyChecker()
	fs.SetHermesPaths(dir, dir)

	reason := fs.checkHermesDenyWrite(dir + "/output.txt")
	if reason != "" {
		t.Errorf("regular file should be allowed, got: %s", reason)
	}
}

func TestCheckHermesDenyWrite_NoHermesHome(t *testing.T) {
	fs := NewFileSafetyChecker()

	reason := fs.checkHermesDenyWrite("/tmp/config.yaml")
	if reason != "" {
		t.Errorf("without hermesHome, control files should not be blocked, got: %s", reason)
	}
}

func TestCheckHermesDenyWrite_McpTokensDir(t *testing.T) {
	dir := t.TempDir()
	fs := NewFileSafetyChecker()
	fs.SetHermesPaths(dir, dir)

	reason := fs.checkHermesDenyWrite(dir + "/mcp-tokens/tok.json")
	if reason == "" {
		t.Error("mcp-tokens under hermesHome should be blocked for write")
	}
}

func TestCheckHermesDenyWrite_ControlFileOutsideHome(t *testing.T) {
	dir := t.TempDir()
	otherDir := t.TempDir()
	fs := NewFileSafetyChecker()
	fs.SetHermesPaths(dir, dir)

	reason := fs.checkHermesDenyWrite(otherDir + "/config.yaml")
	if reason != "" {
		t.Errorf("config.yaml outside hermesHome should be allowed, got: %s", reason)
	}
}

// ───────────────────────────── SetAllowedRoot 测试 ─────────────────────────────

func TestSetAllowedRoot_RealDir(t *testing.T) {
	fs := NewFileSafetyChecker()
	dir := t.TempDir()
	fs.SetAllowedRoot(dir)
	if fs.allowedRoot == "" {
		t.Error("allowedRoot should be set after SetAllowedRoot")
	}
}

func TestSetAllowedRoot_EmptyString(t *testing.T) {
	fs := NewFileSafetyChecker()
	fs.SetAllowedRoot("")
	if fs.allowedRoot != "" {
		t.Error("allowedRoot should remain empty with empty input")
	}
}

func TestSetAllowedRoot_NonExistentPath(t *testing.T) {
	fs := NewFileSafetyChecker()
	fs.SetAllowedRoot("/nonexistent/dir/xyz")
	if fs.allowedRoot == "" {
		t.Error("allowedRoot should fall back to abs path")
	}
}

// ───────────────────────────── CheckWrite + allowedRoot 测试 ─────────────────────────────

func TestCheckWrite_AllowedRoot_PathInside(t *testing.T) {
	dir := t.TempDir()
	fs := NewFileSafetyChecker()
	fs.SetAllowedRoot(dir)

	allowed, reason := fs.CheckWrite(dir+"/output.txt", 100)
	if !allowed {
		t.Errorf("path inside allowedRoot should be allowed, got: %s", reason)
	}
}

func TestCheckWrite_AllowedRoot_PathOutside(t *testing.T) {
	dir := t.TempDir()
	otherDir := t.TempDir()
	fs := NewFileSafetyChecker()
	fs.SetAllowedRoot(dir)

	allowed, reason := fs.CheckWrite(otherDir+"/output.txt", 100)
	if allowed {
		t.Error("path outside allowedRoot should be blocked")
	}
	if reason == "" {
		t.Error("reason should not be empty when blocked")
	}
}

func TestCheckWrite_AllowedRoot_TraversalAttempt(t *testing.T) {
	dir := t.TempDir()
	fs := NewFileSafetyChecker()
	fs.SetAllowedRoot(dir)

	allowed, reason := fs.CheckWrite(dir+"/../../../etc/passwd", 100)
	if allowed {
		t.Error("path traversal attempt should be blocked")
	}
	if reason == "" {
		t.Error("reason should not be empty for traversal attempt")
	}
}

func TestCheckWrite_AllowedRoot_SubdirectoryAllowed(t *testing.T) {
	dir := t.TempDir()
	fs := NewFileSafetyChecker()
	fs.SetAllowedRoot(dir)

	allowed, reason := fs.CheckWrite(dir+"/sub/dir/file.txt", 100)
	if !allowed {
		t.Errorf("subdirectory path should be allowed, got: %s", reason)
	}
}

// ───────────────────────────── CheckWrite + checkHermesDenyWrite 集成测试 ─────────────────────────────

func TestCheckWrite_HermesControlFile(t *testing.T) {
	dir := t.TempDir()
	fs := NewFileSafetyChecker()
	fs.SetHermesPaths(dir, dir)

	allowed, reason := fs.CheckWrite(dir+"/config.yaml", 100)
	if allowed {
		t.Error("hermes control file config.yaml should be blocked")
	}
	if reason == "" {
		t.Error("reason should not be empty for hermes control file")
	}
}

func TestCheckWrite_HermesRegularFile(t *testing.T) {
	dir := t.TempDir()
	fs := NewFileSafetyChecker()
	fs.SetHermesPaths(dir, dir)

	allowed, reason := fs.CheckWrite(dir+"/regular.txt", 100)
	if !allowed {
		t.Errorf("regular file under hermesHome should be allowed, got: %s", reason)
	}
}

func TestCheckWrite_HermesMcpTokens(t *testing.T) {
	dir := t.TempDir()
	fs := NewFileSafetyChecker()
	fs.SetHermesPaths(dir, dir)

	allowed, reason := fs.CheckWrite(dir+"/mcp-tokens/tok.json", 100)
	if allowed {
		t.Error("mcp-tokens under hermesHome should be blocked for write")
	}
	if reason == "" {
		t.Error("reason should not be empty for mcp-tokens write")
	}
}
