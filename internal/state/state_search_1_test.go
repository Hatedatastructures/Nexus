package state

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestSearchMessages_Latin(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &Session{ID: "search-latin", Source: "cli", Title: "Test Session", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, session)

	msg := &MessageRecord{
		SessionID: "search-latin",
		Role:      "user",
		Content:   "The quick brown fox jumps over the lazy dog",
	}
	store.InsertMessage(ctx, msg)

	results, err := store.SearchMessages(ctx, "quick brown", 10)
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].SessionID != "search-latin" {
		t.Errorf("SessionID = %q, want %q", results[0].SessionID, "search-latin")
	}
}



func TestSearchMessages_CJKTrigram(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &Session{ID: "search-cjk", Source: "cli", Title: "CJK Session", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, session)

	msg := &MessageRecord{
		SessionID: "search-cjk",
		Role:      "user",
		Content:   "这是一个测试消息，用于验证中文搜索功能是否正常工作",
	}
	store.InsertMessage(ctx, msg)

	results, err := store.SearchMessages(ctx, "验证中文搜索", 10)
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if len(results) < 1 {
		t.Fatalf("got %d CJK results, want at least 1", len(results))
	}
}



func TestSearchMessages_CJKShortLike(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &Session{ID: "search-cjk-short", Source: "cli", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, session)

	msg := &MessageRecord{
		SessionID: "search-cjk-short",
		Role:      "user",
		Content:   "你好世界测试",
	}
	store.InsertMessage(ctx, msg)

	// Single CJK char → LIKE fallback
	results, err := store.SearchMessages(ctx, "你", 10)
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if len(results) < 1 {
		t.Fatalf("got %d CJK short results, want at least 1", len(results))
	}
}



func TestSearchMessages_EmptyQuery(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	results, err := store.SearchMessages(ctx, "", 10)
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty query, got %d", len(results))
	}
}



func TestSearchMessages_NoResults(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &Session{ID: "search-none", Source: "cli", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, session)

	results, err := store.SearchMessages(ctx, "nonexistentquery", 10)
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

// ── ListRecentSessions 测试 ───────────────────────────────────



func TestCreateFTSTables(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Should succeed even when tables already exist (IF NOT EXISTS)
	if err := store.CreateFTSTables(ctx); err != nil {
		t.Fatalf("CreateFTSTables: %v", err)
	}
}

// ── SchemaSQL 测试 ────────────────────────────────────────────



func TestCreateFTSDefault(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "fts_default.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	ctx := context.Background()

	db.ExecContext(ctx, `CREATE TABLE messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT NOT NULL,
		role TEXT NOT NULL,
		content TEXT,
		tool_name TEXT,
		tool_calls TEXT
	)`)

	if err := createFTSDefault(ctx, db); err != nil {
		t.Fatalf("createFTSDefault: %v", err)
	}

	var count int
	db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='messages_fts'",
	).Scan(&count)
	if count != 1 {
		t.Error("messages_fts table should exist")
	}

	db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name='messages_fts_insert'",
	).Scan(&count)
	if count != 1 {
		t.Error("messages_fts_insert trigger should exist")
	}

	db.ExecContext(ctx, `INSERT INTO messages (session_id, role, content) VALUES ('s1', 'user', 'hello world')`)
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM messages_fts").Scan(&count)
	if count != 1 {
		t.Errorf("messages_fts should have 1 row after insert, got %d", count)
	}

	if err := createFTSDefault(ctx, db); err != nil {
		t.Fatalf("createFTSDefault idempotent: %v", err)
	}
}



