package state

import (
	"context"
	"database/sql"
	"testing"
)

func TestReconcileColumns_ActuallyAddsColumns(t *testing.T) {
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
	db.ExecContext(ctx, "ALTER TABLE messages RENAME TO messages_old")
	db.ExecContext(ctx, "CREATE TABLE messages (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT, role TEXT, content TEXT, timestamp REAL)")
	db.ExecContext(ctx, "DROP TABLE messages_old")
	if err := reconcileColumns(ctx, db); err != nil {
		t.Fatalf("reconcileColumns failed: %v", err)
	}
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(messages)")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var cid int
		var name, colType string
		var notnull int
		var dflt sql.NullString
		var pk int
		rows.Scan(&cid, &name, &colType, &notnull, &dflt, &pk)
		if name == "tool_calls" {
			found = true
		}
	}
	if !found {
		t.Error("tool_calls column was not added by reconcileColumns")
	}
}



func TestEnsureFTS_CreatesBothTablesFromScratch(t *testing.T) {
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
	db.ExecContext(ctx, "INSERT INTO sessions (id, source, started_at) VALUES ('ftstest', 'test', 1.0)")
	db.ExecContext(ctx, "INSERT INTO messages (session_id, role, content, timestamp) VALUES ('ftstest', 'user', 'hello world', 1.0)")
	if err := ensureFTS(ctx, db); err != nil {
		t.Fatalf("ensureFTS failed: %v", err)
	}
	for _, tbl := range []string{"messages_fts", "messages_fts_trigram"} {
		var cnt int
		db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", tbl).Scan(&cnt)
		if cnt == 0 {
			t.Errorf("%s table was not created", tbl)
		}
	}
}



func TestEnsureFTS_BackfillPopulatesIndex(t *testing.T) {
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
	db.ExecContext(ctx, "INSERT INTO sessions (id, source, started_at) VALUES ('bf', 'test', 1.0)")
	db.ExecContext(ctx, "INSERT INTO messages (session_id, role, content, timestamp) VALUES ('bf', 'user', 'unique-search-term-xyz', 1.0)")
	if err := ensureFTS(ctx, db); err != nil {
		t.Fatalf("ensureFTS failed: %v", err)
	}
	results, err := store.SearchMessages(ctx, "unique-search-term-xyz", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Error("backfill did not populate FTS index")
	}
}



func TestRunMigrations_CreatesTitleIndex(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	var cnt int
	err = store.DB().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_sessions_title_unique'",
	).Scan(&cnt)
	if err != nil {
		t.Fatal(err)
	}
	if cnt != 1 {
		t.Errorf("expected title index to exist, count=%d", cnt)
	}
}




func TestMigrateV10_ExistingTableSkip(t *testing.T) {
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
	// Run v10 migration once
	if err := migrateV10(ctx, db); err != nil {
		t.Fatal(err)
	}
	// Run again - should detect existing table and skip
	if err := migrateV10(ctx, db); err != nil {
		t.Fatalf("second v10 migration should skip: %v", err)
	}
}



func TestRunVersionMigrations_FromVersion9(t *testing.T) {
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
	// from version 9 should trigger both v10 and v11
	if err := runVersionMigrations(ctx, db, 9); err != nil {
		t.Fatalf("runVersionMigrations from v9 failed: %v", err)
	}
	// Verify FTS tables exist
	for _, tbl := range []string{"messages_fts", "messages_fts_trigram"} {
		var cnt int
		db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", tbl).Scan(&cnt)
		if cnt == 0 {
			t.Errorf("%s table was not created", tbl)
		}
	}
}




func TestRunMigrations_SkipWhenCurrent(t *testing.T) {
	dir := t.TempDir() + "/test.db"
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	db := store.DB()
	// Run migrations once to set up schema
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	// Set version to current so version-gated migrations are skipped
	if err := setSchemaVersion(ctx, db, currentSchemaVersion); err != nil {
		t.Fatal(err)
	}
	// Running again should succeed without running version migrations
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatalf("RunMigrations on current version should not fail: %v", err)
	}
}



func TestRunVersionMigrations_FromVersion12(t *testing.T) {
	dir := t.TempDir() + "/test.db"
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	db := store.DB()
	if err := execSchemaStatements(ctx, db, schemaSQL); err != nil {
		t.Fatal(err)
	}
	// fromVersion >= 11 should be a no-op
	if err := runVersionMigrations(ctx, db, 12); err != nil {
		t.Fatalf("runVersionMigrations with fromVersion >= 11 should not fail: %v", err)
	}
}



func TestRunMigrations_FullPath(t *testing.T) {
	dir := t.TempDir() + "/test.db"
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	db := store.DB()
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatalf("RunMigrations failed: %v", err)
	}
	ver, err := getSchemaVersion(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if ver != currentSchemaVersion {
		t.Fatalf("expected version %d, got %d", currentSchemaVersion, ver)
	}
}



func TestRunVersionMigrations_V10Migration(t *testing.T) {
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
	// Insert a message to verify backfill
	store.CreateSession(ctx, &Session{ID: "v10-sess"})
	store.InsertMessage(ctx, &MessageRecord{SessionID: "v10-sess", Role: "user", Content: "hello world"})
	if err := setSchemaVersion(ctx, db, 9); err != nil {
		t.Fatal(err)
	}
	if err := runVersionMigrations(ctx, db, 10); err != nil {
		t.Fatalf("runVersionMigrations to v10 failed: %v", err)
	}
}



func TestRunVersionMigrations_V11Migration(t *testing.T) {
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
	store.CreateSession(ctx, &Session{ID: "v11-sess"})
	store.InsertMessage(ctx, &MessageRecord{SessionID: "v11-sess", Role: "user", Content: "test content"})
	if err := setSchemaVersion(ctx, db, 10); err != nil {
		t.Fatal(err)
	}
	if err := runVersionMigrations(ctx, db, 11); err != nil {
		t.Fatalf("runVersionMigrations to v11 failed: %v", err)
	}
}



func TestEnsureFTS_CreateNew(t *testing.T) {
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
	// Drop FTS tables so ensureFTS has to recreate them
	db.ExecContext(ctx, "DROP TABLE IF EXISTS messages_fts")
	db.ExecContext(ctx, "DROP TABLE IF EXISTS messages_fts_trigram")
	if err := ensureFTS(ctx, db); err != nil {
		t.Fatalf("ensureFTS failed: %v", err)
	}
}



func TestEnsureFTS_BackfillWithData(t *testing.T) {
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
	store.CreateSession(ctx, &Session{ID: "ftsf-sess"})
	store.InsertMessage(ctx, &MessageRecord{SessionID: "ftsf-sess", Role: "user", Content: "backfill me"})
	db.ExecContext(ctx, "DELETE FROM messages_fts")
	db.ExecContext(ctx, "DELETE FROM messages_fts_trigram")
	if err := ensureFTS(ctx, db); err != nil {
		t.Fatalf("ensureFTS backfill failed: %v", err)
	}
}



func TestEnsureFTS_EmptyDB(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	db := store.DB()
	// ensureFTS on fresh DB with no schema should still work
	if err := ensureFTS(ctx, db); err != nil {
		t.Fatalf("ensureFTS on empty DB failed: %v", err)
	}
}



func TestReconcileColumns_NoOp(t *testing.T) {
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
	if err := reconcileColumns(ctx, db); err != nil {
		t.Fatalf("reconcileColumns no-op failed: %v", err)
	}
}
