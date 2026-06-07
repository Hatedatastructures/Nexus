package state

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestRunMigrations_VersionUpgradeFrom9(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/test.db"
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	ctx := context.Background()

	// Run base schema creation
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}

	// Set version back to 9 to trigger v10 + v11 migrations
	if _, err := db.ExecContext(ctx, "UPDATE schema_version SET version = 9"); err != nil {
		t.Fatal(err)
	}

	// Add a message so backfill has data
	if _, err := db.ExecContext(ctx, "INSERT INTO sessions (id, source, started_at) VALUES ('v9test', 'test', 1000.0)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO messages (session_id, role, content, timestamp) VALUES ('v9test', 'user', 'migration test data', 1000.0)"); err != nil {
		t.Fatal(err)
	}

	// Re-run migration - should trigger v10 + v11
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatalf("RunMigrations from v9 failed: %v", err)
	}

	var version int
	err = db.QueryRowContext(ctx, "SELECT version FROM schema_version LIMIT 1").Scan(&version)
	if err != nil || version != currentSchemaVersion {
		t.Errorf("expected version %d, got %d, err=%v", currentSchemaVersion, version, err)
	}
}




func TestRunMigrations_FromVersion10(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/test.db"
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	// Full migration first
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}

	// Set version to 10 — should trigger v11 only
	if _, err := db.ExecContext(ctx, "UPDATE schema_version SET version = 10"); err != nil {
		t.Fatal(err)
	}

	// Re-run — should only run v11 migration
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatalf("RunMigrations from v10 failed: %v", err)
	}

	var version int
	err = db.QueryRowContext(ctx, "SELECT version FROM schema_version").Scan(&version)
	if err != nil || version != currentSchemaVersion {
		t.Errorf("expected version %d, got %d, err=%v", currentSchemaVersion, version, err)
	}
}





func TestRunMigrations_FullVersionPath(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/fullpath.db"
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	// Full migration
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}

	// Set version to 0 to force all version migrations
	if _, err := db.ExecContext(ctx, "DELETE FROM schema_version"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO schema_version (version) VALUES (0)"); err != nil {
		t.Fatal(err)
	}

	// Re-run with full version path
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatalf("RunMigrations from v0 failed: %v", err)
	}

	var version int
	err = db.QueryRowContext(ctx, "SELECT version FROM schema_version").Scan(&version)
	if err != nil || version != currentSchemaVersion {
		t.Errorf("expected version %d, got %d, err=%v", currentSchemaVersion, version, err)
	}
}









func TestMigrateV10_AlreadyExists(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", dir+"/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	// Full migration
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}

	// Running migrateV10 again should skip since trigram table exists
	if err := migrateV10(ctx, db); err != nil {
		t.Fatalf("migrateV10 on existing trigram failed: %v", err)
	}
}



func TestMigrateV11_Clean(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", dir+"/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	// Run base schema but not FTS
	if err := execSchemaStatements(ctx, db, schemaSQL); err != nil {
		t.Fatal(err)
	}

	// Insert a message before v11 migration
	db.ExecContext(ctx, "INSERT INTO sessions (id, source, started_at) VALUES ('v11-test', 'test', ?)", float64(time.Now().UnixMilli()))
	db.ExecContext(ctx, "INSERT INTO messages (session_id, role, content, timestamp) VALUES ('v11-test', 'user', 'hello v11', ?)", float64(time.Now().UnixMilli()))

	// Run v11 migration
	if err := migrateV11(ctx, db); err != nil {
		t.Fatalf("migrateV11 failed: %v", err)
	}

	// Verify FTS tables were created
	for _, tbl := range []string{"messages_fts", "messages_fts_trigram"} {
		var count int
		db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", tbl).Scan(&count)
		if count != 1 {
			t.Errorf("%s table should exist after v11 migration", tbl)
		}
	}
}



func TestReconcileColumns_AddsMissingColumn(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", dir+"/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	// Create a minimal sessions table missing many columns
	_, err = db.ExecContext(ctx, "CREATE TABLE sessions (id TEXT PRIMARY KEY)")
	if err != nil {
		t.Fatal(err)
	}

	// Run reconciliation
	if err := reconcileColumns(ctx, db); err != nil {
		t.Fatalf("reconcileColumns failed: %v", err)
	}

	// Check that 'source' column was added
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(sessions)")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var cid int
		var name, colType string
		var notnull int
		var defaultVal sql.NullString
		var pk int
		rows.Scan(&cid, &name, &colType, &notnull, &defaultVal, &pk)
		if name == "source" {
			found = true
		}
	}
	if !found {
		t.Error("source column should have been added by reconcileColumns")
	}
}



func TestRunMigrations_ClosedDB2(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", dir+"/test.db?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	db.Close()
	err = RunMigrations(ctx, db)
	if err == nil {
		t.Fatal("expected error on closed DB")
	}
}



func TestRunMigrations_CancelCtx(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", dir+"/test.db?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = RunMigrations(ctx, db)
	if err == nil {
		t.Fatal("expected error with cancelled context")
	}
}



func TestMigrateV10_ClosedDB2(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", dir+"/test.db?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	db.Close()
	err = migrateV10(ctx, db)
	if err == nil {
		t.Fatal("expected error on closed DB")
	}
}



func TestMigrateV11_ClosedDB2(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", dir+"/test.db?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	db.Close()
	err = migrateV11(ctx, db)
	if err != nil {
		t.Fatalf("migrateV11 should return nil on closed DB (backfill errors are logged): %v", err)
	}
}



func TestEnsureFTS_ClosedDB2(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", dir+"/test.db?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	db.Exec("DROP TABLE IF EXISTS messages_fts")
	db.Exec("DROP TABLE IF EXISTS messages_fts_trigram")
	db.Close()
	err = ensureFTS(ctx, db)
	if err == nil {
		t.Fatal("expected error on closed DB")
	}
}



func TestReconcileColumns_ClosedDB2(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", dir+"/test.db?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	db.Close()
	err = reconcileColumns(ctx, db)
	if err != nil {
		t.Fatalf("reconcileColumns should return nil on closed DB (table info failures are logged): %v", err)
	}
}



func TestGetSchemaVersion_ClosedDB2(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", dir+"/test.db?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	db.Close()
	_, err = getSchemaVersion(ctx, db)
	if err == nil {
		t.Fatal("expected error on closed DB")
	}
}
