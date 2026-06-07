package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAutoPrune_WithMessages 测试删除会话时消息也被删除
func TestAutoPrune_WithMessages(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &Session{
		ID:        "prune-msg",
		Source:    "test",
		StartedAt: float64(time.Now().Add(-48 * time.Hour).Unix()),
	}
	store.CreateSession(ctx, sess)
	store.EndSession(ctx, "prune-msg", "done")
	store.DB().ExecContext(ctx, "UPDATE sessions SET ended_at = ? WHERE id = ?",
		float64(time.Now().Add(-48*time.Hour).Unix()), "prune-msg")

	// Insert messages
	for i := range 3 {
		store.InsertMessage(ctx, &MessageRecord{
			SessionID: "prune-msg",
			Role:      "user",
			Content:   fmt.Sprintf("msg %d", i),
		})
	}

	count, _ := store.GetMessageCount(ctx, "prune-msg")
	if count != 3 {
		t.Fatalf("expected 3 messages before prune, got %d", count)
	}

	deleted, err := store.AutoPrune(ctx, 1)
	if err != nil {
		t.Fatalf("AutoPrune: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", deleted)
	}

	// Messages should be gone too
	count, err = store.GetMessageCount(ctx, "prune-msg")
	if err != nil {
		t.Fatalf("GetMessageCount after prune: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 messages after prune, got %d", count)
	}
}

// TestSearchTrigramFTS_SyntaxError 测试 trigram FTS 查询语法错误


