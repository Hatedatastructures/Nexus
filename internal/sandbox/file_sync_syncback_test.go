package sandbox

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileSyncManager_SyncBack(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create a tar archive to simulate remote download
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	content := "remote content"
	hdr := &tar.Header{
		Name:     "remote_file.txt",
		Size:     int64(len(content)),
		Mode:     0644,
		ModTime:  time.Now(),
		Typeflag: tar.TypeReg,
	}
	_ = tw.WriteHeader(hdr)
	_, _ = tw.Write([]byte(content))
	_ = tw.Close()

	m := NewFileSyncManager(dir, "/remote")

	downloadFn := func(_ string) (io.ReadCloser, error) {
		return io.NopCloser(&tarBuf), nil
	}

	err := m.SyncBack(downloadFn)
	if err != nil {
		t.Fatalf("SyncBack failed: %v", err)
	}

	// Verify the file was written
	data, err := os.ReadFile(filepath.Join(dir, "remote_file.txt"))
	if err != nil {
		t.Fatalf("file should exist: %v", err)
	}
	if string(data) != "remote content" {
		t.Errorf("expected 'remote content', got %q", string(data))
	}
}

func TestFileSyncManager_SyncBack_PathTraversal(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create a tar archive with path traversal
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	content := "evil"
	hdr := &tar.Header{
		Name:     "../../etc/passwd",
		Size:     int64(len(content)),
		Mode:     0644,
		ModTime:  time.Now(),
		Typeflag: tar.TypeReg,
	}
	_ = tw.WriteHeader(hdr)
	_, _ = tw.Write([]byte(content))
	_ = tw.Close()

	m := NewFileSyncManager(dir, "/remote")

	downloadFn := func(_ string) (io.ReadCloser, error) {
		return io.NopCloser(&tarBuf), nil
	}

	err := m.SyncBack(downloadFn)
	if err != nil {
		t.Fatalf("SyncBack should not error: %v", err)
	}

	// Verify no file was written outside the directory
	evilPath := filepath.Join(dir, "../../etc/passwd")
	absPath, _ := filepath.Abs(evilPath)
	if _, err := os.Stat(absPath); !os.IsNotExist(err) {
		t.Error("path traversal file should not have been written")
	}
}

func TestFileSyncManager_SyncBack_DownloadError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	m := NewFileSyncManager(dir, "/remote")

	downloadFn := func(_ string) (io.ReadCloser, error) {
		return nil, fmt.Errorf("network error")
	}

	err := m.SyncBack(downloadFn)
	if err == nil {
		t.Error("expected error from failed download")
	}
}

func TestFileSyncManager_SyncBack_SameContent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Create a local file with same content
	localContent := "same content"
	_ = os.WriteFile(filepath.Join(dir, "same.txt"), []byte(localContent), 0644)

	// Create tar with same content
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	hdr := &tar.Header{
		Name:     "same.txt",
		Size:     int64(len(localContent)),
		Mode:     0644,
		ModTime:  time.Now(),
		Typeflag: tar.TypeReg,
	}
	_ = tw.WriteHeader(hdr)
	_, _ = tw.Write([]byte(localContent))
	_ = tw.Close()

	m := NewFileSyncManager(dir, "/remote")

	downloadFn := func(_ string) (io.ReadCloser, error) {
		return io.NopCloser(&tarBuf), nil
	}

	err := m.SyncBack(downloadFn)
	if err != nil {
		t.Fatalf("SyncBack failed: %v", err)
	}

	// File should remain unchanged (same hash)
	info, _ := os.Stat(filepath.Join(dir, "same.txt"))
	if info == nil {
		t.Error("file should still exist")
	}
}

func TestFileHash(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "hashme.txt")
	_ = os.WriteFile(path, []byte("hash this content"), 0644)

	hash1, err := fileHash(path)
	if err != nil {
		t.Fatalf("fileHash failed: %v", err)
	}
	if hash1 == "" {
		t.Error("expected non-empty hash")
	}

	// Same content should produce same hash
	hash2, _ := fileHash(path)
	if hash1 != hash2 {
		t.Error("same file should produce same hash")
	}

	// Verify it's actually a SHA-256 hex string (64 chars)
	if len(hash1) != 64 {
		t.Errorf("expected 64-char SHA-256 hex, got %d chars", len(hash1))
	}
}

func TestFileHash_Nonexistent(t *testing.T) {
	t.Parallel()

	_, err := fileHash("/nonexistent/path/file.txt")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestFileHash_MatchesSHA256(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "verify.txt")
	content := []byte("test content for verification")
	_ = os.WriteFile(path, content, 0644)

	hash, _ := fileHash(path)

	// Manually compute expected hash
	expected := sha256.Sum256(content)
	expectedHex := fmt.Sprintf("%x", expected[:])

	if hash != expectedHex {
		t.Errorf("hash mismatch: got %s, want %s", hash, expectedHex)
	}
}
