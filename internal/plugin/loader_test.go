package plugin

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// ─── Loader tests ───

func TestNewLoader(t *testing.T) {
	t.Parallel()

	l := NewLoader([]string{"/tmp/plugins"})
	if l == nil {
		t.Fatal("NewLoader() returned nil")
	}
}

func TestLoader_Discover_EmptyDirs(t *testing.T) {
	t.Parallel()

	l := NewLoader([]string{})
	manifests, err := l.Discover()
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(manifests) != 0 {
		t.Errorf("Discover() = %d manifests, want 0", len(manifests))
	}
}

func TestLoader_Discover_SinglePlugin(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "my-plugin")
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		t.Fatal(err)
	}

	content := "name: my-plugin\nversion: \"1.0.0\"\n"
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.yaml"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	l := NewLoader([]string{dir})
	manifests, err := l.Discover()
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(manifests) != 1 {
		t.Fatalf("Discover() = %d manifests, want 1", len(manifests))
	}
	if manifests[0].Name != "my-plugin" {
		t.Errorf("Name = %q, want %q", manifests[0].Name, "my-plugin")
	}
}

func TestLoader_Discover_YamlExtension(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "yml-plugin")
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		t.Fatal(err)
	}

	content := "name: yml-plugin\nversion: \"2.0.0\"\n"
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.yml"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	l := NewLoader([]string{dir})
	manifests, err := l.Discover()
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(manifests) != 1 {
		t.Fatalf("Discover() = %d manifests, want 1", len(manifests))
	}
}

func TestLoader_Discover_DeduplicatesByName(t *testing.T) {
	t.Parallel()

	// Two directories each containing a plugin with the same name
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	for _, dir := range []string{dir1, dir2} {
		pluginDir := filepath.Join(dir, "dup-plugin")
		_ = os.MkdirAll(pluginDir, 0755)
		content := "name: dup-plugin\nversion: \"1.0.0\"\n"
		_ = os.WriteFile(filepath.Join(pluginDir, "plugin.yaml"), []byte(content), 0644)
	}

	l := NewLoader([]string{dir1, dir2})
	manifests, _ := l.Discover()
	if len(manifests) != 1 {
		t.Errorf("Discover() = %d manifests, want 1 (deduplicated)", len(manifests))
	}
}

func TestLoader_Discover_FirstDirWins(t *testing.T) {
	t.Parallel()

	dir1 := t.TempDir()
	dir2 := t.TempDir()

	// dir1 has version 1.0.0
	p1 := filepath.Join(dir1, "conflict")
	_ = os.MkdirAll(p1, 0755)
	_ = os.WriteFile(filepath.Join(p1, "plugin.yaml"), []byte("name: conflict\nversion: \"1.0.0\"\n"), 0644)

	// dir2 has version 2.0.0
	p2 := filepath.Join(dir2, "conflict")
	_ = os.MkdirAll(p2, 0755)
	_ = os.WriteFile(filepath.Join(p2, "plugin.yaml"), []byte("name: conflict\nversion: \"2.0.0\"\n"), 0644)

	l := NewLoader([]string{dir1, dir2})
	manifests, _ := l.Discover()
	if len(manifests) != 1 {
		t.Fatalf("manifests = %d, want 1", len(manifests))
	}
	if manifests[0].Version != "1.0.0" {
		t.Errorf("Version = %q, want %q (first dir should win)", manifests[0].Version, "1.0.0")
	}
}

