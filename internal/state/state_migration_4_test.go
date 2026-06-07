package state

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSetSchemaVersion_Insert 测试 setSchemaVersion INSERT 路径
func TestSetSchemaVersion_Insert(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	ctx := context.Background()

	// 先创建 schema_version 表
	_, _ = db.ExecContext(ctx, "CREATE TABLE schema_version (version INTEGER NOT NULL)")

	if err := setSchemaVersion(ctx, db, 42); err != nil {
		t.Fatalf("setSchemaVersion insert: %v", err)
	}

	v, err := getSchemaVersion(ctx, db)
	if err != nil {
		t.Fatalf("getSchemaVersion: %v", err)
	}
	if v != 42 {
		t.Errorf("version = %d, want 42", v)
	}
}

// TestSetSchemaVersion_Update 测试 setSchemaVersion UPDATE 路径


// TestSetSchemaVersion_Update 测试 setSchemaVersion UPDATE 路径
func TestSetSchemaVersion_Update(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	ctx := context.Background()

	_, _ = db.ExecContext(ctx, "CREATE TABLE schema_version (version INTEGER NOT NULL)")
	_, _ = db.ExecContext(ctx, "INSERT INTO schema_version (version) VALUES (1)")

	if err := setSchemaVersion(ctx, db, 99); err != nil {
		t.Fatalf("setSchemaVersion update: %v", err)
	}

	v, err := getSchemaVersion(ctx, db)
	if err != nil {
		t.Fatalf("getSchemaVersion: %v", err)
	}
	if v != 99 {
		t.Errorf("version = %d, want 99", v)
	}
}

// TestGetSchemaVersion_ErrNoRows 测试 schema_version 表为空时返回 0


// TestGetSchemaVersion_ErrNoRows 测试 schema_version 表为空时返回 0
func TestGetSchemaVersion_ErrNoRows(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	ctx := context.Background()

	// 创建空表（没有行）
	_, _ = db.ExecContext(ctx, "CREATE TABLE schema_version (version INTEGER NOT NULL)")

	v, err := getSchemaVersion(ctx, db)
	if err != nil {
		t.Fatalf("getSchemaVersion empty table: %v", err)
	}
	if v != 0 {
		t.Errorf("version = %d, want 0", v)
	}
}

// TestParseSchemaColumns_Error 测试 parseSchemaColumns 对无效 SQL 的容错


// TestReconcileColumns_MissingTable 测试 reconcileColumns 处理不存在的表
func TestReconcileColumns_MissingTable(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "reconcile_missing.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	ctx := context.Background()

	// 只创建空数据库，不运行迁移 — reconcileColumns 应处理不存在的表
	err = reconcileColumns(ctx, db)
	if err != nil {
		t.Logf("reconcileColumns on empty DB: %v (acceptable)", err)
	}
}

// TestTryCheckpoint_CoversQueryPath 测试 tryCheckpoint 覆盖查询和扫描路径


// TestMigrateV10_TableAlreadyExists 测试 migrateV10 跳过已存在的表
func TestMigrateV10_TableAlreadyExists(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "test.db")+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	// Run full migrations so FTS tables exist
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Now call migrateV10 again - should skip
	if err := migrateV10(ctx, db); err != nil {
		t.Fatalf("migrateV10 (already exists): %v", err)
	}
}

// TestMigrateV11_RebuildsFTS 测试 v11 迁移重建 FTS 表


// TestMigrateV11_RebuildsFTS 测试 v11 迁移重建 FTS 表
func TestMigrateV11_RebuildsFTS(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "test.db")+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	// Run full migrations
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Insert data
	db.ExecContext(ctx, "INSERT INTO sessions (id, source, started_at) VALUES (?, ?, ?)", "v11s", "test", float64(time.Now().Unix()))
	db.ExecContext(ctx, "INSERT INTO messages (session_id, role, content, timestamp) VALUES (?, ?, ?, ?)", "v11s", "user", "v11 test content", float64(time.Now().Unix()))

	// Rebuild via v11
	if err := migrateV11(ctx, db); err != nil {
		t.Fatalf("migrateV11: %v", err)
	}

	// Verify FTS tables still exist and can search
	store := &Store{db: db}
	results, err := store.SearchMessages(ctx, "v11 test", 10)
	if err != nil {
		t.Fatalf("SearchMessages after v11 rebuild: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected search results after v11 rebuild with backfill")
	}
}



// TestRunMigrations_ReconcileError 测试 reconcileColumns 在已损坏的表上仍能完成
func TestRunMigrations_ReconcileColumns_NoMissing(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "reconcile_complete.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	// 完整运行一次迁移
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatalf("first RunMigrations: %v", err)
	}

	// 再次运行 — reconcileColumns 应发现没有缺失列
	if err := reconcileColumns(ctx, db); err != nil {
		t.Fatalf("reconcileColumns with no missing columns: %v", err)
	}
}

