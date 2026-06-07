package commands

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestBackupCommandName(t *testing.T) {
	t.Parallel()
	c := &BackupCommand{}
	if c.Name() != "backup" {
		t.Errorf("BackupCommand.Name() = %q, want %q", c.Name(), "backup")
	}
}

func TestBackupCommandSynopsis(t *testing.T) {
	t.Parallel()
	c := &BackupCommand{}
	if c.Synopsis() == "" {
		t.Error("BackupCommand.Synopsis() returned empty string")
	}
}

func TestBackupCommandRegistered(t *testing.T) {
	t.Parallel()
	cmd, ok := GetCommand("backup")
	if !ok {
		t.Fatal("backup command not registered")
	}
	if _, isBackup := cmd.(*BackupCommand); !isBackup {
		t.Errorf("expected *BackupCommand, got %T", cmd)
	}
}

func TestCreateTarGz(t *testing.T) {
	tmpDir := t.TempDir()

	// Create source directory with files
	srcDir := filepath.Join(tmpDir, "source")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("failed to create source dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "test.txt"), []byte("hello world"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(srcDir, "subdir"), 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "subdir", "nested.txt"), []byte("nested content"), 0644); err != nil {
		t.Fatalf("failed to write nested file: %v", err)
	}

	// Create tar.gz
	targetFile := filepath.Join(tmpDir, "backup.tar.gz")
	if err := createTarGz(srcDir, targetFile); err != nil {
		t.Fatalf("createTarGz() error: %v", err)
	}

	// Verify the file exists and has content
	info, err := os.Stat(targetFile)
	if err != nil {
		t.Fatalf("stat backup file: %v", err)
	}
	if info.Size() == 0 {
		t.Error("backup file is empty")
	}

	// Verify we can read the archive back
	f, err := os.Open(targetFile)
	if err != nil {
		t.Fatalf("open backup file: %v", err)
	}
	defer func() { _ = f.Close() }()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer func() { _ = gzr.Close() }()

	tr := tar.NewReader(gzr)
	foundFiles := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		// Normalize path separators
		name := filepath.ToSlash(hdr.Name)
		foundFiles[name] = true
	}

	if !foundFiles["test.txt"] {
		t.Error("archive missing test.txt")
	}
	// Check for nested file with normalized path
	nestedFound := foundFiles["subdir/nested.txt"] || foundFiles["subdir\\nested.txt"]
	if !nestedFound {
		t.Errorf("archive missing subdir/nested.txt; found files: %v", foundFiles)
	}
}

func TestCreateTarGzEmptyDir(t *testing.T) {
	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "empty_source")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("failed to create source dir: %v", err)
	}

	targetFile := filepath.Join(tmpDir, "empty_backup.tar.gz")
	if err := createTarGz(srcDir, targetFile); err != nil {
		t.Fatalf("createTarGz() on empty dir error: %v", err)
	}

	info, err := os.Stat(targetFile)
	if err != nil {
		t.Fatalf("stat backup file: %v", err)
	}
	if info.Size() == 0 {
		t.Error("backup file for empty dir should not be zero bytes (has tar header)")
	}
}

func TestCreateTarGzNonexistentSource(t *testing.T) {
	tmpDir := t.TempDir()
	targetFile := filepath.Join(tmpDir, "fail.tar.gz")
	err := createTarGz(filepath.Join(tmpDir, "no_such_dir"), targetFile)
	if err == nil {
		t.Error("expected error for nonexistent source directory")
	}
}