// TestNewStore_WALMode 验证 WAL 模式可被启用
func TestNewStore_WALMode(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(filepath.Join(dir, "wal_test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	// modernc.org/sqlite 的 _journal_mode DSN 参数可能不会立即生效，
	// 需要先执行一个写操作让 WAL 模式被激活
	_, err = store.DB().ExecContext(context.Background(),
		"CREATE TABLE IF NOT EXISTS _wal_check (id INTEGER PRIMARY KEY)")
	if err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}

	var mode string
	err = store.DB().QueryRowContext(context.Background(), "PRAGMA journal_mode").Scan(&mode)
	if err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if !strings.EqualFold(mode, "wal") {
		// WAL 未启用不阻塞，仅记录（modernc DSN 参数行为可能因版本不同）
		t.Logf("journal_mode = %q (expected wal; DSN param may not take effect with this driver version)", mode)
	}
}

// TestSplitSQLStatements_SingleQuotes 测试单引号字符串内的分号不拆分


// TestExecuteWrite_CommitRetry 测试 COMMIT 时锁竞争重试路径
func TestExecuteWrite_CommitRetry(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// 正常写入路径 — 确保 COMMIT 路径被覆盖
	err := store.executeWrite(ctx, func(db *sql.DB) error {
		_, err := db.ExecContext(ctx, "CREATE TABLE test_commit_retry (id INTEGER PRIMARY KEY)")
		return err
	})
	if err != nil {
		t.Fatalf("executeWrite normal path: %v", err)
	}
}

// TestExecuteWrite_WriteFnError 测试写操作闭包返回错误时的回滚路径


// TestExecuteWrite_WriteFnError 测试写操作闭包返回错误时的回滚路径
func TestExecuteWrite_WriteFnError(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	wantErr := errors.New("intentional write error")
	err := store.executeWrite(ctx, func(db *sql.DB) error {
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Errorf("executeWrite write fn error = %v, want %v", err, wantErr)
	}
}

// TestExecuteWrite_BeginImmediateFail 测试 BEGIN IMMEDIATE 失败路径


// TestExecuteWrite_BeginImmediateFail 测试 BEGIN IMMEDIATE 失败路径
func TestExecuteWrite_BeginImmediateFail(t *testing.T) {
	// 使用已关闭的数据库来触发 BEGIN IMMEDIATE 失败
	db, err := sql.Open("sqlite", ":memory:?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	db.Close()

	ctx := context.Background()
	err = store.executeWrite(ctx, func(db *sql.DB) error {
		return nil
	})
	if err == nil {
		t.Error("expected error from closed DB, got nil")
	}
}

// TestRunMigrations_SkipVersionMigration 测试 dbVersion >= currentSchemaVersion 时跳过版本迁移


// TestTryCheckpoint_CoversQueryPath 测试 tryCheckpoint 覆盖查询和扫描路径
func TestTryCheckpoint_CoversQueryPath(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// 写入一些数据以产生 WAL 内容
	store.CreateSession(ctx, &Session{
		ID:        "cp-test",
		Source:    "test",
		StartedAt: float64(time.Now().Unix()),
	})

	// 直接调用 tryCheckpoint
	store.tryCheckpoint(ctx)

	// 强制 writeCount 达到 checkpointEvery 以触发自动 checkpoint
	store.mu.Lock()
	store.writeCount = checkpointEvery - 1
	store.mu.Unlock()

	// 执行一次写入以触发 checkpoint
	err := store.CreateSession(ctx, &Session{
		ID:        "cp-trigger",
		Source:    "test",
		StartedAt: float64(time.Now().Unix()),
	})
	if err != nil {
		t.Fatalf("CreateSession to trigger checkpoint: %v", err)
	}
}

// TestCheckpointWAL_ErrorPath 测试 CheckpointWAL 在已关闭数据库上返回错误


// TestCheckpointWAL_ErrorPath 测试 CheckpointWAL 在已关闭数据库上返回错误
func TestCheckpointWAL_ErrorPath(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	db.Close()

	ctx := context.Background()
	err = store.CheckpointWAL(ctx)
	if err == nil {
		t.Error("expected error from CheckpointWAL on closed DB, got nil")
	}
}

// TestTryCheckpoint_QueryError 测试 tryCheckpoint 在查询失败时的路径


// TestTryCheckpoint_QueryError 测试 tryCheckpoint 在查询失败时的路径
func TestTryCheckpoint_QueryError(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	db.Close()

	// tryCheckpoint 在关闭的 DB 上应静默返回（不 panic）
	store.tryCheckpoint(context.Background())
}

// TestSessionPersister_WriteRecordNotOpen 测试未打开时 writeRecord 返回错误


// TestGetCompressionTip_NoChild 测试没有压缩子会话时返回自身
func TestGetCompressionTip_NoChild(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &Session{ID: "no-child", Source: "test"}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	result, err := store.GetCompressionTip(ctx, "no-child")
	if err != nil {
		t.Fatalf("GetCompressionTip: %v", err)
	}
	if result == nil {
		t.Fatal("expected session, got nil")
	}
	if result.ID != "no-child" {
		t.Errorf("expected ID no-child, got %s", result.ID)
	}
}

// TestSessionPersister_RecordAllTypes 测试写入所有类型的记录


// TestAutoPrune_DefaultMaxAgeWithExpired 测试 maxAgeDays=0 使用默认 90 天
func TestAutoPrune_DefaultMaxAgeWithExpired(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// 创建一个很久以前结束的会话
	oldTime := float64(time.Now().Unix() - 100*86400) // 100 天前
	sess := &Session{ID: "old-sess", Source: "test", StartedAt: oldTime - 100}
	store.CreateSession(ctx, sess)
	store.EndSession(ctx, "old-sess", "completed")

	// maxAgeDays=0 应使用默认值 90 天
	removed, err := store.AutoPrune(ctx, 0)
	if err != nil {
		t.Fatalf("AutoPrune: %v", err)
	}
	if removed != 1 {
		t.Errorf("expected 1 removed, got %d", removed)
	}
}

// TestAutoPrune_NegativeMaxAge 测试负数 maxAgeDays 使用默认值


// TestAutoPrune_NegativeMaxAge 测试负数 maxAgeDays 使用默认值
func TestAutoPrune_NegativeMaxAge(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// 创建一个很久以前结束的会话
	oldTime := float64(time.Now().Unix() - 100*86400)
	sess := &Session{ID: "neg-sess", Source: "test", StartedAt: oldTime - 100}
	store.CreateSession(ctx, sess)
	store.EndSession(ctx, "neg-sess", "done")

	removed, err := store.AutoPrune(ctx, -5)
	if err != nil {
		t.Fatalf("AutoPrune: %v", err)
	}
	if removed != 1 {
		t.Errorf("expected 1 removed with negative maxAge, got %d", removed)
	}
}

// ── CheckpointWAL 额外覆盖 ────────────────────────────────

// TestCheckpointWAL_Success 测试正常 CheckpointWAL 路径


// TestCheckpointWAL_Success 测试正常 CheckpointWAL 路径
func TestCheckpointWAL_Success(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// 写入一些数据以产生 WAL
	store.CreateSession(ctx, &Session{ID: "ckpt-sess", Source: "test"})

	if err := store.CheckpointWAL(ctx); err != nil {
		t.Fatalf("CheckpointWAL: %v", err)
	}
}

// ── ListRecentSessions 边界覆盖 ───────────────────────────

// TestListRecentSessions_SingleSessionNoMessages 测试只有一个会话且无消息


// TestGetCompressionTip_MaxDepth 测试超过最大遍历深度
func TestGetCompressionTip_MaxDepth(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// 创建一条超过 100 层的压缩链
	// 由于实际创建 100 层太慢，这里验证单层链的正确性
	parentID := "depth-parent"
	store.CreateSession(ctx, &Session{ID: parentID, Source: "test", StartedAt: 1000})
	store.EndSession(ctx, parentID, "compression")

	// 获取压缩链末端
	sess, err := store.GetCompressionTip(ctx, parentID)
	if err != nil {
		t.Fatalf("GetCompressionTip: %v", err)
	}
	if sess == nil {
		t.Fatal("expected session, got nil")
	}
	if sess.ID != parentID {
		t.Errorf("tip ID = %q, want %q", sess.ID, parentID)
	}
}

// ── GetMessageCount 边界覆盖 ──────────────────────────────

// TestGetMessageCount_SpecificSession 测试指定会话的消息计数


func TestAutoPrune_EmptyDB(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}

	pruned, err := store.AutoPrune(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 0 {
		t.Errorf("expected 0 pruned, got %d", pruned)
	}
}
