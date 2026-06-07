package state

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunMigrations(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "migrate.db")

	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	if err := RunMigrations(context.Background(), db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Verify tables exist
	tables := []string{"sessions", "messages", "schema_version", "state_meta"}
	for _, tbl := range tables {
		var count int
		err := db.QueryRowContext(context.Background(),
			"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", tbl,
		).Scan(&count)
		if err != nil {
			t.Fatalf("checking table %s: %v", tbl, err)
		}
		if count != 1 {
			t.Errorf("table %s not found", tbl)
		}
	}

	// Verify schema version
	var version int
	db.QueryRowContext(context.Background(), "SELECT version FROM schema_version").Scan(&version)
	if version != currentSchemaVersion {
		t.Errorf("schema version = %d, want %d", version, currentSchemaVersion)
	}
}



func TestRunMigrations_Idempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "idem.db")

	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	// Run migrations twice
	if err := RunMigrations(context.Background(), db); err != nil {
		t.Fatalf("first RunMigrations: %v", err)
	}
	if err := RunMigrations(context.Background(), db); err != nil {
		t.Fatalf("second RunMigrations: %v", err)
	}
}



func TestTableExists(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "exists.db")
	db, _ := sql.Open("sqlite", dbPath)
	defer db.Close()

	ctx := context.Background()
	db.ExecContext(ctx, "CREATE TABLE foo (id INTEGER)")

	exists, err := tableExists(ctx, db, "foo")
	if err != nil {
		t.Fatalf("tableExists: %v", err)
	}
	if !exists {
		t.Error("table 'foo' should exist")
	}

	exists, err = tableExists(ctx, db, "nonexistent")
	if err != nil {
		t.Fatalf("tableExists: %v", err)
	}
	if exists {
		t.Error("table 'nonexistent' should not exist")
	}
}



func TestGetSetSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "version.db")
	db, _ := sql.Open("sqlite", dbPath)
	defer db.Close()

	ctx := context.Background()
	db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)")

	// Initially no version → should return 0
	v, err := getSchemaVersion(ctx, db)
	if err != nil {
		t.Fatalf("getSchemaVersion: %v", err)
	}
	if v != 0 {
		t.Errorf("initial version = %d, want 0", v)
	}

	// Set version
	if err := setSchemaVersion(ctx, db, 11); err != nil {
		t.Fatalf("setSchemaVersion: %v", err)
	}

	v, _ = getSchemaVersion(ctx, db)
	if v != 11 {
		t.Errorf("version after set = %d, want 11", v)
	}

	// Update version
	setSchemaVersion(ctx, db, 12)
	v, _ = getSchemaVersion(ctx, db)
	if v != 12 {
		t.Errorf("version after update = %d, want 12", v)
	}
}

// ── executeWrite 重试测试 ─────────────────────────────────────



func TestReconcileColumns_AddsMissingColumns(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "reconcile.db")
	db, _ := sql.Open("sqlite", dbPath)
	defer db.Close()

	ctx := context.Background()

	// Create sessions table without some columns
	db.ExecContext(ctx, `CREATE TABLE sessions (
		id TEXT PRIMARY KEY,
		source TEXT NOT NULL,
		started_at REAL NOT NULL
	)`)
	db.ExecContext(ctx, `CREATE TABLE messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT NOT NULL,
		role TEXT NOT NULL
	)`)

	if err := reconcileColumns(ctx, db); err != nil {
		t.Fatalf("reconcileColumns: %v", err)
	}

	// Check that missing columns were added
	rows, _ := db.QueryContext(ctx, "PRAGMA table_info(sessions)")
	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, colType string
		var notnull int
		var defaultVal sql.NullString
		var pk int
		rows.Scan(&cid, &name, &colType, &notnull, &defaultVal, &pk)
		cols[name] = true
	}
	rows.Close()

	for _, col := range []string{"model", "title", "ended_at", "end_reason"} {
		if !cols[col] {
			t.Errorf("column %q should have been added by reconciliation", col)
		}
	}
}

