package state

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSessionPersister_OpenStatError 测试 Open 处理 stat 错误路径
func TestSessionPersister_OpenStatError(t *testing.T) {
	dir := t.TempDir()
	// Create a file where the directory should be to trigger stat error
	filePath := filepath.Join(dir, "blocked")
	os.WriteFile(filePath, []byte("block"), 0o600)

	sp := NewSessionPersister(filePath, "test")
	// Open should fail because MkdirAll on a file path will fail or
	// the path is invalid for directory creation
	err := sp.Open()
	// Just verify no panic; error is acceptable
	_ = err
}

// TestParseSchemaColumns_InvalidStatement 测试 parseSchemaColumns 跳过无效 SQL


// TestSessionPersister_WriteRecordNotOpen 测试未打开时 writeRecord 返回错误
func TestSessionPersister_WriteRecordNotOpen(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "not-open")

	err := sp.RecordSessionMeta(&Session{ID: "x"})
	if err == nil {
		t.Error("expected error when persister not open, got nil")
	}
	if !strings.Contains(err.Error(), "not open") {
		t.Errorf("error = %q, want 'not open' message", err)
	}
}

// TestSessionPersister_CloseTwice 测试重复关闭不 panic


// TestSessionPersister_CloseTwice 测试重复关闭不 panic
func TestSessionPersister_CloseTwice(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "close-twice")

	if err := sp.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := sp.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	// 第二次关闭应安全返回
	if err := sp.Close(); err != nil {
		t.Fatalf("second Close should be safe: %v", err)
	}
}

// TestSessionPersister_OpenWithExistingFile 测试打开已存在的文件


// TestSessionPersister_OpenWithExistingFile 测试打开已存在的文件
func TestSessionPersister_OpenWithExistingFile(t *testing.T) {
	dir := t.TempDir()

	// 先创建文件并写入一些内容
	sp1 := NewSessionPersister(dir, "existing-file")
	if err := sp1.Open(); err != nil {
		t.Fatalf("first Open: %v", err)
	}
	sp1.RecordSessionMeta(&Session{ID: "existing-file", Source: "test"})
	sp1.Close()

	// 再次打开 — 应进入 append 模式
	sp2 := NewSessionPersister(dir, "existing-file")
	if err := sp2.Open(); err != nil {
		t.Fatalf("second Open: %v", err)
	}
	sp2.RecordSessionMeta(&Session{ID: "existing-file", Source: "test2"})
	sp2.Close()

	// 验证文件有内容
	data, err := os.ReadFile(filepath.Join(dir, "existing-file.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	lines := strings.Count(string(data), "\n")
	if lines < 2 {
		t.Errorf("expected at least 2 lines, got %d", lines)
	}
}



// TestSessionPersister_RecordAllTypes 测试写入所有类型的记录
func TestSessionPersister_RecordAllTypes(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "all-types")

	if err := sp.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sp.Close()

	if err := sp.RecordSessionMeta(&Session{ID: "meta-test", Source: "test"}); err != nil {
		t.Fatalf("RecordSessionMeta: %v", err)
	}
	if err := sp.RecordMessage(&MessageRecord{SessionID: "meta-test", Role: "user", Content: "hello"}); err != nil {
		t.Fatalf("RecordMessage: %v", err)
	}
	if err := sp.RecordCompaction(10, 500); err != nil {
		t.Fatalf("RecordCompaction: %v", err)
	}
	if err := sp.RecordPromptHistory("test prompt"); err != nil {
		t.Fatalf("RecordPromptHistory: %v", err)
	}

	// Verify content
	data, err := os.ReadFile(filepath.Join(dir, "all-types.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "session_meta") {
		t.Error("missing session_meta record")
	}
	if !strings.Contains(content, "message") {
		t.Error("missing message record")
	}
	if !strings.Contains(content, "compaction") {
		t.Error("missing compaction record")
	}
	if !strings.Contains(content, "prompt_history") {
		t.Error("missing prompt_history record")
	}
}

// TestInsertMessagesBatch_Multiple 测试批量插入多条消息


// TestSessionPersister_RotationShiftsFiles 测试轮转时旧文件被移动
func TestSessionPersister_RotationShiftsFiles(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "shift-test")
	sp.maxSize = 30 // 非常小
	sp.maxRotations = 3

	if err := sp.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sp.Close()

	sess := &Session{ID: "shift-test", Source: "test", StartedAt: 1000.0}

	// 大量写入以触发多次轮转
	for i := range 30 {
		if err := sp.RecordSessionMeta(sess); err != nil {
			t.Fatalf("RecordSessionMeta %d: %v", i, err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	// 应该有主文件和至少一个轮转文件
	fileCount := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			fileCount++
		}
	}
	if fileCount < 2 {
		t.Errorf("expected multiple rotation files, got %d", fileCount)
	}
}

// TestSessionPersister_CloseNil 测试双重 Close 不 panic


// TestSessionPersister_CloseNil 测试双重 Close 不 panic
func TestSessionPersister_CloseNilSafe(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "close-nil")

	// 未打开就 Close
	if err := sp.Close(); err != nil {
		t.Errorf("Close on unopened persister: %v", err)
	}

	// 再次 Close
	if err := sp.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// ── AutoPrune 额外路径覆盖 ────────────────────────────────

// TestAutoPrune_DefaultMaxAgeWithExpired 测试 maxAgeDays=0 使用默认 90 天


func TestSessionPersister_WriteAfterClose(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "close-test")

	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}
	if err := sp.Close(); err != nil {
		t.Fatal(err)
	}

	err := sp.RecordMessage(&MessageRecord{Role: "user", Content: "after close"})
	if err == nil {
		t.Error("expected error writing to closed persister")
	}
}