func TestCreateFTSTrigram(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "fts_trigram.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	ctx := context.Background()

	db.ExecContext(ctx, `CREATE TABLE messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT NOT NULL,
		role TEXT NOT NULL,
		content TEXT,
		tool_name TEXT,
		tool_calls TEXT
	)`)

	if err := createFTSTrigram(ctx, db); err != nil {
		t.Fatalf("createFTSTrigram: %v", err)
	}

	var count int
	db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='messages_fts_trigram'",
	).Scan(&count)
	if count != 1 {
		t.Error("messages_fts_trigram table should exist")
	}

	db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name='messages_fts_trigram_insert'",
	).Scan(&count)
	if count != 1 {
		t.Error("messages_fts_trigram_insert trigger should exist")
	}

	db.ExecContext(ctx, `INSERT INTO messages (session_id, role, content) VALUES ('s1', 'user', '中文测试')`)
	db.QueryRowContext(ctx, "SELECT COUNT(*) FROM messages_fts_trigram").Scan(&count)
	if count != 1 {
		t.Errorf("messages_fts_trigram should have 1 row after insert, got %d", count)
	}

	if err := createFTSTrigram(ctx, db); err != nil {
		t.Fatalf("createFTSTrigram idempotent: %v", err)
	}
}

// ── execSchemaStatements 错误路径测试 ─────────────────────────



func TestSearchLatin_SyntaxError(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// This should trigger FTS5 syntax error path (returns nil, nil)
	results, err := store.searchLatin(ctx, "??? &&& !!!", 10)
	if err != nil {
		t.Errorf("expected nil error on FTS syntax error, got: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results on FTS syntax error, got %d results", len(results))
	}
}

// ── ListRecentSessions 测试 ─────────────────────────────────────



func TestSearchMessages_WhitespaceOnly(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	results, err := store.SearchMessages(ctx, "   \t\n  ", 10)
	if err != nil {
		t.Fatalf("SearchMessages whitespace: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for whitespace query, got %d", len(results))
	}
}



func TestSearchMessages_DefaultLimit(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Insert a session and a message
	store.CreateSession(ctx, &Session{ID: "search-lim", Source: "test", StartedAt: float64(time.Now().Unix())})
	store.InsertMessage(ctx, &MessageRecord{SessionID: "search-lim", Role: "user", Content: "findme"})

	results, err := store.SearchMessages(ctx, "findme", 0)
	if err != nil {
		t.Fatalf("SearchMessages default limit: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

// ── AutoPrune 测试 ──────────────────────────────────────────────



func TestSearchCJKLike_ShortQuery(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	store.CreateSession(ctx, &Session{ID: "cjk-short", Source: "test", StartedAt: float64(time.Now().Unix())})
	store.InsertMessage(ctx, &MessageRecord{SessionID: "cjk-short", Role: "user", Content: "这是一条中文消息"})

	results, err := store.searchCJKLike(ctx, "中文", 10)
	if err != nil {
		t.Fatalf("searchCJKLike: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result for CJK LIKE, got %d", len(results))
	}
}

// ── SessionPersister 测试 ───────────────────────────────────────



func TestSearchCJK_ShortFallback(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &Session{ID: "cjk-short", Source: "test", StartedAt: float64(time.Now().Unix())}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	msg := &MessageRecord{
		SessionID: "cjk-short",
		Role:      "assistant",
		Content:   "这是一个测试消息",
		Timestamp: float64(time.Now().Unix()),
	}
	if err := store.InsertMessage(ctx, msg); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	// 2 CJK chars should fall back to LIKE
	results, err := store.SearchMessages(ctx, "测试", 10)
	if err != nil {
		t.Fatalf("SearchMessages short CJK: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected results for short CJK query")
	}
}



func TestSearchCJK_Trigram(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &Session{ID: "cjk-tri", Source: "test", StartedAt: float64(time.Now().Unix())}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	msg := &MessageRecord{
		SessionID: "cjk-tri",
		Role:      "assistant",
		Content:   "全文搜索引擎支持中文检索功能",
		Timestamp: float64(time.Now().Unix()),
	}
	if err := store.InsertMessage(ctx, msg); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	// 3+ CJK chars should use trigram
	results, err := store.SearchMessages(ctx, "搜索引擎", 10)
	if err != nil {
		t.Fatalf("SearchMessages trigram CJK: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected results for trigram CJK query")
	}
}
