package state

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateV10(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "v10.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	// Create base tables (skip FTS + triggers from schema.sql)
	schemaText := SchemaSQL()
	for _, stmt := range splitSQLStatements(schemaText) {
		upper := strings.ToUpper(strings.TrimSpace(stmt))
		if strings.Contains(upper, "CREATE VIRTUAL TABLE") || strings.Contains(upper, "CREATE TRIGGER") {
			continue
		}
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Logf("exec base stmt: %v", err)
		}
	}

	// Insert test data before migration
	if _, err := db.ExecContext(ctx, `INSERT INTO sessions (id, source, started_at) VALUES ('v10s', 'test', 1000)`); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO messages (session_id, role, content, timestamp) VALUES ('v10s', 'user', '测试中文', 1001)`); err != nil {
		t.Fatalf("insert message: %v", err)
	}

	if err := migrateV10(ctx, db); err != nil {
		t.Fatalf("migrateV10: %v", err)
	}

	// Verify trigram table exists
	var exists int
	err = db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='messages_fts_trigram'",
	).Scan(&exists)
	if err != nil {
		t.Fatalf("check trigram table: %v", err)
	}
	if exists == 0 {
		t.Error("messages_fts_trigram table should exist after migrateV10")
	}

	// Verify triggers were created
	for _, trig := range []string{
		"messages_fts_trigram_insert",
		"messages_fts_trigram_delete",
		"messages_fts_trigram_update",
	} {
		var cnt int
		db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name=?", trig,
		).Scan(&cnt)
		if cnt == 0 {
			t.Errorf("trigger %s should exist", trig)
		}
	}
}



func TestMigrateV10_Idempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "v10_idem.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	schemaText := SchemaSQL()
	for _, stmt := range splitSQLStatements(schemaText) {
		upper := strings.ToUpper(strings.TrimSpace(stmt))
		if strings.Contains(upper, "CREATE VIRTUAL TABLE") || strings.Contains(upper, "CREATE TRIGGER") {
			continue
		}
		db.ExecContext(ctx, stmt)
	}

	if err := migrateV10(ctx, db); err != nil {
		t.Fatalf("first migrateV10: %v", err)
	}
	if err := migrateV10(ctx, db); err != nil {
		t.Fatalf("second migrateV10 (idempotent): %v", err)
	}
}



func TestMigrateV11(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "v11.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	// Create base tables (skip FTS + triggers)
	schemaText := SchemaSQL()
	for _, stmt := range splitSQLStatements(schemaText) {
		upper := strings.ToUpper(strings.TrimSpace(stmt))
		// Skip FTS virtual tables, triggers, and FTS-related indexes
		if strings.Contains(upper, "CREATE VIRTUAL TABLE") ||
			strings.Contains(upper, "CREATE TRIGGER") ||
			strings.Contains(upper, "MESSAGES_FTS") {
			continue
		}
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Logf("exec base stmt: %v", err)
		}
	}

	// Create old-style FTS tables (pre-v11 state)
	if _, err := db.ExecContext(ctx, `CREATE VIRTUAL TABLE messages_fts USING fts5(content)`); err != nil {
		t.Fatalf("create old fts: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE VIRTUAL TABLE messages_fts_trigram USING fts5(content, tokenize='trigram')`); err != nil {
		t.Fatalf("create old trigram: %v", err)
	}

	// Insert test data
	if _, err := db.ExecContext(ctx, `INSERT INTO sessions (id, source, started_at) VALUES ('v11s', 'test', 1000)`); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO messages (session_id, role, content, tool_name, timestamp) VALUES ('v11s', 'user', 'old data', 'Read', 1001)`); err != nil {
		t.Fatalf("insert message: %v", err)
	}

	if err := migrateV11(ctx, db); err != nil {
		t.Fatalf("migrateV11: %v", err)
	}

	// Verify FTS tables were recreated
	for _, tbl := range []string{"messages_fts", "messages_fts_trigram"} {
		var cnt int
		db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", tbl,
		).Scan(&cnt)
		if cnt == 0 {
			t.Errorf("table %s should exist after migrateV11", tbl)
		}
	}

	// Verify triggers were recreated with new names
	for _, trig := range []string{
		"messages_fts_insert",
		"messages_fts_delete",
		"messages_fts_update",
		"messages_fts_trigram_insert",
		"messages_fts_trigram_delete",
		"messages_fts_trigram_update",
	} {
		var cnt int
		db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name=?", trig,
		).Scan(&cnt)
		if cnt == 0 {
			t.Errorf("trigger %s should exist after migrateV11", trig)
		}
	}
}

// ── RunMigrations 全版本测试 ────────────────────────────────────



func TestRunMigrations_FreshDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "fresh_migrate.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	if err := RunMigrations(ctx, db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Verify schema version
	var version int
	db.QueryRowContext(ctx, "SELECT version FROM schema_version LIMIT 1").Scan(&version)
	if version != currentSchemaVersion {
		t.Errorf("schema version = %d, want %d", version, currentSchemaVersion)
	}

	// Verify FTS tables exist
	var count int
		db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('messages_fts', 'messages_fts_trigram')").Scan(&count)
	if count != 2 {
		t.Errorf("expected 2 FTS tables, got %d", count)
	}

	// Idempotent
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatalf("RunMigrations idempotent: %v", err)
	}
}



func TestRunMigrations_VersionUpgrade(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "version_upgrade.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	// Set low version to trigger migrations
	db.ExecContext(ctx, "CREATE TABLE schema_version (version INTEGER)")
	db.ExecContext(ctx, "INSERT INTO schema_version (version) VALUES (0)")

	if err := RunMigrations(ctx, db); err != nil {
		t.Fatalf("RunMigrations with version upgrade: %v", err)
	}

	var version int
	db.QueryRowContext(ctx, "SELECT version FROM schema_version LIMIT 1").Scan(&version)
	if version != currentSchemaVersion {
		t.Errorf("schema version after upgrade = %d, want %d", version, currentSchemaVersion)
	}
}

// ── searchLatin 错误路径测试 ────────────────────────────────────



func TestSchemaVersion_NoTable(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "noversion.db")+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	ver, err := getSchemaVersion(context.Background(), db)
	if err != nil {
		t.Fatalf("getSchemaVersion: %v", err)
	}
	if ver != 0 {
		t.Errorf("expected version 0 with no table, got %d", ver)
	}
}



func TestReconcileColumns_Idempotent(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Run migrations again on existing DB — should be idempotent
	err := RunMigrations(ctx, store.DB())
	if err != nil {
		t.Fatalf("RunMigrations idempotent: %v", err)
	}
}



func TestRunMigrations_VersionGated(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "versioned.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// Create minimal schema manually (no FTS, version 0)
	_, err = db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL DEFAULT '',
			started_at REAL NOT NULL DEFAULT 0
		);
		CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			role TEXT NOT NULL,
			content TEXT DEFAULT '',
			timestamp REAL NOT NULL DEFAULT 0
		);
		CREATE TABLE IF NOT EXISTS schema_version (
			version INTEGER NOT NULL
		);
		INSERT INTO schema_version VALUES (0);
	`)
	if err != nil {
		t.Fatalf("create minimal schema: %v", err)
	}

	// Insert a session and message to test backfill
	_, err = db.ExecContext(ctx, `INSERT INTO sessions (id, source, started_at) VALUES ('s1', 'test', 1.0)`)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO messages (session_id, role, content, timestamp) VALUES ('s1', 'user', 'hello world test', 1.0)`)
	if err != nil {
		t.Fatalf("insert message: %v", err)
	}

	// Run full migrations — should execute v10, v11, ensureFTS etc.
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatalf("RunMigrations from v0: %v", err)
	}

	// Verify version was updated
	ver, err := getSchemaVersion(ctx, db)
	if err != nil {
		t.Fatalf("getSchemaVersion: %v", err)
	}
	if ver != currentSchemaVersion {
		t.Errorf("expected version %d, got %d", currentSchemaVersion, ver)
	}

	// Verify FTS works — search for the message we inserted
	results, err := (&Store{db: db}).SearchMessages(ctx, "hello world", 10)
	if err != nil {
		t.Fatalf("SearchMessages after migration: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected FTS to find the message after migration")
	}
}



// TestRunMigrations_FromV9 测试从 v9 升级到 v11 (触发 v10 和 v11 迁移)
func TestRunMigrations_FromV9(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "v9_upgrade.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	// Create base tables + schema_version at v9
	schemaText := SchemaSQL()
	for _, stmt := range splitSQLStatements(schemaText) {
		upper := strings.ToUpper(strings.TrimSpace(stmt))
		if strings.Contains(upper, "CREATE VIRTUAL TABLE") || strings.Contains(upper, "CREATE TRIGGER") {
			continue
		}
		db.ExecContext(ctx, stmt)
	}
	db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)")
	db.ExecContext(ctx, "INSERT INTO schema_version VALUES (9)")

	// Insert test data
	db.ExecContext(ctx, `INSERT INTO sessions (id, source, started_at) VALUES ('v9s', 'test', 1000)`)
	db.ExecContext(ctx, `INSERT INTO messages (session_id, role, content, timestamp) VALUES ('v9s', 'user', '测试迁移', 1001)`)

	if err := RunMigrations(ctx, db); err != nil {
		t.Fatalf("RunMigrations from v9: %v", err)
	}

	ver, _ := getSchemaVersion(ctx, db)
	if ver != currentSchemaVersion {
		t.Errorf("version after v9 upgrade = %d, want %d", ver, currentSchemaVersion)
	}
}

// TestEnsureFTS_PartialExists 测试 FTS 表存在但 trigram 不存在的情况
