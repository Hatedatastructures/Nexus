package state

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnsureFTS_PartialExists 测试 FTS 表存在但 trigram 不存在的情况
func TestEnsureFTS_PartialExists(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "partial_fts.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	// Create base tables from schema (skip FTS + triggers)
	schemaText := SchemaSQL()
	for _, stmt := range splitSQLStatements(schemaText) {
		upper := strings.ToUpper(strings.TrimSpace(stmt))
		if strings.Contains(upper, "CREATE VIRTUAL TABLE") || strings.Contains(upper, "CREATE TRIGGER") {
			continue
		}
		db.ExecContext(ctx, stmt)
	}

	// Create only the default FTS table (not trigram)
	db.ExecContext(ctx, `CREATE VIRTUAL TABLE messages_fts USING fts5(content)`)

	if err := ensureFTS(ctx, db); err != nil {
		t.Fatalf("ensureFTS partial: %v", err)
	}

	// Trigram table should now exist
	var count int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='messages_fts_trigram'").Scan(&count)
	if count != 1 {
		t.Error("messages_fts_trigram should be created when missing")
	}
}

// TestMigrateV10_SkipWhenExists 测试 v10 迁移跳过已存在的 trigram 表


// TestMigrateV10_SkipWhenExists 测试 v10 迁移跳过已存在的 trigram 表
func TestMigrateV10_SkipWhenExists(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "v10_skip.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	// Create base tables
	schemaText := SchemaSQL()
	for _, stmt := range splitSQLStatements(schemaText) {
		upper := strings.ToUpper(strings.TrimSpace(stmt))
		if strings.Contains(upper, "CREATE VIRTUAL TABLE") || strings.Contains(upper, "CREATE TRIGGER") {
			continue
		}
		db.ExecContext(ctx, stmt)
	}

	// Run v10 once to create trigram table
	if err := migrateV10(ctx, db); err != nil {
		t.Fatalf("first migrateV10: %v", err)
	}

	// Run v10 again — should skip (exists check path)
	if err := migrateV10(ctx, db); err != nil {
		t.Fatalf("second migrateV10 (skip): %v", err)
	}
}

// TestMigrateV11_NoOldTables 测试 v11 迁移时旧 FTS 表不存在的情况


// TestMigrateV11_NoOldTables 测试 v11 迁移时旧 FTS 表不存在的情况
func TestMigrateV11_NoOldTables(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "v11_no_old.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	// Create only base tables (no FTS at all)
	schemaText := SchemaSQL()
	for _, stmt := range splitSQLStatements(schemaText) {
		upper := strings.ToUpper(strings.TrimSpace(stmt))
		if strings.Contains(upper, "CREATE VIRTUAL TABLE") || strings.Contains(upper, "CREATE TRIGGER") {
			continue
		}
		db.ExecContext(ctx, stmt)
	}

	// Insert test data
	db.ExecContext(ctx, `INSERT INTO sessions (id, source, started_at) VALUES ('v11ns', 'test', 1000)`)
	db.ExecContext(ctx, `INSERT INTO messages (session_id, role, content, tool_name, timestamp) VALUES ('v11ns', 'user', 'no old fts', 'Edit', 1001)`)

	// migrateV11 should handle missing old tables gracefully
	if err := migrateV11(ctx, db); err != nil {
		t.Fatalf("migrateV11 no old tables: %v", err)
	}

	// Verify FTS tables were created
	for _, tbl := range []string{"messages_fts", "messages_fts_trigram"} {
		var count int
		db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", tbl).Scan(&count)
		if count != 1 {
			t.Errorf("table %s should exist after migrateV11", tbl)
		}
	}
}

// TestSessionPersister_RotationMaxFiles 测试 rotation 删除最旧文件


// TestEnsureTitleIndex 测试标题唯一索引创建
func TestEnsureTitleIndex(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "title_idx.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	// Create base schema without running full migrations
	schemaText := SchemaSQL()
	for _, stmt := range splitSQLStatements(schemaText) {
		upper := strings.ToUpper(strings.TrimSpace(stmt))
		if strings.Contains(upper, "CREATE VIRTUAL TABLE") || strings.Contains(upper, "CREATE TRIGGER") {
			continue
		}
		db.ExecContext(ctx, stmt)
	}

	if err := ensureTitleIndex(ctx, db); err != nil {
		t.Fatalf("ensureTitleIndex: %v", err)
	}

	// Verify index exists
	var count int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_sessions_title_unique'").Scan(&count)
	if count != 1 {
		t.Error("title unique index should exist")
	}

	// Idempotent
	if err := ensureTitleIndex(ctx, db); err != nil {
		t.Fatalf("ensureTitleIndex idempotent: %v", err)
	}
}

// TestNewStore_WALMode 验证 WAL 模式可被启用


