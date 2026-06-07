package sandbox

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestNewFileSyncManager(t *testing.T) {
	t.Parallel()

	m := NewFileSyncManager("/local", "/remote")
	if m == nil {
		t.Fatal("expected non-nil manager")
	}
	if m.localRoot != "/local" {
		t.Errorf("expected localRoot /local, got %s", m.localRoot)
	}
	if m.remoteRoot != "/remote" {
		t.Errorf("expected remoteRoot /remote, got %s", m.remoteRoot)
	}
	if m.rateLimit != 5*time.Second {
		t.Errorf("expected rateLimit 5s, got %v", m.rateLimit)
	}
}

func TestFileSyncManager_Sync_EmptyDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	m := NewFileSyncManager(dir, "/remote")

	changed, err := m.Sync()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(changed) != 0 {
		t.Errorf("expected 0 changed files in empty dir, got %d", len(changed))
	}
}

func TestFileSyncManager_Sync_DetectsFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello"), 0644)

	m := NewFileSyncManager(dir, "/remote")
	changed, err := m.Sync()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(changed) != 1 {
		t.Fatalf("expected 1 changed file, got %d", len(changed))
	}
	if changed[0] != "test.txt" {
		t.Errorf("expected test.txt, got %s", changed[0])
	}
}

func TestFileSyncManager_Sync_NoChanges(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello"), 0644)

	m := NewFileSyncManager(dir, "/remote")
	// Set rate limit to 0 so sync always works
	m.rateLimit = 0

	// First sync
	_, _ = m.Sync()

	// Second sync with same files
	changed, err := m.Sync()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(changed) != 0 {
		t.Errorf("expected 0 changed files on second sync, got %d", len(changed))
	}
}

func TestFileSyncManager_Sync_DetectsModifiedFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	file := filepath.Join(dir, "data.txt")

	_ = os.WriteFile(file, []byte("version1"), 0644)

	m := NewFileSyncManager(dir, "/remote")
	m.rateLimit = 0
	_, _ = m.Sync()

	// Modify the file
	_ = os.WriteFile(file, []byte("version2"), 0644)

	changed, err := m.Sync()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(changed) != 1 {
		t.Errorf("expected 1 changed file after modification, got %d", len(changed))
	}
}

func TestFileSyncManager_Sync_RateLimited(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "test.txt"), []byte("hello"), 0644)

	m := NewFileSyncManager(dir, "/remote")
	m.rateLimit = 1 * time.Hour // Very long rate limit

	// First sync
	changed1, _ := m.Sync()
	if len(changed1) == 0 {
		t.Error("first sync should detect files")
	}

	// Add a new file - should be rate-limited
	_ = os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new"), 0644)
	changed2, _ := m.Sync()
	if len(changed2) != 0 {
		t.Error("second sync should be rate-limited, expected 0 changes")
	}
}

func TestFileSyncManager_Sync_SkipsHiddenDirs(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".git"), 0755)
	_ = os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("git config"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "visible.txt"), []byte("visible"), 0644)

	m := NewFileSyncManager(dir, "/remote")
	m.rateLimit = 0
	changed, err := m.Sync()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, f := range changed {
		if strings.HasPrefix(f, ".git") {
			t.Errorf("hidden dir files should be skipped, got %s", f)
		}
	}
}

func TestFileSyncManager_Sync_SkipsLargeFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Create a file larger than 10MB
	largeFile := filepath.Join(dir, "large.bin")
	f, _ := os.Create(largeFile)
	_ = f.Truncate(11 * 1024 * 1024) // 11MB
	_ = f.Close()

	// Also create a small file
	_ = os.WriteFile(filepath.Join(dir, "small.txt"), []byte("small"), 0644)

	m := NewFileSyncManager(dir, "/remote")
	m.rateLimit = 0
	changed, err := m.Sync()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, f := range changed {
		if f == "large.bin" {
			t.Error("large files should be skipped")
		}
	}
	// small file should be detected
	found := false
	for _, f := range changed {
		if f == "small.txt" {
			found = true
		}
	}
	if !found {
		t.Error("small.txt should be detected")
	}
}

func TestFileSyncManager_StateHash(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("aaa"), 0644)

	m := NewFileSyncManager(dir, "/remote")
	m.rateLimit = 0
	_, _ = m.Sync()

	hash1 := m.StateHash()
	if hash1 == "" {
		t.Error("expected non-empty state hash")
	}

	// Same state should produce same hash
	hash2 := m.StateHash()
	if hash1 != hash2 {
		t.Error("same state should produce same hash")
	}
}

func TestFileSyncManager_StateHash_Changes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("aaa"), 0644)

	m := NewFileSyncManager(dir, "/remote")
	m.rateLimit = 0
	_, _ = m.Sync()

	hash1 := m.StateHash()

	// Add a file
	_ = os.WriteFile(filepath.Join(dir, "b.txt"), []byte("bbb"), 0644)
	_, _ = m.Sync()

	hash2 := m.StateHash()
	if hash1 == hash2 {
		t.Error("state hash should change after adding a file")
	}
}

func TestFileSyncManager_CreateTarArchive(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "file1.txt"), []byte("content1"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "file2.txt"), []byte("content2"), 0644)

	m := NewFileSyncManager(dir, "/remote")

	var buf bytes.Buffer
	err := m.CreateTarArchive([]string{"file1.txt", "file2.txt"}, &buf)
	if err != nil {
		t.Fatalf("CreateTarArchive failed: %v", err)
	}

	// Verify tar contents
	tr := tar.NewReader(&buf)
	var files []string
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar read failed: %v", err)
		}
		files = append(files, header.Name)
	}

	sort.Strings(files)
	if len(files) != 2 {
		t.Fatalf("expected 2 files in tar, got %d", len(files))
	}
	if files[0] != "file1.txt" || files[1] != "file2.txt" {
		t.Errorf("unexpected files in tar: %v", files)
	}
}

func TestFileSyncManager_CreateTarArchive_NonexistentFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	m := NewFileSyncManager(dir, "/remote")

	var buf bytes.Buffer
	err := m.CreateTarArchive([]string{"nonexistent.txt"}, &buf)
	if err != nil {
		t.Fatalf("CreateTarArchive should not error for nonexistent files: %v", err)
	}
}
