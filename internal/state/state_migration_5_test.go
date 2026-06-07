package state

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// TestEnsureFTS_BackfillEmptyDB 测试 FTS 表创建但无消息时 backfill 优雅失败
func TestEnsureFTS_BackfillEmptyDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "fts_empty_backfill.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	// 仅创建基础表
	if err := execSchemaStatements(ctx, db, schemaSQL); err != nil {
		t.Fatalf("execSchemaStatements: %v", err)
	}

	// 不插入任何消息，调用 ensureFTS
	if err := ensureFTS(ctx, db); err != nil {
		t.Fatalf("ensureFTS empty backfill: %v", err)
	}

	ftsExists, _ := tableExists(ctx, db, "messages_fts")
	if !ftsExists {
		t.Error("messages_fts should exist even with no messages")
	}
}

// ── runVersionMigrations 路径覆盖 ──────────────────────────

// TestRunVersionMigrations_V10AndV11 测试 fromVersion < 10 触发 v10 和 v11


// TestRunVersionMigrations_V10AndV11 测试 fromVersion < 10 触发 v10 和 v11
func TestRunVersionMigrations_V10AndV11(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "vmig_9.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	// 创建基础表
	if err := execSchemaStatements(ctx, db, schemaSQL); err != nil {
		t.Fatalf("execSchemaStatements: %v", err)
	}

	// 插入测试数据
	db.ExecContext(ctx, `INSERT INTO sessions (id, source, started_at) VALUES ('vs', 'test', 1000)`)
	db.ExecContext(ctx, `INSERT INTO messages (session_id, role, content, timestamp) VALUES ('vs', 'user', 'test content', 1001)`)

	// 从版本 9 开始运行版本迁移
	if err := runVersionMigrations(ctx, db, 9); err != nil {
		t.Fatalf("runVersionMigrations from v9: %v", err)
	}

	// 验证 trigram 表存在
	var count int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='messages_fts_trigram'").Scan(&count)
	if count != 1 {
		t.Error("messages_fts_trigram should exist after runVersionMigrations from v9")
	}
}

// TestRunVersionMigrations_FromV10 测试 fromVersion=10 只触发 v11


// TestRunVersionMigrations_FromV10 测试 fromVersion=10 只触发 v11
func TestRunVersionMigrations_FromV10(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "vmig_10.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	// 创建基础表
	if err := execSchemaStatements(ctx, db, schemaSQL); err != nil {
		t.Fatalf("execSchemaStatements: %v", err)
	}

	// 插入测试数据
	db.ExecContext(ctx, `INSERT INTO sessions (id, source, started_at) VALUES ('v10s', 'test', 1000)`)
	db.ExecContext(ctx, `INSERT INTO messages (session_id, role, content, timestamp) VALUES ('v10s', 'user', 'v10 test', 1001)`)

	// 从版本 10 开始 — 应跳过 v10，仅执行 v11
	if err := runVersionMigrations(ctx, db, 10); err != nil {
		t.Fatalf("runVersionMigrations from v10: %v", err)
	}
}

// TestRunVersionMigrations_NoMigrationNeeded 测试 fromVersion >= currentSchemaVersion


// TestRunVersionMigrations_NoMigrationNeeded 测试 fromVersion >= currentSchemaVersion
func TestRunVersionMigrations_NoMigrationNeeded(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "vmig_current.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	// 创建基础表
	if err := execSchemaStatements(ctx, db, schemaSQL); err != nil {
		t.Fatalf("execSchemaStatements: %v", err)
	}

	// 从当前版本开始 — 不应有任何迁移
	if err := runVersionMigrations(ctx, db, currentSchemaVersion); err != nil {
		t.Fatalf("runVersionMigrations at current version: %v", err)
	}
}

// ── SessionPersister rotation 路径覆盖 ───────────────────

// TestSessionPersister_RotationShiftsFiles 测试轮转时旧文件被移动


// TestMigrateV11_BackfillWarn 测试 v11 回填时无消息数据的 warn 路径
func TestMigrateV11_BackfillWarn(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "v11_backfill_warn.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	// 创建基础表
	if err := execSchemaStatements(ctx, db, schemaSQL); err != nil {
		t.Fatalf("execSchemaStatements: %v", err)
	}

	// 不插入消息，直接运行 v11 迁移
	if err := migrateV11(ctx, db); err != nil {
		t.Fatalf("migrateV11 with no data: %v", err)
	}

	// FTS 表应该存在
	ftsExists, _ := tableExists(ctx, db, "messages_fts")
	if !ftsExists {
		t.Error("messages_fts should exist after migrateV11")
	}
}

