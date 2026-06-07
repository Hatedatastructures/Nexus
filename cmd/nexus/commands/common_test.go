package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	RegisterAllCommands()
	os.Exit(m.Run())
}

// ───────────────────────────── Style variables ─────────────────────────────

func TestStylesAreDefined(t *testing.T) {
	t.Parallel()

	styles := []struct {
		name  string
		value string
	}{
		{"TitleStyle", TitleStyle.Render("x")},
		{"DimStyle", DimStyle.Render("x")},
		{"UserStyle", UserStyle.Render("x")},
		{"ErrorStyle", ErrorStyle.Render("x")},
		{"GreenBold", GreenBold.Render("x")},
		{"ReasoningLbl", ReasoningLbl.Render("x")},
	}

	for _, s := range styles {
		if s.value == "" {
			t.Errorf("style %s rendered empty string", s.name)
		}
	}
}

// ───────────────────────────── Command Registry ─────────────────────────────

func TestRegisterAndGetCommand(t *testing.T) {
	t.Parallel()

	// Verify some well-known commands are registered via init()
	names := ListCommands()
	if len(names) == 0 {
		t.Fatal("expected commands to be registered via init(), got empty registry")
	}

	// Check a few specific ones exist
	for _, name := range []string{"version", "doctor", "status", "chat", "tool", "backup", "session", "cron", "gateway"} {
		cmd, ok := GetCommand(name)
		if !ok {
			t.Errorf("expected command %q to be registered", name)
			continue
		}
		if cmd.Name() != name {
			t.Errorf("expected command name %q, got %q", name, cmd.Name())
		}
	}
}

func TestGetCommandNotFound(t *testing.T) {
	t.Parallel()

	_, ok := GetCommand("nonexistent_command_xyz")
	if ok {
		t.Error("expected GetCommand to return false for unregistered command")
	}
}

func TestListCommandsContainsKnown(t *testing.T) {
	t.Parallel()

	names := ListCommands()
	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
	}

	for _, expected := range []string{"version", "status", "doctor"} {
		if !nameSet[expected] {
			t.Errorf("expected %q in ListCommands result", expected)
		}
	}
}

// ───────────────────────────── MaskAPIKey ─────────────────────────────

func TestMaskAPIKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		key      string
		expected string
	}{
		{"empty key", "", "(未设置)"},
		{"short key", "abc", "****"},
		{"exactly 8 chars", "12345678", "****"},
		{"9 chars", "123456789", "1234...6789"},
		{"long key", "sk-ant-api03-longkeyvaluehere", "sk-a...here"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := MaskAPIKey(tt.key)
			if result != tt.expected {
				t.Errorf("MaskAPIKey(%q) = %q, want %q", tt.key, result, tt.expected)
			}
		})
	}
}

// ───────────────────────────── Path Helpers ─────────────────────────────

func TestGetNexusHome(t *testing.T) {
	t.Parallel()

	home := GetNexusHome()
	if home == "" {
		t.Fatal("GetNexusHome returned empty string")
	}
	if !strings.HasSuffix(home, "/.nexus") && home != ".nexus" {
		t.Errorf("GetNexusHome() = %q, expected to end with /.nexus or be .nexus", home)
	}
}

func TestGetDBPath(t *testing.T) {
	t.Parallel()

	db := GetDBPath()
	if !strings.HasSuffix(db, "/nexus.db") {
		t.Errorf("GetDBPath() = %q, expected to end with /nexus.db", db)
	}
}

func TestGetConfigPath(t *testing.T) {
	t.Parallel()

	cfg := GetConfigPath()
	if !strings.HasSuffix(cfg, "/config.yaml") {
		t.Errorf("GetConfigPath() = %q, expected to end with /config.yaml", cfg)
	}
}

func TestGetLogsDir(t *testing.T) {
	t.Parallel()

	logs := GetLogsDir()
	if !strings.HasSuffix(logs, "/logs") {
		t.Errorf("GetLogsDir() = %q, expected to end with /logs", logs)
	}
}

func TestPathRelationships(t *testing.T) {
	t.Parallel()

	home := GetNexusHome()
	if GetDBPath() != home+"/nexus.db" {
		t.Errorf("GetDBPath() should equal GetNexusHome()+/nexus.db")
	}
	if GetConfigPath() != home+"/config.yaml" {
		t.Errorf("GetConfigPath() should equal GetNexusHome()+/config.yaml")
	}
	if GetLogsDir() != home+"/logs" {
		t.Errorf("GetLogsDir() should equal GetNexusHome()+/logs")
	}
}