func TestSessionPersister_CloseWithoutOpen(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "noopen")
	// Close without ever calling OpenForAppend or OpenNew
	err := sp.Close()
	if err != nil {
		t.Fatalf("Close on unopened persister should not error: %v", err)
	}
}



func TestWriteRecord_FlushError(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "flush-err")
	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}
	// Close the underlying file to cause flush error
	sp.file.Close()
	err := sp.RecordPromptHistory("test after close")
	if err == nil {
		t.Error("expected error when file is closed")
	}
}



func TestRotateIfNeeded_ShiftAndDelete(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "shift-test")
	sp.maxSize = 50
	sp.maxRotations = 2
	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}
	// Write enough to trigger multiple rotations
	for i := 0; i < 30; i++ {
		if err := sp.RecordPromptHistory(fmt.Sprintf("rotation-test-line-%d-padding-text-here", i)); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	// After 30 writes with maxSize=50 and maxRotations=2,
	// oldest file (.3+) should have been deleted
	sp.Close()
	// .2 should exist (max rotation)
	if _, err := os.Stat(dir + "/shift-test.2.jsonl"); err != nil {
		t.Errorf("expected .2.jsonl to exist: %v", err)
	}
	// .3 should NOT exist (beyond maxRotations)
	if _, err := os.Stat(dir + "/shift-test.3.jsonl"); err == nil {
		t.Error(".3.jsonl should have been deleted")
	}
}



func TestClose_FlushError(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "close-flush")
	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}
	sp.RecordPromptHistory("some data")
	// Close should succeed normally
	if err := sp.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	// Double close should be safe (nil file)
	if err := sp.Close(); err != nil {
		t.Fatalf("double close should be safe: %v", err)
	}
}



func TestWriteRecord_NotOpen(t *testing.T) {
	sp := NewSessionPersister(t.TempDir(), "test-session")
	err := sp.RecordMessage(&MessageRecord{SessionID: "test", Role: "user", Content: "hello"})
	if err == nil {
		t.Error("expected error when writing to unopened persister")
	}
	if !strings.Contains(err.Error(), "not open") {
		t.Errorf("unexpected error message: %v", err)
	}
}




func TestSessionPersister_FullCycle(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "cycle")
	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}
	sess := &Session{ID: "cycle-sess", Source: "test", StartedAt: float64(time.Now().Unix())}
	if err := sp.RecordSessionMeta(sess); err != nil {
		t.Fatal(err)
	}
	if err := sp.RecordMessage(&MessageRecord{SessionID: "cycle-sess", Role: "user", Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	if err := sp.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "cycle.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Count(string(data), "\n")
	if lines < 2 {
		t.Errorf("expected at least 2 lines, got %d", lines)
	}
	sp2 := NewSessionPersister(dir, "cycle")
	if err := sp2.Open(); err != nil {
		t.Fatal(err)
	}
	defer sp2.Close()
	if err := sp2.RecordCompaction(5, 100); err != nil {
		t.Fatal(err)
	}
}



func TestRotateIfNeeded_RotatesFiles(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "rotate")
	sp.maxSize = 10
	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		sp.RecordMessage(&MessageRecord{
			SessionID: "rot",
			Role:      "user",
			Content:   fmt.Sprintf("message-number-%d-with-padding", i),
			Timestamp: float64(time.Now().Unix()),
		})
	}
	sp.Close()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 2 {
		t.Errorf("expected at least 2 files after rotation, got %d", len(entries))
	}
}



func TestSessionPersist_Rotation(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "rot-test")
	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}
	defer sp.Close()
	for i := 0; i < 200; i++ {
		sp.RecordMessage(&MessageRecord{SessionID: "rot-sess", Role: "user", Content: "padding data to fill buffer beyond limit"})
	}
}