// ── parseSchemaColumns 边界覆盖 ───────────────────────────

// TestParseSchemaColumns_Valid 测试 parseSchemaColumns 正常解析


func TestEnsureFTS_Idempotent(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", dir+"/test.db?_journal_mode=WAL")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()

	if err := ensureFTS(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := ensureFTS(ctx, db); err != nil {
		t.Fatal(err)
	}
}

// ── 新增测试: 覆盖率补全 ──────────────────────────────────────



func TestRunMigrations_SkipsWhenAlreadyCurrent(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	db := store.DB()

	// Run migrations to get to current version
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}

	// Run again - should be no-op
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatalf("second RunMigrations should succeed: %v", err)
	}
}



func TestEnsureFTS_BothTablesAlreadyExist(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	db := store.DB()

	// Run migrations to create FTS tables
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}

	// Call CreateFTSTables again - should be no-op
	if err := store.CreateFTSTables(ctx); err != nil {
		t.Fatalf("CreateFTSTables on existing tables should succeed: %v", err)
	}
}





func TestRunMigrations_SkipWhenVersionCurrent(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	// Run migrations once to get to current version
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	// Run again - should skip version migrations entirely
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
}



func TestRunVersionMigrations_AllVersionsCurrent(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	db := store.DB()
	// Create base schema first so schema_version table exists
	if err := execSchemaStatements(ctx, db, schemaSQL); err != nil {
		t.Fatal(err)
	}
	// Set version to 11 (current) - no migrations should run
	if err := setSchemaVersion(ctx, db, 11); err != nil {
		t.Fatal(err)
	}
	if err := runVersionMigrations(ctx, db, 11); err != nil {
		t.Fatal(err)
	}
}



func TestReconcileColumns_AddsColumn(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	db := store.DB()
	// Create schema first
	if err := execSchemaStatements(ctx, db, schemaSQL); err != nil {
		t.Fatal(err)
	}
	// Drop a column by recreating table without it
	db.ExecContext(ctx, "ALTER TABLE sessions RENAME TO sessions_old")
	db.ExecContext(ctx, "CREATE TABLE sessions (id TEXT PRIMARY KEY, source TEXT)")
	db.ExecContext(ctx, "DROP TABLE sessions_old")
	// reconcileColumns should add missing columns back
	if err := reconcileColumns(ctx, db); err != nil {
		t.Fatalf("reconcileColumns should handle missing columns: %v", err)
	}
}



func TestEnsureFTS_CreatesFromEmpty(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	db := store.DB()
	if err := execSchemaStatements(ctx, db, schemaSQL); err != nil {
		t.Fatal(err)
	}
	// Insert a message before FTS tables exist
	sess := &Session{ID: "fts-test", Source: "test", StartedAt: float64(time.Now().Unix())}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	msg := &MessageRecord{SessionID: "fts-test", Role: "user", Content: "backfill me", Timestamp: float64(time.Now().Unix())}
	if err := store.InsertMessage(ctx, msg); err != nil {
		t.Fatal(err)
	}
	// Drop FTS tables if they exist
	db.ExecContext(ctx, "DROP TABLE IF EXISTS messages_fts")
	db.ExecContext(ctx, "DROP TABLE IF EXISTS messages_fts_trigram")
	// ensureFTS should recreate them and backfill
	if err := ensureFTS(ctx, db); err != nil {
		t.Fatalf("ensureFTS from empty: %v", err)
	}
	results, err := store.SearchMessages(ctx, "backfill", 10)
	if err != nil {
		t.Fatalf("SearchMessages error: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected backfilled message to be found via FTS")
	}
}



func TestExecSchemaStatements_CreateTableFail(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", dir+"/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	// Create a table with invalid SQL
	err = execSchemaStatements(ctx, db, "CREATE TABLE bad(table)")
	if err == nil {
		t.Error("expected error for invalid CREATE TABLE")
	}
}



func TestRunMigrations_AlreadyCurrent(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	db := store.DB()
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatalf("second RunMigrations should succeed: %v", err)
	}
}