func TestLoader_Discover_SkipsExcludedDirs(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// .git directory with a plugin.yaml inside (should be skipped)
	gitDir := filepath.Join(dir, ".git")
	_ = os.MkdirAll(gitDir, 0755)
	_ = os.WriteFile(filepath.Join(gitDir, "plugin.yaml"), []byte("name: git-plugin\nversion: \"1.0.0\"\n"), 0644)

	// .github directory
	ghDir := filepath.Join(dir, ".github")
	_ = os.MkdirAll(ghDir, 0755)
	_ = os.WriteFile(filepath.Join(ghDir, "plugin.yaml"), []byte("name: gh-plugin\nversion: \"1.0.0\"\n"), 0644)

	// .cache directory
	cacheDir := filepath.Join(dir, ".cache")
	_ = os.MkdirAll(cacheDir, 0755)
	_ = os.WriteFile(filepath.Join(cacheDir, "plugin.yaml"), []byte("name: cache-plugin\nversion: \"1.0.0\"\n"), 0644)

	// Valid plugin
	validDir := filepath.Join(dir, "real-plugin")
	_ = os.MkdirAll(validDir, 0755)
	_ = os.WriteFile(filepath.Join(validDir, "plugin.yaml"), []byte("name: real-plugin\nversion: \"1.0.0\"\n"), 0644)

	l := NewLoader([]string{dir})
	manifests, _ := l.Discover()
	if len(manifests) != 1 {
		t.Fatalf("manifests = %d, want 1 (only real-plugin)", len(manifests))
	}
	if manifests[0].Name != "real-plugin" {
		t.Errorf("Name = %q, want %q", manifests[0].Name, "real-plugin")
	}
}

func TestLoader_Discover_SkipsInvalidManifests(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Invalid manifest (missing name)
	badDir := filepath.Join(dir, "bad-plugin")
	_ = os.MkdirAll(badDir, 0755)
	_ = os.WriteFile(filepath.Join(badDir, "plugin.yaml"), []byte("version: \"1.0.0\"\n"), 0644)

	// Valid manifest
	goodDir := filepath.Join(dir, "good-plugin")
	_ = os.MkdirAll(goodDir, 0755)
	_ = os.WriteFile(filepath.Join(goodDir, "plugin.yaml"), []byte("name: good-plugin\nversion: \"1.0.0\"\n"), 0644)

	l := NewLoader([]string{dir})
	manifests, _ := l.Discover()
	if len(manifests) != 1 {
		t.Fatalf("manifests = %d, want 1 (invalid skipped)", len(manifests))
	}
	if manifests[0].Name != "good-plugin" {
		t.Errorf("Name = %q, want %q", manifests[0].Name, "good-plugin")
	}
}

func TestLoader_Discover_NonExistentDir(t *testing.T) {
	t.Parallel()

	l := NewLoader([]string{"/nonexistent/path"})
	manifests, err := l.Discover()
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(manifests) != 0 {
		t.Errorf("Discover() = %d, want 0 for non-existent dir", len(manifests))
	}
}

func TestLoader_Discover_EmptyStringDir(t *testing.T) {
	t.Parallel()

	l := NewLoader([]string{""})
	manifests, err := l.Discover()
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(manifests) != 0 {
		t.Errorf("Discover() = %d, want 0 for empty dir string", len(manifests))
	}
}

func TestLoader_Discover_PlatformFilter(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Plugin restricted to "nonexistent_os"
	p1 := filepath.Join(dir, "os-specific")
	_ = os.MkdirAll(p1, 0755)
	_ = os.WriteFile(filepath.Join(p1, "plugin.yaml"),
		[]byte("name: os-specific\nversion: \"1.0.0\"\nplatforms:\n  - nonexistent_os\n"), 0644)

	// Plugin with no platform restriction (all platforms)
	p2 := filepath.Join(dir, "universal")
	_ = os.MkdirAll(p2, 0755)
	_ = os.WriteFile(filepath.Join(p2, "plugin.yaml"),
		[]byte("name: universal\nversion: \"1.0.0\"\n"), 0644)

	l := NewLoader([]string{dir})
	manifests, _ := l.Discover()
	if len(manifests) != 1 {
		t.Fatalf("manifests = %d, want 1 (os-specific filtered)", len(manifests))
	}
	if manifests[0].Name != "universal" {
		t.Errorf("Name = %q, want %q", manifests[0].Name, "universal")
	}
}