// TestSetSchemaVersion_InsertPath 测试 setSchemaVersion 的 INSERT 路径
func TestSetSchemaVersion_InsertPath(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "version_insert.db"))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	db.ExecContext(ctx, "CREATE TABLE schema_version (version INTEGER NOT NULL)")

	// First set should INSERT (no existing row)
	if err := setSchemaVersion(ctx, db, 5); err != nil {
		t.Fatalf("setSchemaVersion INSERT: %v", err)
	}

	var v int
	db.QueryRowContext(ctx, "SELECT version FROM schema_version").Scan(&v)
	if v != 5 {
		t.Errorf("version = %d, want 5", v)
	}

	// Second set should UPDATE
	if err := setSchemaVersion(ctx, db, 10); err != nil {
		t.Fatalf("setSchemaVersion UPDATE: %v", err)
	}
	db.QueryRowContext(ctx, "SELECT version FROM schema_version").Scan(&v)
	if v != 10 {
		t.Errorf("version = %d, want 10", v)
	}
}

// TestScanSearchResults_Empty 测试 scanSearchResults 处理空结果


// TestRunMigrations_SkipVersionMigration 测试 dbVersion >= currentSchemaVersion 时跳过版本迁移
func TestRunMigrations_SkipVersionMigration(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "skip_vmig.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	ctx := context.Background()

	// 首次迁移，设置 schema_version 到 currentSchemaVersion
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatalf("first RunMigrations: %v", err)
	}

	// 手动设置为当前版本，确保版本门控迁移被跳过
	_, _ = db.ExecContext(ctx, "UPDATE schema_version SET version = ?", currentSchemaVersion)

	// 再次运行迁移 — 应跳过 runVersionMigrations 路径
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatalf("second RunMigrations (skip version): %v", err)
	}
}

// TestRunMigrations_ExecSchemaError 测试 execSchemaStatements 返回错误路径


// TestRunMigrations_ExecSchemaError 测试 execSchemaStatements 返回错误路径
func TestRunMigrations_ExecSchemaError(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "schema_err.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	ctx := context.Background()

	err = execSchemaStatements(ctx, db, "CREATE TABLE !!!invalid (id INT)")
	if err == nil {
		t.Error("expected error from invalid CREATE TABLE, got nil")
	}
}

// TestRunVersionMigrations_V10Skip 测试 fromVersion >= 10 跳过 v10 迁移


// TestRunVersionMigrations_V10Skip 测试 fromVersion >= 10 跳过 v10 迁移
func TestRunVersionMigrations_V10Skip(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "v10skip.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	ctx := context.Background()

	// 先运行完整迁移创建基础表
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatalf("initial RunMigrations: %v", err)
	}

	// 直接调用 runVersionMigrations fromVersion=10 (应跳过 v10)
	if err := runVersionMigrations(ctx, db, 10); err != nil {
		t.Fatalf("runVersionMigrations fromVersion=10: %v", err)
	}

	// 直接调用 runVersionMigrations fromVersion=11 (应跳过 v10 和 v11)
	if err := runVersionMigrations(ctx, db, 11); err != nil {
		t.Fatalf("runVersionMigrations fromVersion=11: %v", err)
	}
}

// TestEnsureFTS_TablesAlreadyExist 测试 FTS 表已存在时跳过创建


// TestEnsureFTS_TablesAlreadyExist 测试 FTS 表已存在时跳过创建
func TestEnsureFTS_TablesAlreadyExist(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "fts_exist.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	ctx := context.Background()

	// 首次创建
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatalf("initial RunMigrations: %v", err)
	}

	// 再次调用 ensureFTS — 应跳过创建
	if err := ensureFTS(ctx, db); err != nil {
		t.Fatalf("ensureFTS when tables exist: %v", err)
	}
}

// TestEnsureFTS_CreateFromScratch 测试 FTS 表不存在时创建


// TestEnsureFTS_CreateFromScratch 测试 FTS 表不存在时创建
func TestEnsureFTS_CreateFromScratch(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "fts_new.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	ctx := context.Background()

	// 创建基础表但不创建 FTS 表
	if err := execSchemaStatements(ctx, db, schemaSQL); err != nil {
		t.Fatalf("execSchemaStatements: %v", err)
	}

	// 调用 ensureFTS — 应创建 FTS 表
	if err := ensureFTS(ctx, db); err != nil {
		t.Fatalf("ensureFTS from scratch: %v", err)
	}

	ftsExists, err := tableExists(ctx, db, "messages_fts")
	if err != nil {
		t.Fatalf("tableExists messages_fts: %v", err)
	}
	if !ftsExists {
		t.Error("messages_fts should exist after ensureFTS")
	}

	trigramExists, err := tableExists(ctx, db, "messages_fts_trigram")
	if err != nil {
		t.Fatalf("tableExists messages_fts_trigram: %v", err)
	}
	if !trigramExists {
		t.Error("messages_fts_trigram should exist after ensureFTS")
	}
}

// TestGetSchemaVersion_NoTable 测试 schema_version 表不存在时返回 0


// TestGetSchemaVersion_NoTable 测试 schema_version 表不存在时返回 0
func TestGetSchemaVersion_NoTable(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	ctx := context.Background()

	v, err := getSchemaVersion(ctx, db)
	if err != nil {
		t.Fatalf("getSchemaVersion no table: %v", err)
	}
	if v != 0 {
		t.Errorf("getSchemaVersion no table = %d, want 0", v)
	}
}

// TestSetSchemaVersion_Insert 测试 setSchemaVersion INSERT 路径