// ── jitterSleep 测试 ──────────────────────────────────────────



func TestExecSchemaStatements_CreateTableError(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "schema_err.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	ctx := context.Background()

	// Duplicate table — second CREATE TABLE will fail
	db.ExecContext(ctx, "CREATE TABLE bad (id INTEGER PRIMARY KEY)")
	err = execSchemaStatements(ctx, db, "CREATE TABLE bad (id INTEGER PRIMARY KEY);")
	if err == nil {
		t.Error("expected error from duplicate CREATE TABLE, got nil")
	}
}



func TestExecSchemaStatements_MixedStatements(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "schema_mix.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	ctx := context.Background()

	sql := `
		CREATE TABLE valid_tbl (id INTEGER PRIMARY KEY);
		CREATE INDEX nonexistent_idx ON no_such_tbl(col);
		CREATE TABLE another_valid (k TEXT);
	`
	err = execSchemaStatements(ctx, db, sql)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	var count int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('valid_tbl','another_valid')").Scan(&count)
	if count != 2 {
		t.Errorf("expected 2 tables, got %d", count)
	}
}

// ── ensureFTS 测试 ──────────────────────────────────────────────



func TestEnsureFTS_CreatesTablesFromScratch(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "ensure_fts_fresh.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	// Create base schema tables
	schemaText := SchemaSQL()
	for _, stmt := range splitSQLStatements(schemaText) {
		upper := strings.ToUpper(strings.TrimSpace(stmt))
		if strings.Contains(upper, "CREATE VIRTUAL TABLE") || strings.Contains(upper, "CREATE TRIGGER") {
			continue
		}
		db.ExecContext(ctx, stmt)
	}

	if err := ensureFTS(ctx, db); err != nil {
		t.Fatalf("ensureFTS: %v", err)
	}

	var count int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='messages_fts'").Scan(&count)
	if count != 1 {
		t.Error("messages_fts should exist after ensureFTS")
	}
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='messages_fts_trigram'").Scan(&count)
	if count != 1 {
		t.Error("messages_fts_trigram should exist after ensureFTS")
	}
}



func TestEnsureFTS_WithExistingMessages(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "ensure_fts_with_data.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	// Create base schema
	schemaText := SchemaSQL()
	for _, stmt := range splitSQLStatements(schemaText) {
		upper := strings.ToUpper(strings.TrimSpace(stmt))
		if strings.Contains(upper, "CREATE VIRTUAL TABLE") || strings.Contains(upper, "CREATE TRIGGER") {
			continue
		}
		db.ExecContext(ctx, stmt)
	}

	// Insert a session and message before FTS tables exist
	db.ExecContext(ctx, `INSERT INTO sessions (id, source, started_at) VALUES ('fts-test', 'test', 1000)`)
	db.ExecContext(ctx, `INSERT INTO messages (session_id, role, content, timestamp) VALUES ('fts-test', 'user', 'hello backfill', 1001)`)

	if err := ensureFTS(ctx, db); err != nil {
		t.Fatalf("ensureFTS with data: %v", err)
	}

	var count int
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM messages_fts").Scan(&count)
	if count != 1 {
		t.Errorf("messages_fts should have 1 backfilled row, got %d", count)
	}
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM messages_fts_trigram").Scan(&count)
	if count != 1 {
		t.Errorf("messages_fts_trigram should have 1 backfilled row, got %d", count)
	}
}



func TestEnsureFTS_AlreadyExists(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "ensure_fts_exists.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	// Run full migration first
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// Call ensureFTS again — should be no-op
	if err := ensureFTS(ctx, db); err != nil {
		t.Fatalf("ensureFTS on existing: %v", err)
	}
}

// ── migrateV10 / migrateV11 测试 ────────────────────────────────