// ─── manifestMatchesPlatform tests ───

func TestManifestMatchesPlatform_Empty(t *testing.T) {
	t.Parallel()

	m := &Manifest{Platforms: nil}
	if !manifestMatchesPlatform(m) {
		t.Error("empty platforms should match all")
	}
}

func TestManifestMatchesPlatform_CurrentOS(t *testing.T) {
	t.Parallel()

	currentOS := runtime.GOOS
	m := &Manifest{Platforms: []string{currentOS}}
	if !manifestMatchesPlatform(m) {
		t.Errorf("should match current OS %q", currentOS)
	}
}

func TestManifestMatchesPlatform_WrongOS(t *testing.T) {
	t.Parallel()

	m := &Manifest{Platforms: []string{"nonexistent_os"}}
	if manifestMatchesPlatform(m) {
		t.Error("should not match nonexistent_os")
	}
}

func TestManifestMatchesPlatform_DarwinAlias(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "darwin" {
		t.Skip("only runs on darwin")
	}
	m := &Manifest{Platforms: []string{"macos"}}
	if !manifestMatchesPlatform(m) {
		t.Error("darwin should match macos alias")
	}
}

// ─── Loader Validate tests ───

func TestLoader_Validate_NilManifest(t *testing.T) {
	t.Parallel()

	l := NewLoader(nil)
	err := l.Validate(nil)
	if err == nil {
		t.Fatal("Validate(nil) expected error")
	}
}

func TestLoader_Validate_EmptyName(t *testing.T) {
	t.Parallel()

	l := NewLoader(nil)
	m := &Manifest{Version: "1.0.0"}
	err := l.Validate(m)
	if err == nil {
		t.Fatal("Validate() expected error for empty name")
	}
}

func TestLoader_Validate_InvalidNameChars(t *testing.T) {
	t.Parallel()

	l := NewLoader(nil)
	m := &Manifest{Name: "has space", Version: "1.0.0"}
	err := l.Validate(m)
	if err == nil {
		t.Fatal("Validate() expected error for invalid name chars")
	}
}

func TestLoader_Validate_ValidManifest(t *testing.T) {
	t.Parallel()

	l := NewLoader(nil)
	m := &Manifest{Name: "valid-plugin", Version: "1.0.0"}
	err := l.Validate(m)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestLoader_Validate_MissingEnvVar(t *testing.T) {
	t.Parallel()

	l := NewLoader(nil)
	m := &Manifest{
		Name:        "env-plugin",
		Version:     "1.0.0",
		RequiresEnv: []string{"NEXUS_NONEXISTENT_VAR_12345"},
	}
	err := l.Validate(m)
	if err == nil {
		t.Fatal("Validate() expected error for missing env var")
	}
}

func TestLoader_Validate_WithEnvVarSet(t *testing.T) {
	// Cannot use t.Parallel() with t.Setenv
	t.Setenv("NEXUS_TEST_PLUGIN_VAR", "1")

	l := NewLoader(nil)
	m := &Manifest{
		Name:        "env-plugin",
		Version:     "1.0.0",
		RequiresEnv: []string{"NEXUS_TEST_PLUGIN_VAR"},
	}
	err := l.Validate(m)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestLoader_Validate_MissingExternalDep(t *testing.T) {
	t.Parallel()

	l := NewLoader(nil)
	m := &Manifest{
		Name:         "dep-plugin",
		Version:      "1.0.0",
		ExternalDeps: []string{"nonexistent_binary_xyz123"},
	}
	err := l.Validate(m)
	if err == nil {
		t.Fatal("Validate() expected error for missing binary")
	}
}

// ─── isInPATH tests ───

func TestIsInPATH_NonExistent(t *testing.T) {
	t.Parallel()

	if isInPATH("definitely_not_a_real_binary_12345") {
		t.Error("isInPATH() = true for nonexistent binary")
	}
}
