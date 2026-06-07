package state

import (
	"context"
	"database/sql"
	"testing"
)

func TestSetSchemaVersion_ClosedDB2(t *testing.T) {
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
	err = setSchemaVersion(ctx, db, 99)
	if err == nil {
		t.Fatal("expected error on closed DB")
	}
}



func TestMigrateV10_QueryErr(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", dir+"/test.db?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	if err := execSchemaStatements(ctx, db, SchemaSQL()); err != nil {
		t.Fatal(err)
	}
	if err := reconcileColumns(ctx, db); err != nil {
		t.Fatal(err)
	}
	err = migrateV10(ctx, db)
	if err != nil {
		t.Fatalf("migrateV10 should succeed on fresh DB: %v", err)
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='messages_fts_trigram'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatal("expected messages_fts_trigram table after v10 migration")
	}
	db.Close()
}



func TestEnsureFTS_Idempotent2(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", dir+"/test.db?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	if err := RunMigrations(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := ensureFTS(ctx, db); err != nil {
		t.Fatalf("ensureFTS should be idempotent: %v", err)
	}
}
