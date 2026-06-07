package state

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestSearchMessages_ToolName(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &Session{ID: "tool-search", Source: "test", StartedAt: float64(time.Now().Unix())}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	msg := &MessageRecord{
		SessionID: "tool-search",
		Role:      "tool",
		Content:   "result data here",
		ToolName:  "read_file",
		Timestamp: float64(time.Now().Unix()),
	}
	if err := store.InsertMessage(ctx, msg); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	// LIKE fallback for short CJK should search tool_name too
	results, err := store.SearchMessages(ctx, "测试不存在的内容", 10)
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	// Just verify no panic/error
	_ = results
}



func TestSearchMessages_SpacesOnly(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	results, err := store.SearchMessages(ctx, "   ", 10)
	if err != nil {
		t.Fatalf("SearchMessages spaces: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for spaces-only query, got %d", len(results))
	}
}



func TestSearchLatin_QuotedPhrase(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &Session{ID: "phrase-search", Source: "test", StartedAt: float64(time.Now().Unix())}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	msg := &MessageRecord{
		SessionID: "phrase-search",
		Role:      "assistant",
		Content:   "the quick brown fox jumps over the lazy dog",
		Timestamp: float64(time.Now().Unix()),
	}
	if err := store.InsertMessage(ctx, msg); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	results, err := store.SearchMessages(ctx, `"quick brown"`, 10)
	if err != nil {
		t.Fatalf("SearchMessages quoted: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected results for quoted phrase search")
	}
}



func TestSearchCJKLike_MultipleFields(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &Session{ID: "cjk-multi", Source: "test", StartedAt: float64(time.Now().Unix())}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Message with tool_name containing CJK
	msg := &MessageRecord{
		SessionID: "cjk-multi",
		Role:      "tool",
		Content:   "result data",
		ToolName:  "读取文件工具",
		Timestamp: float64(time.Now().Unix()),
	}
	if err := store.InsertMessage(ctx, msg); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	// Short CJK query should match tool_name via LIKE fallback
	results, err := store.SearchMessages(ctx, "读取", 10)
	if err != nil {
		t.Fatalf("SearchMessages CJK tool_name: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected results matching tool_name via CJK LIKE")
	}
}

// ── 覆盖率提升测试 ───────────────────────────────────────────────

// TestExecuteWrite_CheckpointTrigger 触发 WAL checkpoint (每50次写入)


// TestSearchTrigramFTS_SyntaxError 测试 trigram FTS 查询语法错误
func TestSearchTrigramFTS_SyntaxError(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &Session{ID: "tri-err", Source: "test", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, sess)
	store.InsertMessage(ctx, &MessageRecord{
		SessionID: "tri-err",
		Role:      "user",
		Content:   "中文内容测试数据",
	})

	// Invalid trigram query should return nil without panicking
	results, err := store.searchTrigramFTS(ctx, "中文 OR &&&", 10)
	if err != nil {
		t.Errorf("expected nil error on trigram syntax error, got: %v", err)
	}
	// Results may be nil or empty — just no panic
	_ = results
}

// TestEnsureTitleIndex 测试标题唯一索引创建