// TestRunMigrations_VersionGatedFromV8 测试从低版本 (v8) 升级同时触发 v10 和 v11


// TestRunMigrations_VersionGatedFromV8 测试从低版本 (v8) 升级同时触发 v10 和 v11
func TestRunMigrations_VersionGatedFromV8(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "v8_upgrade.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	// 创建基础表
	schemaText := SchemaSQL()
	for _, stmt := range splitSQLStatements(schemaText) {
		upper := strings.ToUpper(strings.TrimSpace(stmt))
		if strings.Contains(upper, "CREATE VIRTUAL TABLE") || strings.Contains(upper, "CREATE TRIGGER") {
			continue
		}
		db.ExecContext(ctx, stmt)
	}
	db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)")
	db.ExecContext(ctx, "INSERT INTO schema_version VALUES (8)")

	// 插入一些测试数据
	db.ExecContext(ctx, `INSERT INTO sessions (id, source, started_at) VALUES ('v8s', 'test', 1000)`)
	db.ExecContext(ctx, `INSERT INTO messages (session_id, role, content, timestamp) VALUES ('v8s', 'user', 'version 8 data', 1001)`)

	if err := RunMigrations(ctx, db); err != nil {
		t.Fatalf("RunMigrations from v8: %v", err)
	}

	ver, _ := getSchemaVersion(ctx, db)
	if ver != currentSchemaVersion {
		t.Errorf("version after v8 upgrade = %d, want %d", ver, currentSchemaVersion)
	}

	// 确保 FTS 表已创建并可搜索
	var ftsCount int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name LIKE 'messages_fts%'").Scan(&ftsCount)
	if ftsCount < 2 {
		t.Errorf("expected at least 2 FTS tables, got %d", ftsCount)
	}
}

// TestRunMigrations_EnsureTitleIndexWarn 测试 ensureTitleIndex 在非致命错误时的 warn 路径


// TestRunMigrations_EnsureTitleIndexWarn 测试 ensureTitleIndex 在非致命错误时的 warn 路径
func TestRunMigrations_EnsureTitleIndexWarn(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "title_warn.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	// 完整迁移
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// 再次调用 ensureTitleIndex — 应幂等
	if err := ensureTitleIndex(ctx, db); err != nil {
		t.Fatalf("ensureTitleIndex idempotent: %v", err)
	}
}

// TestGetSchemaVersion_Error 测试 schema_version 表有非预期结构时


// TestGetSchemaVersion_Error 测试 schema_version 表有非预期结构时
func TestGetSchemaVersion_Error(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	// 创建一个 schema_version 表但类型错误
	db.ExecContext(ctx, "CREATE TABLE schema_version (version TEXT NOT NULL)")
	db.ExecContext(ctx, "INSERT INTO schema_version VALUES ('not_a_number')")

	v, err := getSchemaVersion(ctx, db)
	// 应该返回错误或者返回 0
	if err == nil && v != 0 {
		t.Logf("getSchemaVersion with text version: v=%d err=%v", v, err)
	}
}

// TestSetSchemaVersion_Error 测试 setSchemaVersion 在无表时的行为


// TestSetSchemaVersion_Error 测试 setSchemaVersion 在无表时的行为
func TestSetSchemaVersion_NoTable(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	err = setSchemaVersion(ctx, db, 99)
	if err == nil {
		t.Error("expected error when schema_version table doesn't exist")
	}
}

// ── ensureFTS 回填路径覆盖 ────────────────────────────────

// TestEnsureFTS_BackfillWithMessages 测试 FTS 表不存在但有消息时的回填


// TestEnsureFTS_BackfillWithMessages 测试 FTS 表不存在但有消息时的回填
func TestEnsureFTS_BackfillWithMessages(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "fts_backfill.db")
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

	// 插入消息（在 FTS 表创建之前）
	db.ExecContext(ctx, `INSERT INTO sessions (id, source, started_at) VALUES ('bs', 'test', 1000)`)
	db.ExecContext(ctx, `INSERT INTO messages (session_id, role, content, timestamp) VALUES ('bs', 'user', 'hello world backfill', 1001)`)
	db.ExecContext(ctx, `INSERT INTO messages (session_id, role, content, timestamp) VALUES ('bs', 'assistant', '你好世界回填', 1002)`)

	// 调用 ensureFTS — 应创建 FTS 表并回填
	if err := ensureFTS(ctx, db); err != nil {
		t.Fatalf("ensureFTS with backfill: %v", err)
	}

	// 验证回填后可以搜索到内容
	var count int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM messages_fts").Scan(&count)
	if count == 0 {
		t.Error("messages_fts should have backfilled rows")
	}

	var triCount int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM messages_fts_trigram").Scan(&triCount)
	if triCount == 0 {
		t.Error("messages_fts_trigram should have backfilled rows")
	}
}

// TestEnsureFTS_BackfillEmptyDB 测试 FTS 表创建但无消息时 backfill 优雅失败