// ───────────────────────────── FileExists ─────────────────────────────

func TestFileExists(t *testing.T) {
	t.Parallel()

	// Create a temp file
	tmpDir := t.TempDir()
	existingFile := filepath.Join(tmpDir, "exists.txt")
	if err := os.WriteFile(existingFile, []byte("hello"), 0644); err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}

	if !FileExists(existingFile) {
		t.Errorf("FileExists(%q) = false, want true", existingFile)
	}

	missingFile := filepath.Join(tmpDir, "does_not_exist.txt")
	if FileExists(missingFile) {
		t.Errorf("FileExists(%q) = true, want false", missingFile)
	}
}

func TestFileExistsDirectory(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	// Directories should also return true with os.Stat
	if !FileExists(tmpDir) {
		t.Errorf("FileExists(%q) for directory = false, want true", tmpDir)
	}
}

// ───────────────────────────── validateSkillName ─────────────────────────────

func TestValidateSkillName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"empty", "", true},
		{"dot", ".", true},
		{"double dot", "..", true},
		{"contains double dot", "foo..bar", true},
		{"forward slash", "foo/bar", true},
		{"backslash", "foo\\bar", true},
		{"null byte", "foo\x00bar", true},
		{"valid simple name", "my-skill", false},
		{"valid with underscore", "my_skill", false},
		{"valid alphanumeric", "skill123", false},
		{"valid with dots inside", "skill.v2", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateSkillName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSkillName(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestValidateSkillNameErrorMessages(t *testing.T) {
	t.Parallel()

	err := validateSkillName("")
	if err == nil {
		t.Fatal("expected error for empty name")
	}
	if !strings.Contains(err.Error(), "空") {
		t.Errorf("expected empty-name error message to mention '空', got %q", err.Error())
	}

	err = validateSkillName("..")
	if err == nil {
		t.Fatal("expected error for '..'")
	}
	if !strings.Contains(err.Error(), "..") {
		t.Errorf("expected error message to contain '..', got %q", err.Error())
	}

	err = validateSkillName("foo/bar")
	if err == nil {
		t.Fatal("expected error for path separator")
	}
	if !strings.Contains(err.Error(), "路径分隔符") {
		t.Errorf("expected error about path separator, got %q", err.Error())
	}
}

// ───────────────────────────── Command Interface ─────────────────────────────

func TestCommandInterfaceTypes(t *testing.T) {
	t.Parallel()

	// Verify all command types implement Command interface
	var _ Command = (*VersionCommand)(nil)
	var _ Command = (*DoctorCommand)(nil)
	var _ Command = (*LogsCommand)(nil)
	var _ Command = (*CronCommand)(nil)
	var _ Command = (*SessionCommand)(nil)
	var _ Command = (*GatewayCommand)(nil)
	var _ Command = (*BackupCommand)(nil)
	var _ Command = (*MemoryCommand)(nil)
	var _ Command = (*ExportCommand)(nil)
	var _ Command = (*ProviderCommand)(nil)
	var _ Command = (*SetupCommand)(nil)
	var _ Command = (*ConfigCommand)(nil)
	var _ Command = (*ImportCommand)(nil)
	var _ Command = (*ModelCommand)(nil)
	var _ Command = (*ChatCommand)(nil)
	var _ Command = (*MCPServeCommand)(nil)
	var _ Command = (*RLCommand)(nil)
	var _ Command = (*SkillCommand)(nil)
	var _ Command = (*StatusCommand)(nil)
	var _ Command = (*ToolCommand)(nil)
}

func TestCommandSynopsis(t *testing.T) {
	t.Parallel()

	// All commands should have non-empty synopsis
	names := ListCommands()
	for _, name := range names {
		cmd, ok := GetCommand(name)
		if !ok {
			continue
		}
		syn := cmd.Synopsis()
		if syn == "" {
			t.Errorf("command %q has empty Synopsis()", name)
		}
	}
}

// ───────────────────────────── PrintTitle ─────────────────────────────

func TestPrintTitle(t *testing.T) {
	PrintTitle("Test Title")
}

func TestPrintSection(t *testing.T) {
	PrintSection("TestSection")
}

func TestPrintSuccess(t *testing.T) {
	PrintSuccess("test success message")
}

func TestPrintWarning(t *testing.T) {
	PrintWarning("test warning: %s", "detail")
}