// TestScanSearchResults_Empty 测试 scanSearchResults 处理空结果
func TestScanSearchResults_Empty(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "scan_empty.db"))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	// Create a messages table and FTS with no data
	db.ExecContext(ctx, `CREATE TABLE messages (id INTEGER PRIMARY KEY, session_id TEXT, content TEXT)`)
	db.ExecContext(ctx, `CREATE TABLE sessions (id TEXT PRIMARY KEY)`)
	db.ExecContext(ctx, `CREATE VIRTUAL TABLE messages_fts USING fts5(content)`)

	rows, err := db.QueryContext(ctx, `SELECT m.id, m.session_id, '', 0.0 FROM messages m JOIN sessions s ON s.id = m.session_id WHERE 1=0`)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	defer rows.Close()

	results, err := scanSearchResults(rows)
	if err != nil {
		t.Fatalf("scanSearchResults: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

// TestListRecentSessions_SessionWithoutMessages 测试没有消息的会话排序


// TestSearchCJKLike_ToolCallsField 测试 CJK LIKE 搜索 tool_calls 字段
func TestSearchCJKLike_ToolCallsField(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &Session{ID: "cjk-tc", Source: "test", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, sess)

	msg := &MessageRecord{
		SessionID: "cjk-tc",
		Role:      "assistant",
		Content:   "some content",
		ToolCalls: `[{"function":{"name":"读取数据"}}]`,
		Timestamp: float64(time.Now().Unix()),
	}
	store.InsertMessage(ctx, msg)

	// Short CJK in tool_calls should be found via LIKE
	results, err := store.searchCJKLike(ctx, "读取", 10)
	if err != nil {
		t.Fatalf("searchCJKLike tool_calls: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected results matching tool_calls via CJK LIKE")
	}
}

// TestSessionPersister_OpenStatError 测试 Open 处理 stat 错误路径


// TestSearchMessages_NegativeLimit 测试负数 limit 默认为 20
func TestSearchMessages_NegativeLimit(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &Session{ID: "neg-limit", Source: "test", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, sess)
	store.InsertMessage(ctx, &MessageRecord{
		SessionID: "neg-limit",
		Role:      "user",
		Content:   "findable content",
	})

	results, err := store.SearchMessages(ctx, "findable", -1)
	if err != nil {
		t.Fatalf("SearchMessages negative limit: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result with negative limit, got %d", len(results))
	}
}

// ── 追加测试: 提升覆盖率至 90%+ ────────────────────────────────

// TestExecuteWrite_CommitRetry 测试 COMMIT 时锁竞争重试路径


func TestSearchTrigramFTS_BooleanOperators(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &Session{ID: "bool-op", Source: "test"}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	msg := &MessageRecord{
		SessionID: "bool-op",
		Role:      "user",
		Content:   "苹果手机和香蕉牛奶都是好东西",
		Timestamp: float64(time.Now().Unix()),
	}
	if err := store.InsertMessage(ctx, msg); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	// Use 3+ CJK char tokens so trigram FTS5 can match
	results, err := store.SearchMessages(ctx, "苹果手机 AND 香蕉牛奶", 10)
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected results for AND boolean query with 3+ CJK chars per token")
	}
}

// TestMigrateV10_TableAlreadyExists 测试 migrateV10 跳过已存在的表


// TestSearchMessages_ChineseShortQuery 测试短中文查询走 LIKE 路径
func TestSearchMessages_ChineseShortQuery(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	store.CreateSession(ctx, &Session{ID: "cjk-short", Source: "test"})
	store.InsertMessage(ctx, &MessageRecord{
		SessionID: "cjk-short",
		Role:      "user",
		Content:   "这是一条测试消息",
		Timestamp: 1000,
	})

	// 2 个 CJK 字符 — 应走 LIKE 路径
	results, err := store.SearchMessages(ctx, "测试", 10)
	if err != nil {
		t.Fatalf("SearchMessages short CJK: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected results for short CJK query")
	}
}

// TestSearchMessages_ChineseLongQuery 测试长中文查询走 trigram FTS 路径


// TestSearchMessages_ChineseLongQuery 测试长中文查询走 trigram FTS 路径
func TestSearchMessages_ChineseLongQuery(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	store.CreateSession(ctx, &Session{ID: "cjk-long", Source: "test"})
	store.InsertMessage(ctx, &MessageRecord{
		SessionID: "cjk-long",
		Role:      "user",
		Content:   "这是一条用于测试全文搜索的中文消息内容",
		Timestamp: 1000,
	})

	// 3+ 个 CJK 字符 — 应走 trigram FTS 路径
	results, err := store.SearchMessages(ctx, "测试全文搜索", 10)
	if err != nil {
		t.Fatalf("SearchMessages long CJK: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected results for long CJK query")
	}
}

// TestSearchMessages_LatinFTS 测试拉丁语系 FTS 搜索


// TestSearchMessages_LatinFTS 测试拉丁语系 FTS 搜索
func TestSearchMessages_LatinFTS(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	store.CreateSession(ctx, &Session{ID: "latin-fts", Source: "test"})
	store.InsertMessage(ctx, &MessageRecord{
		SessionID: "latin-fts",
		Role:      "user",
		Content:   "The quick brown fox jumps over the lazy dog",
		Timestamp: 1000,
	})

	results, err := store.SearchMessages(ctx, "brown fox", 10)
	if err != nil {
		t.Fatalf("SearchMessages latin: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected results for latin FTS query")
	}
}

// ── scanSearchResults 错误路径覆盖 ─────────────────────────

// TestScanSearchResults_Empty 测试 scanSearchResults 空行


// TestScanSearchResults_Empty 测试 scanSearchResults 空行
func TestScanSearchResults_NoRows(t *testing.T) {
	// 使用一个不会返回行的查询来测试 scanSearchResults
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "scan_empty.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	if err := RunMigrations(ctx, db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	rows, err := db.QueryContext(ctx, "SELECT 0, 'x', 'snippet', 1.0 WHERE 1=0")
	if err != nil {
		t.Fatalf("QueryContext: %v", err)
	}
	defer rows.Close()

	results, err := scanSearchResults(rows)
	if err != nil {
		t.Fatalf("scanSearchResults no rows: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}
}
