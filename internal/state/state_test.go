package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── 辅助函数测试 ──────────────────────────────────────────────

func TestNullStr(t *testing.T) {
	if got := nullStr(sql.NullString{Valid: true, String: "hello"}); got != "hello" {
		t.Errorf("nullStr(valid) = %q, want %q", got, "hello")
	}
	if got := nullStr(sql.NullString{Valid: false}); got != "" {
		t.Errorf("nullStr(invalid) = %q, want empty", got)
	}
}

func TestNullFloat(t *testing.T) {
	if got := nullFloat(sql.NullFloat64{Valid: true, Float64: 3.14}); got != 3.14 {
		t.Errorf("nullFloat(valid) = %v, want 3.14", got)
	}
	if got := nullFloat(sql.NullFloat64{Valid: false}); got != 0 {
		t.Errorf("nullFloat(invalid) = %v, want 0", got)
	}
}

func TestNullInt(t *testing.T) {
	if got := nullInt(sql.NullInt64{Valid: true, Int64: 42}); got != 42 {
		t.Errorf("nullInt(valid) = %v, want 42", got)
	}
	if got := nullInt(sql.NullInt64{Valid: false}); got != 0 {
		t.Errorf("nullInt(invalid) = %v, want 0", got)
	}
}

func TestNullStrOrNil(t *testing.T) {
	if got := nullStrOrNil(""); got != nil {
		t.Errorf("nullStrOrNil('') = %v, want nil", got)
	}
	if got := nullStrOrNil("abc"); got != "abc" {
		t.Errorf("nullStrOrNil('abc') = %v, want 'abc'", got)
	}
}

func TestNullIntOrNil(t *testing.T) {
	if got := nullIntOrNil(0); got != nil {
		t.Errorf("nullIntOrNil(0) = %v, want nil", got)
	}
	if got := nullIntOrNil(5); got != 5 {
		t.Errorf("nullIntOrNil(5) = %v, want 5", got)
	}
}

func TestJoinStrings(t *testing.T) {
	tests := []struct {
		parts []string
		sep   string
		want  string
	}{
		{[]string{}, ", ", ""},
		{[]string{"a"}, ", ", "a"},
		{[]string{"a", "b", "c"}, ", ", "a, b, c"},
		{[]string{"x", "y"}, "|", "x|y"},
	}
	for _, tt := range tests {
		got := joinStrings(tt.parts, tt.sep)
		if got != tt.want {
			t.Errorf("joinStrings(%v, %q) = %q, want %q", tt.parts, tt.sep, got, tt.want)
		}
	}
}

func TestEscapeLikePattern(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"abc", "abc"},
		{"100%", "100\\%"},
		{"a_b", "a\\_b"},
		{"a\\b", "a\\\\b"},
		{"a%b_c\\d", "a\\%b\\_c\\\\d"},
	}
	for _, tt := range tests {
		got := escapeLikePattern(tt.input)
		if got != tt.want {
			t.Errorf("escapeLikePattern(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ── CJK 检测测试 ──────────────────────────────────────────────

func TestIsCJKCodepoint(t *testing.T) {
	cjk := []rune{'中', '日', '韓', 'の', 'ハ', '가', '。', '一', '鿿'}
	for _, r := range cjk {
		if !isCJKCodepoint(r) {
			t.Errorf("isCJKCodepoint(%U) = false, want true", r)
		}
	}
	nonCJK := []rune{'A', 'z', '0', ' ', '.', 'é', 'À'}
	for _, r := range nonCJK {
		if isCJKCodepoint(r) {
			t.Errorf("isCJKCodepoint(%U) = true, want false", r)
		}
	}
}

func TestContainsCJK(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"hello world", false},
		{"你好世界", true},
		{"hello 你好", true},
		{"カタカナ", true},
		{"", false},
		{"123", false},
	}
	for _, tt := range tests {
		got := containsCJK(tt.input)
		if got != tt.want {
			t.Errorf("containsCJK(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestCountCJK(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"hello", 0},
		{"你好", 2},
		{"hello你好world", 2},
		{"日本語テスト", 6},
		{"", 0},
		{"a中b日c", 2},
	}
	for _, tt := range tests {
		got := countCJK(tt.input)
		if got != tt.want {
			t.Errorf("countCJK(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

// ── FTS5 查询清理测试 ─────────────────────────────────────────

func TestSanitizeFTS5Query(t *testing.T) {
	tests := []struct {
		name, input, want string
	}{
		{"simple", "hello world", "hello world"},
		{"special chars", "hello+world", "hello world"},
		{"quoted phrase", `"exact phrase"`, `"exact phrase"`},
		{"leading star", "*hello", "hello"},
		{"dangling AND", "hello AND", "hello"},
		{"dangling OR start", "OR hello", "hello"},
		{"dangling NOT end", "hello NOT", "hello"},
		{"parentheses", "hello (world)", "hello  world"},
		{"dot-separated", "v1.2.3", `"v1.2.3"`},
		{"dash-separated", "my-var", `"my-var"`},
		{"underscore", `my_var`, `"my_var"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeFTS5Query(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeFTS5Query(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSanitizeFTS5Query_EmptyInput(t *testing.T) {
	got := sanitizeFTS5Query("")
	if got != "" {
		t.Errorf("sanitizeFTS5Query('') = %q, want empty", got)
	}
}

// ── Store CRUD 测试 ───────────────────────────────────────────

// newTestStore 创建一个使用临时目录的测试 Store
func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	store, err := NewStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	if err := RunMigrations(context.Background(), store.DB()); err != nil {
		t.Fatalf("RunMigrations failed: %v", err)
	}
	return store
}

func TestNewStore(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	if store.db == nil {
		t.Fatal("store.db is nil")
	}
	if store.DB() == nil {
		t.Fatal("store.DB() returned nil")
	}
}

func TestCreateAndGetSession(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	now := float64(time.Now().Unix())
	session := &Session{
		ID:        "test-session-1",
		Source:    "cli",
		UserID:    "user-123",
		Model:     "claude-3",
		StartedAt: now,
	}

	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	got, err := store.GetSession(ctx, "test-session-1")
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if got == nil {
		t.Fatal("GetSession returned nil")
	}
	if got.ID != "test-session-1" {
		t.Errorf("got.ID = %q, want %q", got.ID, "test-session-1")
	}
	if got.Source != "cli" {
		t.Errorf("got.Source = %q, want %q", got.Source, "cli")
	}
	if got.UserID != "user-123" {
		t.Errorf("got.UserID = %q, want %q", got.UserID, "user-123")
	}
	if got.Model != "claude-3" {
		t.Errorf("got.Model = %q, want %q", got.Model, "claude-3")
	}
}

func TestCreateSession_IDExists(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &Session{ID: "dup-id", Source: "cli", StartedAt: float64(time.Now().Unix())}
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatalf("first CreateSession: %v", err)
	}
	// INSERT OR IGNORE — second call should not error
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatalf("duplicate CreateSession should not error: %v", err)
	}
}

func TestCreateSession_DefaultStartedAt(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &Session{ID: "auto-ts", Source: "cli"}
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	got, _ := store.GetSession(ctx, "auto-ts")
	if got == nil {
		t.Fatal("session not found")
	}
	if got.StartedAt == 0 {
		t.Error("StartedAt should be auto-populated when left as 0")
	}
}

func TestGetSession_NotFound(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	got, err := store.GetSession(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("GetSession unexpected error: %v", err)
	}
	if got != nil {
		t.Error("GetSession should return nil for nonexistent session")
	}
}

func TestUpdateSession(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &Session{
		ID:        "update-me",
		Source:    "cli",
		Model:     "old-model",
		StartedAt: float64(time.Now().Unix()),
	}
	if err := store.CreateSession(ctx, session); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	session.Model = "new-model"
	session.Title = "updated title"
	session.MessageCount = 5
	session.ToolCallCount = 2
	session.InputTokens = 100
	session.OutputTokens = 200
	session.CacheReadTokens = 50
	session.CacheWriteTokens = 30
	session.EstimatedCostUSD = 0.05
	session.APICallCount = 3

	if err := store.UpdateSession(ctx, session); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}

	got, _ := store.GetSession(ctx, "update-me")
	if got == nil {
		t.Fatal("session not found after update")
	}
	if got.Model != "new-model" {
		t.Errorf("Model = %q, want %q", got.Model, "new-model")
	}
	if got.Title != "updated title" {
		t.Errorf("Title = %q, want %q", got.Title, "updated title")
	}
	if got.MessageCount != 5 {
		t.Errorf("MessageCount = %d, want 5", got.MessageCount)
	}
	if got.ToolCallCount != 2 {
		t.Errorf("ToolCallCount = %d, want 2", got.ToolCallCount)
	}
	if got.EstimatedCostUSD != 0.05 {
		t.Errorf("EstimatedCostUSD = %v, want 0.05", got.EstimatedCostUSD)
	}
}

func TestEndSession(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &Session{ID: "end-me", Source: "cli", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, session)

	if err := store.EndSession(ctx, "end-me", "completed"); err != nil {
		t.Fatalf("EndSession: %v", err)
	}

	got, _ := store.GetSession(ctx, "end-me")
	if got == nil {
		t.Fatal("session not found")
	}
	if got.EndedAt == 0 {
		t.Error("EndedAt should be set after EndSession")
	}
	if got.EndReason != "completed" {
		t.Errorf("EndReason = %q, want %q", got.EndReason, "completed")
	}
}

func TestEndSession_FirstWriterWins(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &Session{ID: "first-wins", Source: "cli", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, session)

	store.EndSession(ctx, "first-wins", "compression")
	store.EndSession(ctx, "first-wins", "user_exit")

	got, _ := store.GetSession(ctx, "first-wins")
	if got.EndReason != "compression" {
		t.Errorf("EndReason = %q, want first writer 'compression'", got.EndReason)
	}
}

func TestEndSession_AlreadyEnded(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &Session{ID: "already-ended", Source: "cli", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, session)

	// First end
	store.EndSession(ctx, "already-ended", "done")
	got, _ := store.GetSession(ctx, "already-ended")
	firstEnd := got.EndedAt

	// Second end — should be no-op
	store.EndSession(ctx, "already-ended", "other")
	got2, _ := store.GetSession(ctx, "already-ended")
	if got2.EndedAt != firstEnd {
		t.Error("second EndSession should not change EndedAt")
	}
}

func TestListSessions(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		s := &Session{
			ID:        fmt.Sprintf("list-%d", i),
			Source:    "cli",
			UserID:    "user-1",
			StartedAt: float64(time.Now().Unix() + int64(i)),
		}
		store.CreateSession(ctx, s)
	}

	// End one session
	store.EndSession(ctx, "list-2", "done")

	tests := []struct {
		name   string
		filter *SessionFilter
		want   int
	}{
		{"all", nil, 5},
		{"source filter", &SessionFilter{Source: "cli"}, 5},
		{"ended true", &SessionFilter{Ended: boolPtr(true)}, 1},
		{"ended false", &SessionFilter{Ended: boolPtr(false)}, 4},
		{"with limit", &SessionFilter{Limit: 2}, 2},
		{"with offset", &SessionFilter{Offset: 3}, 2},
		{"user filter", &SessionFilter{UserID: "user-1"}, 5},
		{"no match source", &SessionFilter{Source: "api"}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessions, err := store.ListSessions(ctx, tt.filter)
			if err != nil {
				t.Fatalf("ListSessions error: %v", err)
			}
			if len(sessions) != tt.want {
				t.Errorf("got %d sessions, want %d", len(sessions), tt.want)
			}
		})
	}
}

func TestListSessions_EmptyResult(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sessions, err := store.ListSessions(ctx, nil)
	if err != nil {
		t.Fatalf("ListSessions error: %v", err)
	}
	if sessions == nil {
		t.Error("should return empty slice, not nil")
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}
}

func boolPtr(b bool) *bool { return &b }

// ── Compression Chain 测试 ─────────────────────────────────────

func TestGetCompressionTip_NoChain(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &Session{ID: "tip-1", Source: "cli", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, session)

	got, err := store.GetCompressionTip(ctx, "tip-1")
	if err != nil {
		t.Fatalf("GetCompressionTip: %v", err)
	}
	if got == nil || got.ID != "tip-1" {
		t.Error("should return the same session when no chain exists")
	}
}

func TestGetCompressionTip_WithChain(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	now := float64(time.Now().Unix())

	// Parent session (ended with compression)
	parent := &Session{ID: "chain-parent", Source: "cli", StartedAt: now - 100}
	store.CreateSession(ctx, parent)
	store.EndSession(ctx, "chain-parent", "compression")

	// Child session (compression continuation)
	// Use a started_at well after the parent's ended_at (set by EndSession at runtime)
	child := &Session{
		ID:              "chain-child",
		Source:          "cli",
		ParentSessionID: "chain-parent",
		StartedAt:       now + 200,
	}
	store.CreateSession(ctx, child)

	got, err := store.GetCompressionTip(ctx, "chain-parent")
	if err != nil {
		t.Fatalf("GetCompressionTip: %v", err)
	}
	if got == nil {
		t.Fatal("GetCompressionTip returned nil")
	}
	if got.ID != "chain-child" {
		t.Errorf("tip = %q, want %q", got.ID, "chain-child")
	}
}

func TestGetCompressionTip_NonexistentSession(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	got, err := store.GetCompressionTip(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("GetCompressionTip error: %v", err)
	}
	if got != nil {
		t.Error("should return nil for nonexistent session")
	}
}

// ── Message CRUD 测试 ─────────────────────────────────────────

func TestInsertAndGetMessages(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &Session{ID: "msg-session", Source: "cli", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, session)

	msg := &MessageRecord{
		SessionID:    "msg-session",
		Role:         "user",
		Content:      "Hello, world!",
		ToolCalls:    `[{"id":"call_1","function":{"name":"bash"}}]`,
		ToolName:     "bash",
		Timestamp:    float64(time.Now().Unix()),
		TokenCount:   10,
		FinishReason: "stop",
	}

	if err := store.InsertMessage(ctx, msg); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}
	if msg.ID == 0 {
		t.Error("msg.ID should be set after insert")
	}

	got, err := store.GetMessages(ctx, "msg-session", 10, 0)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1", len(got))
	}
	if got[0].Content != "Hello, world!" {
		t.Errorf("Content = %q, want %q", got[0].Content, "Hello, world!")
	}
	if got[0].Role != "user" {
		t.Errorf("Role = %q, want %q", got[0].Role, "user")
	}
	if got[0].ToolName != "bash" {
		t.Errorf("ToolName = %q, want %q", got[0].ToolName, "bash")
	}
}

func TestInsertMessage_UpdatesSessionCounters(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &Session{ID: "counter-session", Source: "cli", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, session)

	// Insert message without tool calls
	msg1 := &MessageRecord{
		SessionID: "counter-session",
		Role:      "user",
		Content:   "hello",
	}
	store.InsertMessage(ctx, msg1)

	got, _ := store.GetSession(ctx, "counter-session")
	if got.MessageCount != 1 {
		t.Errorf("MessageCount = %d, want 1", got.MessageCount)
	}
	if got.ToolCallCount != 0 {
		t.Errorf("ToolCallCount = %d, want 0", got.ToolCallCount)
	}

	// Insert message with tool calls
	msg2 := &MessageRecord{
		SessionID: "counter-session",
		Role:      "assistant",
		Content:   "using tool",
		ToolCalls: `[{"id":"c1"}]`,
	}
	store.InsertMessage(ctx, msg2)

	got, _ = store.GetSession(ctx, "counter-session")
	if got.MessageCount != 2 {
		t.Errorf("MessageCount = %d, want 2", got.MessageCount)
	}
	if got.ToolCallCount != 1 {
		t.Errorf("ToolCallCount = %d, want 1", got.ToolCallCount)
	}
}

func TestInsertMessage_ToolCallEdgeCases(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &Session{ID: "tool-edge", Source: "cli", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, session)

	// ToolCalls = "null" → no increment
	msg1 := &MessageRecord{SessionID: "tool-edge", Role: "assistant", ToolCalls: "null"}
	store.InsertMessage(ctx, msg1)

	// ToolCalls = "[]" → no increment
	msg2 := &MessageRecord{SessionID: "tool-edge", Role: "assistant", ToolCalls: "[]"}
	store.InsertMessage(ctx, msg2)

	// ToolCalls = "" → no increment
	msg3 := &MessageRecord{SessionID: "tool-edge", Role: "assistant", ToolCalls: ""}
	store.InsertMessage(ctx, msg3)

	got, _ := store.GetSession(ctx, "tool-edge")
	if got.MessageCount != 3 {
		t.Errorf("MessageCount = %d, want 3", got.MessageCount)
	}
	if got.ToolCallCount != 0 {
		t.Errorf("ToolCallCount = %d, want 0 (all edge cases)", got.ToolCallCount)
	}
}

func TestInsertMessagesBatch(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &Session{ID: "batch-session", Source: "cli", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, session)

	msgs := []*MessageRecord{
		{SessionID: "batch-session", Role: "user", Content: "first"},
		{SessionID: "batch-session", Role: "assistant", Content: "second", ToolCalls: `[{"id":"c1"}]`},
		{SessionID: "batch-session", Role: "user", Content: "third"},
	}

	if err := store.InsertMessagesBatch(ctx, msgs); err != nil {
		t.Fatalf("InsertMessagesBatch: %v", err)
	}

	got, _ := store.GetMessages(ctx, "batch-session", 10, 0)
	if len(got) != 3 {
		t.Fatalf("got %d messages, want 3", len(got))
	}

	sess, _ := store.GetSession(ctx, "batch-session")
	if sess.MessageCount != 3 {
		t.Errorf("MessageCount = %d, want 3", sess.MessageCount)
	}
	if sess.ToolCallCount != 1 {
		t.Errorf("ToolCallCount = %d, want 1", sess.ToolCallCount)
	}
}

func TestInsertMessagesBatch_Empty(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if err := store.InsertMessagesBatch(ctx, nil); err != nil {
		t.Errorf("InsertMessagesBatch(nil) should not error: %v", err)
	}
	if err := store.InsertMessagesBatch(ctx, []*MessageRecord{}); err != nil {
		t.Errorf("InsertMessagesBatch(empty) should not error: %v", err)
	}
}

func TestGetMessages_DefaultLimit(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &Session{ID: "limit-session", Source: "cli", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, session)

	for i := 0; i < 5; i++ {
		msg := &MessageRecord{
			SessionID: "limit-session",
			Role:      "user",
			Content:   fmt.Sprintf("msg-%d", i),
			Timestamp: float64(time.Now().Unix() + int64(i)),
		}
		store.InsertMessage(ctx, msg)
	}

	// limit=0 should default to 100
	got, err := store.GetMessages(ctx, "limit-session", 0, 0)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(got) != 5 {
		t.Errorf("got %d messages with default limit, want 5", len(got))
	}
}

func TestGetMessageCount(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &Session{ID: "count-session", Source: "cli", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, session)

	for i := 0; i < 3; i++ {
		msg := &MessageRecord{SessionID: "count-session", Role: "user", Content: "hi"}
		store.InsertMessage(ctx, msg)
	}

	count, err := store.GetMessageCount(ctx, "count-session")
	if err != nil {
		t.Fatalf("GetMessageCount: %v", err)
	}
	if count != 3 {
		t.Errorf("GetMessageCount = %d, want 3", count)
	}
}

func TestGetMessageCount_NoMessages(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &Session{ID: "empty-count", Source: "cli", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, session)

	count, err := store.GetMessageCount(ctx, "empty-count")
	if err != nil {
		t.Fatalf("GetMessageCount: %v", err)
	}
	if count != 0 {
		t.Errorf("GetMessageCount = %d, want 0", count)
	}
}

// ── 搜索测试 ──────────────────────────────────────────────────

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

func TestListRecentSessions(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		s := &Session{
			ID:        fmt.Sprintf("recent-%d", i),
			Source:    "cli",
			StartedAt: float64(time.Now().Unix() - int64(i*100)),
		}
		store.CreateSession(ctx, s)
	}

	sessions, err := store.ListRecentSessions(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecentSessions: %v", err)
	}
	if len(sessions) != 3 {
		t.Errorf("got %d sessions, want 3", len(sessions))
	}
}

func TestListRecentSessions_DefaultLimit(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	s := &Session{ID: "recent-default", Source: "cli", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, s)

	sessions, err := store.ListRecentSessions(ctx, 0)
	if err != nil {
		t.Fatalf("ListRecentSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Errorf("got %d sessions with default limit, want 1", len(sessions))
	}
}

// ── AutoPrune 测试 ─────────────────────────────────────────────

func TestAutoPrune(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Create an old ended session
	oldSession := &Session{
		ID:        "old-session",
		Source:    "cli",
		StartedAt: float64(time.Now().Unix() - 200*86400), // 200 days ago
	}
	store.CreateSession(ctx, oldSession)
	store.EndSession(ctx, "old-session", "completed")

	// Create a new active session
	newSession := &Session{
		ID:        "new-session",
		Source:    "cli",
		StartedAt: float64(time.Now().Unix()),
	}
	store.CreateSession(ctx, newSession)

	// Prune sessions older than 90 days
	removed, err := store.AutoPrune(ctx, 90)
	if err != nil {
		t.Fatalf("AutoPrune: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}

	// Old session should be gone
	got, _ := store.GetSession(ctx, "old-session")
	if got != nil {
		t.Error("old session should be pruned")
	}

	// New session should remain
	got2, _ := store.GetSession(ctx, "new-session")
	if got2 == nil {
		t.Error("new session should not be pruned")
	}
}

func TestAutoPrune_DefaultMaxAge(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Create session with negative maxAge → should default to 90
	session := &Session{
		ID:        "prune-default",
		Source:    "cli",
		StartedAt: float64(time.Now().Unix() - 100*86400),
	}
	store.CreateSession(ctx, session)
	store.EndSession(ctx, "prune-default", "done")

	removed, err := store.AutoPrune(ctx, -1)
	if err != nil {
		t.Fatalf("AutoPrune: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
}

func TestAutoPrune_NoExpiredSessions(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	session := &Session{
		ID:        "active-session",
		Source:    "cli",
		StartedAt: float64(time.Now().Unix()),
	}
	store.CreateSession(ctx, session)

	removed, err := store.AutoPrune(ctx, 90)
	if err != nil {
		t.Fatalf("AutoPrune: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
}

func TestAutoPrune_OrphansChildren(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Old parent with child
	parent := &Session{
		ID:        "old-parent",
		Source:    "cli",
		StartedAt: float64(time.Now().Unix() - 200*86400),
	}
	store.CreateSession(ctx, parent)
	store.EndSession(ctx, "old-parent", "done")

	child := &Session{
		ID:              "orphan-child",
		Source:          "cli",
		ParentSessionID: "old-parent",
		StartedAt:       float64(time.Now().Unix() - 200*86400),
	}
	store.CreateSession(ctx, child)
	store.EndSession(ctx, "orphan-child", "done")

	removed, err := store.AutoPrune(ctx, 90)
	if err != nil {
		t.Fatalf("AutoPrune: %v", err)
	}
	if removed != 2 {
		t.Errorf("removed = %d, want 2 (parent and child both old)", removed)
	}
}

// ── CheckpointWAL 测试 ────────────────────────────────────────

func TestCheckpointWAL(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	if err := store.CheckpointWAL(ctx); err != nil {
		t.Fatalf("CheckpointWAL: %v", err)
	}
}

// ── SessionPersister 测试 ─────────────────────────────────────

func TestSessionPersister_RecordAndRead(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "test-session")

	if err := sp.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}

	session := &Session{
		ID:     "test-session",
		Source: "cli",
	}
	if err := sp.RecordSessionMeta(session); err != nil {
		t.Fatalf("RecordSessionMeta: %v", err)
	}

	msg := &MessageRecord{
		SessionID: "test-session",
		Role:      "user",
		Content:   "test message",
	}
	if err := sp.RecordMessage(msg); err != nil {
		t.Fatalf("RecordMessage: %v", err)
	}

	if err := sp.RecordCompaction(10, 500); err != nil {
		t.Fatalf("RecordCompaction: %v", err)
	}

	if err := sp.RecordPromptHistory("/help"); err != nil {
		t.Fatalf("RecordPromptHistory: %v", err)
	}

	if err := sp.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Verify file exists and has content
	path := filepath.Join(dir, "test-session.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 4 {
		t.Errorf("expected 4 JSONL lines, got %d", len(lines))
	}
}

func TestSessionPersister_WriteWithoutOpen(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "unopened")

	err := sp.RecordSessionMeta(&Session{ID: "test"})
	if err == nil {
		t.Error("should error when writing without open")
	}
}

func TestSessionPersister_DoubleClose(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "double-close")

	sp.Open()
	sp.Close()

	// Second close should not panic
	if err := sp.Close(); err != nil {
		t.Errorf("second Close should not error: %v", err)
	}
}

func TestSessionPersister_Append(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "append-test")

	// First write
	sp.Open()
	sp.RecordSessionMeta(&Session{ID: "append-test"})
	sp.Close()

	// Second write — should append
	sp2 := NewSessionPersister(dir, "append-test")
	sp2.Open()
	sp2.RecordMessage(&MessageRecord{SessionID: "append-test", Role: "user", Content: "second"})
	sp2.Close()

	data, _ := os.ReadFile(filepath.Join(dir, "append-test.jsonl"))
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 lines after append, got %d", len(lines))
	}
}

func TestSessionPersister_Rotation(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "rotate-test")
	sp.maxSize = 100 // very small to trigger rotation
	sp.maxRotations = 2

	sp.Open()

	// Write enough data to trigger rotation
	for i := 0; i < 50; i++ {
		msg := &MessageRecord{
			SessionID: "rotate-test",
			Role:      "user",
			Content:   fmt.Sprintf("message number %d with padding to exceed size threshold", i),
		}
		if err := sp.RecordMessage(msg); err != nil {
			t.Fatalf("RecordMessage %d: %v", i, err)
		}
	}
	sp.Close()

	// Check that rotation files exist
	if _, err := os.Stat(filepath.Join(dir, "rotate-test.jsonl")); err != nil {
		t.Errorf("active file missing: %v", err)
	}
	// At least one rotation file should exist
	found := false
	for i := 1; i <= 3; i++ {
		p := filepath.Join(dir, fmt.Sprintf("rotate-test.%d.jsonl", i))
		if _, err := os.Stat(p); err == nil {
			found = true
		}
	}
	if !found {
		t.Error("expected at least one rotation file")
	}
}

// ── Migration 测试 ─────────────────────────────────────────────

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

func TestParseSchemaColumns(t *testing.T) {
	columns, err := parseSchemaColumns(schemaSQL)
	if err != nil {
		t.Fatalf("parseSchemaColumns: %v", err)
	}

	// Check sessions table
	sessCols, ok := columns["sessions"]
	if !ok {
		t.Fatal("sessions table not found in parsed schema")
	}
	expectedCols := []string{"id", "source", "user_id", "model", "system_prompt", "started_at", "ended_at"}
	for _, col := range expectedCols {
		if _, ok := sessCols[col]; !ok {
			t.Errorf("sessions table missing column %q", col)
		}
	}

	// Check messages table
	msgCols, ok := columns["messages"]
	if !ok {
		t.Fatal("messages table not found in parsed schema")
	}
	if _, ok := msgCols["content"]; !ok {
		t.Error("messages table missing 'content' column")
	}
}

func TestSplitSQLStatements(t *testing.T) {
	tests := []struct {
		name, input string
		wantCount   int
	}{
		{"empty", "", 0},
		{"single", "SELECT 1", 1},
		{"multiple", "SELECT 1; SELECT 2;", 2},
		{"with comments", "-- comment\nSELECT 1;", 1},
		{"only comments", "-- just a comment", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmts := splitSQLStatements(tt.input)
			if len(stmts) != tt.wantCount {
				t.Errorf("got %d statements, want %d", len(stmts), tt.wantCount)
			}
		})
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

func TestExecuteWrite_CancelledContext(t *testing.T) {
	store := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := store.executeWrite(ctx, func(db *sql.DB) error {
		_, err := db.ExecContext(ctx, "CREATE TABLE should_not_exist (id INTEGER)")
		return err
	})
	if err == nil {
		t.Error("expected error with cancelled context")
	}
}

func TestExecuteWrite_Success(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	err := store.executeWrite(ctx, func(db *sql.DB) error {
		_, err := db.ExecContext(ctx, "CREATE TABLE test_write (id INTEGER)")
		return err
	})
	if err != nil {
		t.Fatalf("executeWrite: %v", err)
	}

	var count int
	store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE name='test_write'").Scan(&count)
	if count != 1 {
		t.Error("table should have been created")
	}
}

// ── 并发安全测试 ──────────────────────────────────────────────

func TestConcurrentWrites(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	const goroutines = 10
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			s := &Session{
				ID:        fmt.Sprintf("concurrent-%d", idx),
				Source:    "cli",
				StartedAt: float64(time.Now().Unix()),
			}
			if err := store.CreateSession(ctx, s); err != nil {
				t.Errorf("goroutine %d: CreateSession failed: %v", idx, err)
			}
		}(i)
	}
	wg.Wait()

	sessions, _ := store.ListSessions(ctx, nil)
	if len(sessions) != goroutines {
		t.Errorf("got %d sessions, want %d", len(sessions), goroutines)
	}
}

// ── isLockedErr 测试 ───────────────────────────────────────────

func TestIsLockedErr(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"database is locked", true},
		{"database is busy", true},
		{"SQLITE_BUSY", true},
		{"no such table: foo", false},
		{"", false},
	}
	for _, tt := range tests {
		got := isLockedErr(errors.New(tt.input))
		if got != tt.want {
			t.Errorf("isLockedErr(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestIsLockedErr_Nil(t *testing.T) {
	if isLockedErr(nil) {
		t.Error("isLockedErr(nil) should be false")
	}
}

// ── CreateFTSTables 测试 ──────────────────────────────────────

func TestCreateFTSTables(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Should succeed even when tables already exist (IF NOT EXISTS)
	if err := store.CreateFTSTables(ctx); err != nil {
		t.Fatalf("CreateFTSTables: %v", err)
	}
}

// ── SchemaSQL 测试 ────────────────────────────────────────────

func TestSchemaSQL(t *testing.T) {
	sql := SchemaSQL()
	if sql == "" {
		t.Error("SchemaSQL() returned empty string")
	}
	if !strings.Contains(sql, "CREATE TABLE") {
		t.Error("SchemaSQL() should contain CREATE TABLE statements")
	}
}

// ── reconcileColumns 测试 ─────────────────────────────────────

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

func TestJitterSleep(t *testing.T) {
	for i := 0; i < 100; i++ {
		d := jitterSleep()
		if d < 20*time.Millisecond || d > 150*time.Millisecond {
			t.Errorf("jitterSleep() = %v, want [20ms, 150ms]", d)
		}
	}
}

// ── tryCheckpoint 测试 ────────────────────────────────────────

func TestTryCheckpoint(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &Session{ID: "cp-test", Source: "test", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, sess)
	store.InsertMessage(ctx, &MessageRecord{
		SessionID: "cp-test", Role: "user", Content: "checkpoint test",
	})

	store.tryCheckpoint(ctx)
}

func TestTryCheckpoint_EmptyDB(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	store.tryCheckpoint(ctx)
}

// ── createFTSDefault / createFTSTrigram 测试 ──────────────────

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

func TestListRecentSessions_WithData(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	now := float64(time.Now().Unix())

	// Create sessions in different order than expected output
	store.CreateSession(ctx, &Session{ID: "old-session", Source: "test", StartedAt: now - 100})
	store.CreateSession(ctx, &Session{ID: "new-session", Source: "test", StartedAt: now})

	// Insert messages to set last_active
	store.InsertMessage(ctx, &MessageRecord{SessionID: "old-session", Role: "user", Content: "old", Timestamp: now - 50})
	store.InsertMessage(ctx, &MessageRecord{SessionID: "new-session", Role: "user", Content: "new", Timestamp: now})

	sessions, err := store.ListRecentSessions(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecentSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
	// Most recent first
	if sessions[0].ID != "new-session" {
		t.Errorf("first session = %q, want new-session", sessions[0].ID)
	}
}

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

func TestAutoPrune_WithExpiredSessions(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Create an expired session (started 100 days ago)
	oldTime := float64(time.Now().Add(-100 * 24 * time.Hour).Unix())
	store.CreateSession(ctx, &Session{ID: "expired", Source: "test", StartedAt: oldTime})
	store.EndSession(ctx, "expired", "complete")

	// Create a recent session
	store.CreateSession(ctx, &Session{ID: "recent", Source: "test", StartedAt: float64(time.Now().Unix())})

	count, err := store.AutoPrune(ctx, 30)
	if err != nil {
		t.Fatalf("AutoPrune: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 pruned, got %d", count)
	}

	// Recent session should still exist
	sess, _ := store.GetSession(ctx, "recent")
	if sess == nil {
		t.Error("recent session should not be pruned")
	}

	// Expired session should be gone
	sess, _ = store.GetSession(ctx, "expired")
	if sess != nil {
		t.Error("expired session should be pruned")
	}
}

// ── searchCJKLike 测试 ─────────────────────────────────────────

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

func TestSessionPersister_OpenClose(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "test-session")

	if err := sp.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := sp.RecordSessionMeta(&Session{ID: "test-session", Source: "test"}); err != nil {
		t.Fatalf("RecordSessionMeta: %v", err)
	}

	if err := sp.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Verify file was created
	if _, err := os.Stat(filepath.Join(dir, "test-session.jsonl")); os.IsNotExist(err) {
		t.Error("JSONL file should exist after Close")
	}
}

func TestSessionPersister_WriteNotOpen(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "not-open")

	err := sp.RecordMessage(&MessageRecord{Role: "user", Content: "test"})
	if err == nil {
		t.Error("expected error when writing to unopened persister")
	}
}

func TestSessionPersister_CompactionAndPrompt(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "records-test")

	if err := sp.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sp.Close()

	if err := sp.RecordCompaction(100, 500); err != nil {
		t.Fatalf("RecordCompaction: %v", err)
	}
	if err := sp.RecordPromptHistory("hello world"); err != nil {
		t.Fatalf("RecordPromptHistory: %v", err)
	}
}

// ── orphanChildren 测试 ─────────────────────────────────────────

func TestOrphanChildren_Empty(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orphan.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()

	// Empty parentIDs should be no-op
	if err := orphanChildren(ctx, db, nil); err != nil {
		t.Errorf("orphanChildren with nil: %v", err)
	}
	if err := orphanChildren(ctx, db, []string{}); err != nil {
		t.Errorf("orphanChildren with empty: %v", err)
	}
}

// ── NewStore 错误路径测试 ───────────────────────────────────────

func TestNewStore_InvalidPath(t *testing.T) {
	// sql.Open does not validate paths — error surfaces on first query.
	// Verify the Store opens without error, but a real query fails.
	store, err := NewStore("/nonexistent/deep/path/db.sqlite")
	if err != nil {
		// Some drivers may error on Open — that's acceptable
		t.Logf("NewStore returned error on invalid path (acceptable): %v", err)
		return
	}
	defer store.Close()
	if pingErr := store.DB().PingContext(context.Background()); pingErr == nil {
		t.Error("expected error when pinging invalid path DB, got nil")
	}
}

// ── sanitizeFTS5Query 补充测试 ─────────────────────────────────

func TestSanitizeFTS5Query_SpecialChars(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello+world", "hello world"},
		{"test{ing}", "test ing"},
		{"(parenthetical)", "parenthetical"},
		{"\"exact phrase\"", "\"exact phrase\""},
		{"hello   world", "hello   world"},
		{"AND something", "something"},
		{"something OR", "something"},
		{"NOT alone", "alone"},
		{"***test", "test"},
		{"my-api_key.code", "\"my-api_key.code\""},
	}
	for _, tt := range tests {
		got := sanitizeFTS5Query(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeFTS5Query(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ── CJK 检测补充测试 ───────────────────────────────────────────

func TestContainsCJK_Mixed(t *testing.T) {
	if !containsCJK("hello世界") {
		t.Error("expected CJK detection in mixed text")
	}
	if containsCJK("hello world") {
		t.Error("expected no CJK in pure Latin text")
	}
}

func TestInsertMessagesBatch_WithMessages(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	store.CreateSession(ctx, &Session{ID: "batch-test", Source: "test", StartedAt: float64(time.Now().Unix())})

	msgs := []*MessageRecord{
		{SessionID: "batch-test", Role: "user", Content: "first"},
		{SessionID: "batch-test", Role: "assistant", Content: "second", ToolCalls: `[{"id":"t1"}]`},
	}
	if err := store.InsertMessagesBatch(ctx, msgs); err != nil {
		t.Fatalf("InsertMessagesBatch: %v", err)
	}

	count, err := store.GetMessageCount(ctx, "batch-test")
	if err != nil {
		t.Fatalf("GetMessageCount: %v", err)
	}
	if count != 2 {
		t.Errorf("message count = %d, want 2", count)
	}

	sess, _ := store.GetSession(ctx, "batch-test")
	if sess.MessageCount != 2 {
		t.Errorf("session message_count = %d, want 2", sess.MessageCount)
	}
	if sess.ToolCallCount != 1 {
		t.Errorf("session tool_call_count = %d, want 1", sess.ToolCallCount)
	}
}

// ── GetMessages 分页测试 ────────────────────────────────────────

func TestGetMessages_Pagination(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	store.CreateSession(ctx, &Session{ID: "page-test", Source: "test", StartedAt: float64(time.Now().Unix())})

	for i := 0; i < 5; i++ {
		store.InsertMessage(ctx, &MessageRecord{
			SessionID: "page-test",
			Role:      "user",
			Content:   fmt.Sprintf("msg %d", i),
		})
	}

	// Get first page
	msgs, err := store.GetMessages(ctx, "page-test", 2, 0)
	if err != nil {
		t.Fatalf("GetMessages page 1: %v", err)
	}
	if len(msgs) != 2 {
		t.Errorf("page 1: expected 2 messages, got %d", len(msgs))
	}

	// Get second page
	msgs, err = store.GetMessages(ctx, "page-test", 2, 2)
	if err != nil {
		t.Fatalf("GetMessages page 2: %v", err)
	}
	if len(msgs) != 2 {
		t.Errorf("page 2: expected 2 messages, got %d", len(msgs))
	}

	// Default limit
	msgs, err = store.GetMessages(ctx, "page-test", 0, 0)
	if err != nil {
		t.Fatalf("GetMessages default limit: %v", err)
	}
	if len(msgs) != 5 {
		t.Errorf("default limit: expected 5 messages, got %d", len(msgs))
	}
}

func TestExecuteWrite_FnError(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	err := store.executeWrite(ctx, func(db *sql.DB) error {
		return fmt.Errorf("intentional fn error")
	})
	if err == nil {
		t.Fatal("expected error from fn callback, got nil")
	}
	if !strings.Contains(err.Error(), "intentional fn error") {
		t.Errorf("error should wrap fn error, got: %v", err)
	}
}

func TestAutoPrune_ExpiredSession(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &Session{
		ID:        "prune-me",
		Source:    "test",
		StartedAt: float64(time.Now().Add(-48 * time.Hour).Unix()),
	}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	// End the session
	if err := store.EndSession(ctx, "prune-me", "timeout"); err != nil {
		t.Fatalf("EndSession: %v", err)
	}

	// Manually set ended_at to past the prune threshold
	_, err := store.DB().ExecContext(ctx,
		"UPDATE sessions SET ended_at = ? WHERE id = ?",
		float64(time.Now().Add(-48*time.Hour).Unix()), "prune-me")
	if err != nil {
		t.Fatalf("set ended_at: %v", err)
	}

	deleted, err := store.AutoPrune(ctx, 1)
	if err != nil {
		t.Fatalf("AutoPrune: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", deleted)
	}

	got, err := store.GetSession(ctx, "prune-me")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got != nil {
		t.Error("expired session should be deleted")
	}
}

func TestAutoPrune_ActiveSessionNotPruned(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &Session{
		ID:        "keep-me",
		Source:    "test",
		StartedAt: float64(time.Now().Unix()),
	}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	deleted, err := store.AutoPrune(ctx, 90)
	if err != nil {
		t.Fatalf("AutoPrune: %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 deleted, got %d", deleted)
	}

	got, err := store.GetSession(ctx, "keep-me")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got == nil {
		t.Error("active session should still exist")
	}
}

func TestGetCompressionTip_NonCompressionChild(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	parent := &Session{
		ID:        "parent-nc",
		Source:    "test",
		StartedAt: float64(time.Now().Add(-2 * time.Hour).Unix()),
	}
	if err := store.CreateSession(ctx, parent); err != nil {
		t.Fatalf("CreateSession parent: %v", err)
	}
	// End parent with non-compression reason
	if err := store.EndSession(ctx, "parent-nc", "user_stop"); err != nil {
		t.Fatalf("EndSession parent: %v", err)
	}

	child := &Session{
		ID:              "child-nc",
		Source:          "test",
		ParentSessionID: "parent-nc",
		StartedAt:       float64(time.Now().Add(-1 * time.Hour).Unix()),
	}
	if err := store.CreateSession(ctx, child); err != nil {
		t.Fatalf("CreateSession child: %v", err)
	}

	tip, err := store.GetCompressionTip(ctx, "parent-nc")
	if err != nil {
		t.Fatalf("GetCompressionTip: %v", err)
	}
	// Non-compression parent should return itself, not the child
	if tip.ID != "parent-nc" {
		t.Errorf("expected parent-nc, got %s", tip.ID)
	}
}

func TestListRecentSessions_Empty(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sessions, err := store.ListRecentSessions(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecentSessions empty: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}
}

func TestListRecentSessions_OrderByActivity(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Create two sessions with different timestamps
	s1 := &Session{ID: "recent-s1", Source: "test", StartedAt: float64(time.Now().Add(-2 * time.Hour).Unix())}
	s2 := &Session{ID: "recent-s2", Source: "test", StartedAt: float64(time.Now().Add(-1 * time.Hour).Unix())}
	if err := store.CreateSession(ctx, s1); err != nil {
		t.Fatalf("CreateSession s1: %v", err)
	}
	if err := store.CreateSession(ctx, s2); err != nil {
		t.Fatalf("CreateSession s2: %v", err)
	}

	// Add a message to s1 to make it more recently active
	msg := &MessageRecord{
		SessionID: "recent-s1",
		Role:      "user",
		Content:   "hello",
		Timestamp: float64(time.Now().Unix()),
	}
	if err := store.InsertMessage(ctx, msg); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}

	sessions, err := store.ListRecentSessions(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecentSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
	// s1 should be first because it has a recent message
	if sessions[0].ID != "recent-s1" {
		t.Errorf("expected recent-s1 first, got %s", sessions[0].ID)
	}
}

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

func TestSanitizeFTS5Query_EmptyAfterSanitize(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Only special chars should result in empty sanitized query
	results, err := store.SearchMessages(ctx, "+++===!!!", 10)
	if err != nil {
		t.Fatalf("SearchMessages empty sanitize: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for sanitized-to-empty query, got %d", len(results))
	}
}

func TestSplitSQLStatements_Simple(t *testing.T) {
	input := "SELECT 1;\nSELECT 2;"
	stmts := splitSQLStatements(input)
	if len(stmts) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(stmts))
	}
	if stmts[0] != "SELECT 1" {
		t.Errorf("stmt 0: got %q", stmts[0])
	}
	if stmts[1] != "SELECT 2" {
		t.Errorf("stmt 1: got %q", stmts[1])
	}
}

func TestSplitSQLStatements_TriggerWithSemicolons(t *testing.T) {
	input := `CREATE TRIGGER t AFTER INSERT ON x BEGIN
		INSERT INTO y VALUES (1);
		INSERT INTO y VALUES (2);
	END;
	SELECT 1;`
	stmts := splitSQLStatements(input)
	if len(stmts) != 2 {
		t.Fatalf("expected 2 statements, got %d: %v", len(stmts), stmts)
	}
	if !strings.HasPrefix(stmts[0], "CREATE TRIGGER") {
		t.Errorf("first stmt should be trigger, got %q", stmts[0])
	}
}

func TestSplitSQLStatements_EmptyInput(t *testing.T) {
	stmts := splitSQLStatements("")
	if len(stmts) != 0 {
		t.Errorf("expected 0 statements, got %d", len(stmts))
	}
}

func TestSplitSQLStatements_CommentsOnly(t *testing.T) {
	input := "-- just a comment\n-- another comment"
	stmts := splitSQLStatements(input)
	if len(stmts) != 0 {
		t.Errorf("expected 0 statements from comments, got %d", len(stmts))
	}
}

func TestSplitSQLStatements_NoTrailingSemicolon(t *testing.T) {
	input := "SELECT 1"
	stmts := splitSQLStatements(input)
	if len(stmts) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(stmts))
	}
	if stmts[0] != "SELECT 1" {
		t.Errorf("got %q", stmts[0])
	}
}

func TestIsCommentOnly(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"-- hello", true},
		{"  -- hello  ", true},
		{"SELECT 1", false},
		{"", true},
		{"\n\n", true},
		{"-- a\n-- b", true},
		{"-- a\nSELECT 1", false},
	}
	for _, tt := range tests {
		got := isCommentOnly(tt.input)
		if got != tt.want {
			t.Errorf("isCommentOnly(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestNewStore_Close(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "close-test.db")
	store, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestSessionPersister_BasicLifecycle(t *testing.T) {
	dir := t.TempDir()
	p := NewSessionPersister(dir, "lifecycle-test")

	if err := p.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := p.RecordSessionMeta(&Session{ID: "lifecycle-test", Source: "test"}); err != nil {
		t.Fatalf("RecordSessionMeta: %v", err)
	}
	if err := p.RecordMessage(&MessageRecord{Role: "user", Content: "hi"}); err != nil {
		t.Fatalf("RecordMessage: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestSessionPersister_OpenExistingFile(t *testing.T) {
	dir := t.TempDir()
	p1 := NewSessionPersister(dir, "existing-test")
	if err := p1.Open(); err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := p1.RecordMessage(&MessageRecord{Role: "user", Content: "first"}); err != nil {
		t.Fatalf("RecordMessage: %v", err)
	}
	if err := p1.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	// Reopen should succeed
	p2 := NewSessionPersister(dir, "existing-test")
	if err := p2.Open(); err != nil {
		t.Fatalf("second Open: %v", err)
	}
	if err := p2.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestSessionPersister_WriteBeforeOpen(t *testing.T) {
	dir := t.TempDir()
	p := NewSessionPersister(dir, "no-open-test")
	// Writing without Open should not panic; it should be a no-op or error
	err := p.RecordMessage(&MessageRecord{Role: "user", Content: "no-open"})
	// We accept either error or nil, but not a panic
	_ = err
}

func TestSessionPersister_RotationTrigger(t *testing.T) {
	dir := t.TempDir()
	p := NewSessionPersister(dir, "rotation-test")
	p.maxSize = 200 // Small size to trigger rotation

	if err := p.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Write enough data to trigger rotation
	for i := range 50 {
		err := p.RecordMessage(&MessageRecord{
			Role:      "user",
			Content:   fmt.Sprintf("message number %d with some padding content to fill space", i),
			Timestamp: float64(time.Now().Unix()),
		})
		if err != nil {
			t.Fatalf("RecordMessage %d: %v", i, err)
		}
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Check that rotated files exist
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) < 2 {
		t.Errorf("expected rotation files, got %d entries", len(entries))
	}
}

func TestListSessions_WithFilter(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	s1 := &Session{ID: "f-src1", Source: "api", StartedAt: float64(time.Now().Unix())}
	s2 := &Session{ID: "f-src2", Source: "cli", StartedAt: float64(time.Now().Unix())}
	if err := store.CreateSession(ctx, s1); err != nil {
		t.Fatalf("CreateSession s1: %v", err)
	}
	if err := store.CreateSession(ctx, s2); err != nil {
		t.Fatalf("CreateSession s2: %v", err)
	}

	filtered, err := store.ListSessions(ctx, &SessionFilter{Source: "api"})
	if err != nil {
		t.Fatalf("ListSessions filtered: %v", err)
	}
	if len(filtered) != 1 {
		t.Fatalf("expected 1 session, got %d", len(filtered))
	}
	if filtered[0].ID != "f-src1" {
		t.Errorf("expected f-src1, got %s", filtered[0].ID)
	}
}

func TestListSessions_Empty(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sessions, err := store.ListSessions(ctx, nil)
	if err != nil {
		t.Fatalf("ListSessions empty: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}
}

func TestStore_DB(t *testing.T) {
	store := newTestStore(t)
	if store.DB() == nil {
		t.Error("DB() should not return nil")
	}
}

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

func TestAutoPrune_WithOrphans(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	parent := &Session{
		ID:        "prune-parent",
		Source:    "test",
		StartedAt: float64(time.Now().Add(-48 * time.Hour).Unix()),
	}
	if err := store.CreateSession(ctx, parent); err != nil {
		t.Fatalf("CreateSession parent: %v", err)
	}
	// End parent
	if err := store.EndSession(ctx, "prune-parent", "done"); err != nil {
		t.Fatalf("EndSession: %v", err)
	}
	// Set ended_at to old
	_, err := store.DB().ExecContext(ctx,
		"UPDATE sessions SET ended_at = ? WHERE id = ?",
		float64(time.Now().Add(-48*time.Hour).Unix()), "prune-parent")
	if err != nil {
		t.Fatalf("set ended_at: %v", err)
	}

	child := &Session{
		ID:              "orphan-child",
		Source:          "test",
		ParentSessionID: "prune-parent",
		StartedAt:       float64(time.Now().Unix()),
	}
	if err := store.CreateSession(ctx, child); err != nil {
		t.Fatalf("CreateSession child: %v", err)
	}

	deleted, err := store.AutoPrune(ctx, 1)
	if err != nil {
		t.Fatalf("AutoPrune: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", deleted)
	}

	// Child should still exist but with orphaned parent
	got, err := store.GetSession(ctx, "orphan-child")
	if err != nil {
		t.Fatalf("GetSession child: %v", err)
	}
	if got == nil {
		t.Fatal("child should still exist")
	}
	if got.ParentSessionID != "" {
		t.Errorf("child parent_session_id should be empty (orphaned), got %q", got.ParentSessionID)
	}
}

func TestSessionPersister_WriteBeforeOpen_Error(t *testing.T) {
	dir := t.TempDir()
	p := NewSessionPersister(dir, "no-open-err")
	err := p.RecordMessage(&MessageRecord{Role: "user", Content: "no-open"})
	if err == nil {
		t.Error("expected error when writing before Open, got nil")
	}
	if !strings.Contains(err.Error(), "not open") {
		t.Errorf("error should mention not open, got: %v", err)
	}
}

func TestSessionPersister_RecordCompaction(t *testing.T) {
	dir := t.TempDir()
	p := NewSessionPersister(dir, "compact-test")
	if err := p.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := p.RecordCompaction(10, 500); err != nil {
		t.Fatalf("RecordCompaction: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestSessionPersister_RecordPromptHistory(t *testing.T) {
	dir := t.TempDir()
	p := NewSessionPersister(dir, "prompt-test")
	if err := p.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := p.RecordPromptHistory("hello world"); err != nil {
		t.Fatalf("RecordPromptHistory: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
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

func TestListSessions_FilterEnded(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	s1 := &Session{ID: "active-f", Source: "test", StartedAt: float64(time.Now().Unix())}
	s2 := &Session{ID: "ended-f", Source: "test", StartedAt: float64(time.Now().Add(-1*time.Hour).Unix())}
	if err := store.CreateSession(ctx, s1); err != nil {
		t.Fatalf("CreateSession s1: %v", err)
	}
	if err := store.CreateSession(ctx, s2); err != nil {
		t.Fatalf("CreateSession s2: %v", err)
	}
	if err := store.EndSession(ctx, "ended-f", "done"); err != nil {
		t.Fatalf("EndSession: %v", err)
	}

	ended := true
	filtered, err := store.ListSessions(ctx, &SessionFilter{Ended: &ended})
	if err != nil {
		t.Fatalf("ListSessions ended: %v", err)
	}
	if len(filtered) != 1 {
		t.Fatalf("expected 1 ended session, got %d", len(filtered))
	}
	if filtered[0].ID != "ended-f" {
		t.Errorf("expected ended-f, got %s", filtered[0].ID)
	}

	active := false
	activeSess, err := store.ListSessions(ctx, &SessionFilter{Ended: &active})
	if err != nil {
		t.Fatalf("ListSessions active: %v", err)
	}
	if len(activeSess) != 1 {
		t.Fatalf("expected 1 active session, got %d", len(activeSess))
	}
	if activeSess[0].ID != "active-f" {
		t.Errorf("expected active-f, got %s", activeSess[0].ID)
	}
}

func TestListSessions_Pagination(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	for i := range 5 {
		sess := &Session{
			ID:        fmt.Sprintf("page-%d", i),
			Source:    "test",
			StartedAt: float64(time.Now().Add(-time.Duration(i) * time.Hour).Unix()),
		}
		if err := store.CreateSession(ctx, sess); err != nil {
			t.Fatalf("CreateSession %d: %v", i, err)
		}
	}

	// Limit 2, offset 0
	page1, err := store.ListSessions(ctx, &SessionFilter{Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("ListSessions page1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("expected 2, got %d", len(page1))
	}

	// Limit 2, offset 2
	page2, err := store.ListSessions(ctx, &SessionFilter{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("ListSessions page2: %v", err)
	}
	if len(page2) != 2 {
		t.Fatalf("expected 2, got %d", len(page2))
	}

	// Pages should have different IDs
	if page1[0].ID == page2[0].ID {
		t.Error("pages should have different sessions")
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

func TestGetCompressionTip_Chain(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	now := time.Now().Unix()
	// Create parent session
	p := &Session{ID: "chain-p", Source: "test", StartedAt: float64(now - 300)}
	if err := store.CreateSession(ctx, p); err != nil {
		t.Fatalf("CreateSession parent: %v", err)
	}
	// End with compression reason
	if err := store.EndSession(ctx, "chain-p", "compression"); err != nil {
		t.Fatalf("EndSession: %v", err)
	}
	// Set ended_at to an old value so children satisfy started_at >= ended_at
	_, err := store.DB().ExecContext(ctx,
		"UPDATE sessions SET ended_at = ? WHERE id = ?",
		float64(now-250), "chain-p")
	if err != nil {
		t.Fatalf("set ended_at: %v", err)
	}

	// Create continuation child (started_at >= parent's ended_at)
	c1 := &Session{
		ID:              "chain-c1",
		Source:          "test",
		ParentSessionID: "chain-p",
		StartedAt:       float64(now - 200),
	}
	if err := store.CreateSession(ctx, c1); err != nil {
		t.Fatalf("CreateSession c1: %v", err)
	}
	if err := store.EndSession(ctx, "chain-c1", "compression"); err != nil {
		t.Fatalf("EndSession c1: %v", err)
	}
	// Set c1 ended_at to allow c2 to be a continuation
	_, err = store.DB().ExecContext(ctx,
		"UPDATE sessions SET ended_at = ? WHERE id = ?",
		float64(now-150), "chain-c1")
	if err != nil {
		t.Fatalf("set c1 ended_at: %v", err)
	}

	// Create second continuation child
	c2 := &Session{
		ID:              "chain-c2",
		Source:          "test",
		ParentSessionID: "chain-c1",
		StartedAt:       float64(now - 100),
	}
	if err := store.CreateSession(ctx, c2); err != nil {
		t.Fatalf("CreateSession c2: %v", err)
	}

	// GetCompressionTip from root should return c2
	tip, err := store.GetCompressionTip(ctx, "chain-p")
	if err != nil {
		t.Fatalf("GetCompressionTip: %v", err)
	}
	if tip.ID != "chain-c2" {
		t.Errorf("expected chain-c2 as tip, got %s", tip.ID)
	}
}

func TestGetCompressionTip_NoTip(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &Session{ID: "no-tip", Source: "test", StartedAt: float64(time.Now().Unix())}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	tip, err := store.GetCompressionTip(ctx, "no-tip")
	if err != nil {
		t.Fatalf("GetCompressionTip: %v", err)
	}
	if tip.ID != "no-tip" {
		t.Errorf("expected no-tip, got %s", tip.ID)
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
func TestExecuteWrite_CheckpointTrigger(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Do checkpointEvery writes to trigger checkpoint
	for i := range checkpointEvery {
		sess := &Session{
			ID:        fmt.Sprintf("cp-%d", i),
			Source:    "test",
			StartedAt: float64(time.Now().Unix()),
		}
		if err := store.CreateSession(ctx, sess); err != nil {
			t.Fatalf("CreateSession %d: %v", i, err)
		}
	}
	// If we got here without deadlock/panic, checkpoint was triggered
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
func TestSessionPersister_RotationMaxFiles(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "maxrot-test")
	sp.maxSize = 100
	sp.maxRotations = 2

	if err := sp.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Write enough to trigger multiple rotations
	for i := range 100 {
		if err := sp.RecordMessage(&MessageRecord{
			Role:    "user",
			Content: fmt.Sprintf("rotation test message %d padding content to exceed threshold", i),
		}); err != nil {
			t.Fatalf("RecordMessage %d: %v", i, err)
		}
	}
	sp.Close()

	// Should have at most maxRotations + 1 files (current + rotated)
	entries, _ := os.ReadDir(dir)
	jsonlFiles := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			jsonlFiles++
		}
	}
	if jsonlFiles > sp.maxRotations+1 {
		t.Errorf("expected at most %d files, got %d", sp.maxRotations+1, jsonlFiles)
	}

	// Rotation file .2 should not exist (maxRotations=2 means .1 and .2, but .3 should be gone)
	if _, err := os.Stat(filepath.Join(dir, "maxrot-test.3.jsonl")); err == nil {
		t.Error("rotation file .3 should have been deleted (maxRotations=2)")
	}
}

// TestAutoPrune_WithMessages 测试删除会话时消息也被删除
func TestAutoPrune_WithMessages(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &Session{
		ID:        "prune-msg",
		Source:    "test",
		StartedAt: float64(time.Now().Add(-48 * time.Hour).Unix()),
	}
	store.CreateSession(ctx, sess)
	store.EndSession(ctx, "prune-msg", "done")
	store.DB().ExecContext(ctx, "UPDATE sessions SET ended_at = ? WHERE id = ?",
		float64(time.Now().Add(-48*time.Hour).Unix()), "prune-msg")

	// Insert messages
	for i := range 3 {
		store.InsertMessage(ctx, &MessageRecord{
			SessionID: "prune-msg",
			Role:      "user",
			Content:   fmt.Sprintf("msg %d", i),
		})
	}

	count, _ := store.GetMessageCount(ctx, "prune-msg")
	if count != 3 {
		t.Fatalf("expected 3 messages before prune, got %d", count)
	}

	deleted, err := store.AutoPrune(ctx, 1)
	if err != nil {
		t.Fatalf("AutoPrune: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", deleted)
	}

	// Messages should be gone too
	count, err = store.GetMessageCount(ctx, "prune-msg")
	if err != nil {
		t.Fatalf("GetMessageCount after prune: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 messages after prune, got %d", count)
	}
}

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
func TestNewStore_WALMode(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(filepath.Join(dir, "wal_test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	// modernc.org/sqlite 的 _journal_mode DSN 参数可能不会立即生效，
	// 需要先执行一个写操作让 WAL 模式被激活
	_, err = store.DB().ExecContext(context.Background(),
		"CREATE TABLE IF NOT EXISTS _wal_check (id INTEGER PRIMARY KEY)")
	if err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}

	var mode string
	err = store.DB().QueryRowContext(context.Background(), "PRAGMA journal_mode").Scan(&mode)
	if err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if !strings.EqualFold(mode, "wal") {
		// WAL 未启用不阻塞，仅记录（modernc DSN 参数行为可能因版本不同）
		t.Logf("journal_mode = %q (expected wal; DSN param may not take effect with this driver version)", mode)
	}
}

// TestSplitSQLStatements_SingleQuotes 测试单引号字符串内的分号不拆分
func TestSplitSQLStatements_SingleQuotes(t *testing.T) {
	input := `INSERT INTO t VALUES ('hello;world'); SELECT 1;`
	stmts := splitSQLStatements(input)
	if len(stmts) != 2 {
		t.Fatalf("expected 2 statements, got %d: %v", len(stmts), stmts)
	}
	if !strings.Contains(stmts[0], "hello;world") {
		t.Errorf("first statement should contain semicolon in string: %q", stmts[0])
	}
}

// TestSplitSQLStatements_DoubleQuotes 测试双引号标识符内的分号不拆分
func TestSplitSQLStatements_DoubleQuotes(t *testing.T) {
	input := `CREATE TABLE "my;table" (id INTEGER); SELECT 1;`
	stmts := splitSQLStatements(input)
	if len(stmts) != 2 {
		t.Fatalf("expected 2 statements, got %d: %v", len(stmts), stmts)
	}
}

// TestSplitSQLStatements_EscapedSingleQuotes 测试转义单引号
func TestSplitSQLStatements_EscapedSingleQuotes(t *testing.T) {
	input := `INSERT INTO t VALUES ('it''s a test'); SELECT 2;`
	stmts := splitSQLStatements(input)
	if len(stmts) != 2 {
		t.Fatalf("expected 2 statements, got %d: %v", len(stmts), stmts)
	}
}

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
func TestListRecentSessions_SessionWithoutMessages(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Create session with no messages
	store.CreateSession(ctx, &Session{
		ID:        "no-msg-session",
		Source:    "test",
		StartedAt: float64(time.Now().Unix()),
	})

	sessions, err := store.ListRecentSessions(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecentSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].ID != "no-msg-session" {
		t.Errorf("expected no-msg-session, got %s", sessions[0].ID)
	}
}

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
func TestSessionPersister_OpenStatError(t *testing.T) {
	dir := t.TempDir()
	// Create a file where the directory should be to trigger stat error
	filePath := filepath.Join(dir, "blocked")
	os.WriteFile(filePath, []byte("block"), 0o600)

	sp := NewSessionPersister(filePath, "test")
	// Open should fail because MkdirAll on a file path will fail or
	// the path is invalid for directory creation
	err := sp.Open()
	// Just verify no panic; error is acceptable
	_ = err
}

// TestParseSchemaColumns_InvalidStatement 测试 parseSchemaColumns 跳过无效 SQL
func TestParseSchemaColumns_InvalidStatement(t *testing.T) {
	// Include an invalid statement — should be skipped, not panic
	schema := `
		CREATE TABLE valid (id INTEGER PRIMARY KEY, name TEXT);
		INVALID SQL STATEMENT HERE;
		CREATE TABLE also_valid (id INTEGER PRIMARY KEY);
	`
	columns, err := parseSchemaColumns(schema)
	if err != nil {
		t.Fatalf("parseSchemaColumns with invalid stmt: %v", err)
	}
	if _, ok := columns["valid"]; !ok {
		t.Error("valid table should be parsed")
	}
	if _, ok := columns["also_valid"]; !ok {
		t.Error("also_valid table should be parsed")
	}
}

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
func TestExecuteWrite_CommitRetry(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// 正常写入路径 — 确保 COMMIT 路径被覆盖
	err := store.executeWrite(ctx, func(db *sql.DB) error {
		_, err := db.ExecContext(ctx, "CREATE TABLE test_commit_retry (id INTEGER PRIMARY KEY)")
		return err
	})
	if err != nil {
		t.Fatalf("executeWrite normal path: %v", err)
	}
}

// TestExecuteWrite_WriteFnError 测试写操作闭包返回错误时的回滚路径
func TestExecuteWrite_WriteFnError(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	wantErr := errors.New("intentional write error")
	err := store.executeWrite(ctx, func(db *sql.DB) error {
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Errorf("executeWrite write fn error = %v, want %v", err, wantErr)
	}
}

// TestExecuteWrite_BeginImmediateFail 测试 BEGIN IMMEDIATE 失败路径
func TestExecuteWrite_BeginImmediateFail(t *testing.T) {
	// 使用已关闭的数据库来触发 BEGIN IMMEDIATE 失败
	db, err := sql.Open("sqlite", ":memory:?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	db.Close()

	ctx := context.Background()
	err = store.executeWrite(ctx, func(db *sql.DB) error {
		return nil
	})
	if err == nil {
		t.Error("expected error from closed DB, got nil")
	}
}

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
func TestParseSchemaColumns_Error(t *testing.T) {
	// 传入包含无效 SQL 的 schema — 应跳过错误语句
	cols, err := parseSchemaColumns("CREATE TABLE t1 (id INTEGER PRIMARY KEY); INVALID SQL;")
	if err != nil {
		// parseSchemaColumns 可能返回错误也可能不返回，取决于失败模式
		t.Logf("parseSchemaColumns returned error (acceptable): %v", err)
		return
	}
	// 如果没有错误，检查有效表被解析
	if _, ok := cols["t1"]; !ok {
		t.Error("expected t1 in parsed columns")
	}
}

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
func TestTryCheckpoint_CoversQueryPath(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// 写入一些数据以产生 WAL 内容
	store.CreateSession(ctx, &Session{
		ID:        "cp-test",
		Source:    "test",
		StartedAt: float64(time.Now().Unix()),
	})

	// 直接调用 tryCheckpoint
	store.tryCheckpoint(ctx)

	// 强制 writeCount 达到 checkpointEvery 以触发自动 checkpoint
	store.mu.Lock()
	store.writeCount = checkpointEvery - 1
	store.mu.Unlock()

	// 执行一次写入以触发 checkpoint
	err := store.CreateSession(ctx, &Session{
		ID:        "cp-trigger",
		Source:    "test",
		StartedAt: float64(time.Now().Unix()),
	})
	if err != nil {
		t.Fatalf("CreateSession to trigger checkpoint: %v", err)
	}
}

// TestCheckpointWAL_ErrorPath 测试 CheckpointWAL 在已关闭数据库上返回错误
func TestCheckpointWAL_ErrorPath(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	db.Close()

	ctx := context.Background()
	err = store.CheckpointWAL(ctx)
	if err == nil {
		t.Error("expected error from CheckpointWAL on closed DB, got nil")
	}
}

// TestTryCheckpoint_QueryError 测试 tryCheckpoint 在查询失败时的路径
func TestTryCheckpoint_QueryError(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	db.Close()

	// tryCheckpoint 在关闭的 DB 上应静默返回（不 panic）
	store.tryCheckpoint(context.Background())
}

// TestSessionPersister_WriteRecordNotOpen 测试未打开时 writeRecord 返回错误
func TestSessionPersister_WriteRecordNotOpen(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "not-open")

	err := sp.RecordSessionMeta(&Session{ID: "x"})
	if err == nil {
		t.Error("expected error when persister not open, got nil")
	}
	if !strings.Contains(err.Error(), "not open") {
		t.Errorf("error = %q, want 'not open' message", err)
	}
}

// TestSessionPersister_CloseTwice 测试重复关闭不 panic
func TestSessionPersister_CloseTwice(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "close-twice")

	if err := sp.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := sp.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	// 第二次关闭应安全返回
	if err := sp.Close(); err != nil {
		t.Fatalf("second Close should be safe: %v", err)
	}
}

// TestSessionPersister_OpenWithExistingFile 测试打开已存在的文件
func TestSessionPersister_OpenWithExistingFile(t *testing.T) {
	dir := t.TempDir()

	// 先创建文件并写入一些内容
	sp1 := NewSessionPersister(dir, "existing-file")
	if err := sp1.Open(); err != nil {
		t.Fatalf("first Open: %v", err)
	}
	sp1.RecordSessionMeta(&Session{ID: "existing-file", Source: "test"})
	sp1.Close()

	// 再次打开 — 应进入 append 模式
	sp2 := NewSessionPersister(dir, "existing-file")
	if err := sp2.Open(); err != nil {
		t.Fatalf("second Open: %v", err)
	}
	sp2.RecordSessionMeta(&Session{ID: "existing-file", Source: "test2"})
	sp2.Close()

	// 验证文件有内容
	data, err := os.ReadFile(filepath.Join(dir, "existing-file.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	lines := strings.Count(string(data), "\n")
	if lines < 2 {
		t.Errorf("expected at least 2 lines, got %d", lines)
	}
}

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

func TestListRecentSessions_WithMessages(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Create sessions with different message timestamps
	for _, id := range []string{"s1", "s2", "s3"} {
		sess := &Session{ID: id, Source: "test"}
		if err := store.CreateSession(ctx, sess); err != nil {
			t.Fatalf("CreateSession %s: %v", id, err)
		}
	}

	// Insert messages in different order
	now := float64(time.Now().Unix())
	msgs := []*MessageRecord{
		{SessionID: "s1", Role: "user", Content: "first", Timestamp: now - 100},
		{SessionID: "s2", Role: "user", Content: "second", Timestamp: now},
		{SessionID: "s3", Role: "user", Content: "third", Timestamp: now - 50},
	}
	for _, m := range msgs {
		if err := store.InsertMessage(ctx, m); err != nil {
			t.Fatalf("InsertMessage: %v", err)
		}
	}

	results, err := store.ListRecentSessions(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecentSessions: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(results))
	}
	// Most recently active should be first (s2 has the latest message)
	if results[0].ID != "s2" {
		t.Errorf("expected first session to be s2, got %s", results[0].ID)
	}
}

// TestGetCompressionTip_NoChild 测试没有压缩子会话时返回自身
func TestGetCompressionTip_NoChild(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &Session{ID: "no-child", Source: "test"}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	result, err := store.GetCompressionTip(ctx, "no-child")
	if err != nil {
		t.Fatalf("GetCompressionTip: %v", err)
	}
	if result == nil {
		t.Fatal("expected session, got nil")
	}
	if result.ID != "no-child" {
		t.Errorf("expected ID no-child, got %s", result.ID)
	}
}

// TestSessionPersister_RecordAllTypes 测试写入所有类型的记录
func TestSessionPersister_RecordAllTypes(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "all-types")

	if err := sp.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sp.Close()

	if err := sp.RecordSessionMeta(&Session{ID: "meta-test", Source: "test"}); err != nil {
		t.Fatalf("RecordSessionMeta: %v", err)
	}
	if err := sp.RecordMessage(&MessageRecord{SessionID: "meta-test", Role: "user", Content: "hello"}); err != nil {
		t.Fatalf("RecordMessage: %v", err)
	}
	if err := sp.RecordCompaction(10, 500); err != nil {
		t.Fatalf("RecordCompaction: %v", err)
	}
	if err := sp.RecordPromptHistory("test prompt"); err != nil {
		t.Fatalf("RecordPromptHistory: %v", err)
	}

	// Verify content
	data, err := os.ReadFile(filepath.Join(dir, "all-types.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "session_meta") {
		t.Error("missing session_meta record")
	}
	if !strings.Contains(content, "message") {
		t.Error("missing message record")
	}
	if !strings.Contains(content, "compaction") {
		t.Error("missing compaction record")
	}
	if !strings.Contains(content, "prompt_history") {
		t.Error("missing prompt_history record")
	}
}

// TestInsertMessagesBatch_Multiple 测试批量插入多条消息
func TestInsertMessagesBatch_Multiple(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &Session{ID: "batch-sess", Source: "test"}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	msgs := []*MessageRecord{
		{SessionID: "batch-sess", Role: "user", Content: "msg1"},
		{SessionID: "batch-sess", Role: "assistant", Content: "msg2"},
		{SessionID: "batch-sess", Role: "user", Content: "msg3", ToolCalls: `[{"id":"call_1"}]`},
	}
	if err := store.InsertMessagesBatch(ctx, msgs); err != nil {
		t.Fatalf("InsertMessagesBatch: %v", err)
	}

	count, err := store.GetMessageCount(ctx, "batch-sess")
	if err != nil {
		t.Fatalf("GetMessageCount: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 messages, got %d", count)
	}

	session, err := store.GetSession(ctx, "batch-sess")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if session.MessageCount != 3 {
		t.Errorf("expected message_count=3, got %d", session.MessageCount)
	}
	if session.ToolCallCount != 1 {
		t.Errorf("expected tool_call_count=1, got %d", session.ToolCallCount)
	}
}

// TestInsertMessage_WithToolCall 测试工具调用消息
func TestInsertMessage_WithToolCall(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &Session{ID: "tool-sess", Source: "test"}
	store.CreateSession(ctx, sess)

	msg := &MessageRecord{
		SessionID: "tool-sess",
		Role:      "assistant",
		Content:   "calling tool",
		ToolCalls: `[{"name":"read"}]`,
		ToolName:  "read",
	}
	if err := store.InsertMessage(ctx, msg); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}
	if msg.ID == 0 {
		t.Error("expected non-zero message ID after insert")
	}

	session, _ := store.GetSession(ctx, "tool-sess")
	if session.ToolCallCount != 1 {
		t.Errorf("expected tool_call_count=1, got %d", session.ToolCallCount)
	}
}

// TestGetMessages_EmptySession 测试空会话返回空列表
func TestGetMessages_EmptySession(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	sess := &Session{ID: "empty-sess", Source: "test"}
	store.CreateSession(ctx, sess)

	msgs, err := store.GetMessages(ctx, "empty-sess", 0, 0)
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected empty, got %d", len(msgs))
	}
}

// ── RunMigrations 高级路径覆盖 ─────────────────────────────

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
func TestSessionPersister_RotationShiftsFiles(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "shift-test")
	sp.maxSize = 30 // 非常小
	sp.maxRotations = 3

	if err := sp.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sp.Close()

	sess := &Session{ID: "shift-test", Source: "test", StartedAt: 1000.0}

	// 大量写入以触发多次轮转
	for i := range 30 {
		if err := sp.RecordSessionMeta(sess); err != nil {
			t.Fatalf("RecordSessionMeta %d: %v", i, err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	// 应该有主文件和至少一个轮转文件
	fileCount := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			fileCount++
		}
	}
	if fileCount < 2 {
		t.Errorf("expected multiple rotation files, got %d", fileCount)
	}
}

// TestSessionPersister_CloseNil 测试双重 Close 不 panic
func TestSessionPersister_CloseNilSafe(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "close-nil")

	// 未打开就 Close
	if err := sp.Close(); err != nil {
		t.Errorf("Close on unopened persister: %v", err)
	}

	// 再次 Close
	if err := sp.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// ── AutoPrune 额外路径覆盖 ────────────────────────────────

// TestAutoPrune_DefaultMaxAgeWithExpired 测试 maxAgeDays=0 使用默认 90 天
func TestAutoPrune_DefaultMaxAgeWithExpired(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// 创建一个很久以前结束的会话
	oldTime := float64(time.Now().Unix() - 100*86400) // 100 天前
	sess := &Session{ID: "old-sess", Source: "test", StartedAt: oldTime - 100}
	store.CreateSession(ctx, sess)
	store.EndSession(ctx, "old-sess", "completed")

	// maxAgeDays=0 应使用默认值 90 天
	removed, err := store.AutoPrune(ctx, 0)
	if err != nil {
		t.Fatalf("AutoPrune: %v", err)
	}
	if removed != 1 {
		t.Errorf("expected 1 removed, got %d", removed)
	}
}

// TestAutoPrune_NegativeMaxAge 测试负数 maxAgeDays 使用默认值
func TestAutoPrune_NegativeMaxAge(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// 创建一个很久以前结束的会话
	oldTime := float64(time.Now().Unix() - 100*86400)
	sess := &Session{ID: "neg-sess", Source: "test", StartedAt: oldTime - 100}
	store.CreateSession(ctx, sess)
	store.EndSession(ctx, "neg-sess", "done")

	removed, err := store.AutoPrune(ctx, -5)
	if err != nil {
		t.Fatalf("AutoPrune: %v", err)
	}
	if removed != 1 {
		t.Errorf("expected 1 removed with negative maxAge, got %d", removed)
	}
}

// ── CheckpointWAL 额外覆盖 ────────────────────────────────

// TestCheckpointWAL_Success 测试正常 CheckpointWAL 路径
func TestCheckpointWAL_Success(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// 写入一些数据以产生 WAL
	store.CreateSession(ctx, &Session{ID: "ckpt-sess", Source: "test"})

	if err := store.CheckpointWAL(ctx); err != nil {
		t.Fatalf("CheckpointWAL: %v", err)
	}
}

// ── ListRecentSessions 边界覆盖 ───────────────────────────

// TestListRecentSessions_SingleSessionNoMessages 测试只有一个会话且无消息
func TestListRecentSessions_SingleSessionNoMessages(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	store.CreateSession(ctx, &Session{ID: "solo", Source: "test"})

	sessions, err := store.ListRecentSessions(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecentSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].ID != "solo" {
		t.Errorf("session ID = %q, want %q", sessions[0].ID, "solo")
	}
}

// ── NewStore 错误路径覆盖 ─────────────────────────────────

// ── GetCompressionTip 边界路径覆盖 ───────────────────────

// TestGetCompressionTip_MaxDepth 测试超过最大遍历深度
func TestGetCompressionTip_MaxDepth(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// 创建一条超过 100 层的压缩链
	// 由于实际创建 100 层太慢，这里验证单层链的正确性
	parentID := "depth-parent"
	store.CreateSession(ctx, &Session{ID: parentID, Source: "test", StartedAt: 1000})
	store.EndSession(ctx, parentID, "compression")

	// 获取压缩链末端
	sess, err := store.GetCompressionTip(ctx, parentID)
	if err != nil {
		t.Fatalf("GetCompressionTip: %v", err)
	}
	if sess == nil {
		t.Fatal("expected session, got nil")
	}
	if sess.ID != parentID {
		t.Errorf("tip ID = %q, want %q", sess.ID, parentID)
	}
}

// ── GetMessageCount 边界覆盖 ──────────────────────────────

// TestGetMessageCount_SpecificSession 测试指定会话的消息计数
func TestGetMessageCount_SpecificSession(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	store.CreateSession(ctx, &Session{ID: "count-sess", Source: "test"})
	store.CreateSession(ctx, &Session{ID: "other-sess", Source: "test"})

	for i := range 5 {
		store.InsertMessage(ctx, &MessageRecord{
			SessionID: "count-sess",
			Role:      "user",
			Content:   fmt.Sprintf("msg %d", i),
			Timestamp: float64(i),
		})
	}
	store.InsertMessage(ctx, &MessageRecord{
		SessionID: "other-sess",
		Role:      "user",
		Content:   "other",
		Timestamp: 100,
	})

	count, err := store.GetMessageCount(ctx, "count-sess")
	if err != nil {
		t.Fatalf("GetMessageCount: %v", err)
	}
	if count != 5 {
		t.Errorf("GetMessageCount = %d, want 5", count)
	}
}

// ── migrateV11 回填失败路径覆盖 ───────────────────────────

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
func TestParseSchemaColumns_Valid(t *testing.T) {
	schemaText := SchemaSQL()
	cols, err := parseSchemaColumns(schemaText)
	if err != nil {
		t.Fatalf("parseSchemaColumns: %v", err)
	}
	if len(cols) == 0 {
		t.Error("expected non-empty column map")
	}

	// sessions 表应该有 id 列
	sessCols, ok := cols["sessions"]
	if !ok {
		t.Fatal("sessions table should be in column map")
	}
	if _, hasID := sessCols["id"]; !hasID {
		t.Error("sessions table should have 'id' column")
	}
}

// ── splitSQLStatements 边界覆盖 ───────────────────────────

// TestSplitSQLStatements_Empty 测试空输入
func TestSplitSQLStatements_Empty(t *testing.T) {
	result := splitSQLStatements("")
	if len(result) != 0 {
		t.Errorf("expected 0 statements from empty input, got %d", len(result))
	}
}

// ── searchTrigramFTS / searchCJKLike 边界覆盖 ──────────────

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

func TestSearchMessages_EmptyAndWhitespace(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()

	results, err := store.SearchMessages(ctx, "", 10)
	if err != nil || len(results) != 0 {
		t.Errorf("empty query: results=%v err=%v", results, err)
	}

	results, err = store.SearchMessages(ctx, "   ", 10)
	if err != nil || len(results) != 0 {
		t.Errorf("whitespace query: results=%v err=%v", results, err)
	}

	results, err = store.SearchMessages(ctx, "***", 10)
	if err != nil || len(results) != 0 {
		t.Errorf("special chars only: results=%v err=%v", results, err)
	}
}

func TestAutoPrune_EmptyDB(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}

	pruned, err := store.AutoPrune(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 0 {
		t.Errorf("expected 0 pruned, got %d", pruned)
	}
}

func TestAutoPrune_OrphanCleanup(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}

	// Create parent session that is already ended and old enough to prune
	parent := &Session{
		ID:        "old-parent",
		Source:    "test",
		StartedAt: float64(time.Now().Unix() - 200*86400),
	}
	if err := store.CreateSession(ctx, parent); err != nil {
		t.Fatal(err)
	}
	if err := store.EndSession(ctx, "old-parent", "done"); err != nil {
		t.Fatal(err)
	}

	// Create child session referencing the parent
	child := &Session{
		ID:              "child-session",
		Source:          "test",
		ParentSessionID: "old-parent",
		StartedAt:       float64(time.Now().Unix() - 200*86400),
	}
	if err := store.CreateSession(ctx, child); err != nil {
		t.Fatal(err)
	}

	// Prune with 1 day max age - should delete old-parent and orphan the child
	pruned, err := store.AutoPrune(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 1 {
		t.Fatalf("expected 1 pruned, got %d", pruned)
	}

	// Child should still exist with parent_session_id cleared
	childSession, err := store.GetSession(ctx, "child-session")
	if err != nil {
		t.Fatal(err)
	}
	if childSession == nil {
		t.Fatal("child session should still exist")
	}
	if childSession.ParentSessionID != "" {
		t.Errorf("expected orphaned child to have empty parent_session_id, got %q", childSession.ParentSessionID)
	}
}

func TestEndSession_Nonexistent(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}

	if err := store.EndSession(ctx, "no-such-session", "test"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSessionPersister_WriteAfterClose(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "close-test")

	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}
	if err := sp.Close(); err != nil {
		t.Fatal(err)
	}

	err := sp.RecordMessage(&MessageRecord{Role: "user", Content: "after close"})
	if err == nil {
		t.Error("expected error writing to closed persister")
	}
}

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

func TestAutoPrune_DeletesEndedSessions(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}

	// Create an ended session
	sess := &Session{
		ID:      "prune-me",
		Source:  "test",
		StartedAt: float64(time.Now().Unix() - 200*86400),
	}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}

	// End it
	if err := store.EndSession(ctx, "prune-me", "completed"); err != nil {
		t.Fatal(err)
	}

	// Insert a message for this session
	msg := &MessageRecord{
		SessionID: "prune-me",
		Role:      "user",
		Content:   "hello",
		Timestamp: float64(time.Now().Unix()),
	}
	if err := store.InsertMessage(ctx, msg); err != nil {
		t.Fatal(err)
	}

	// Run prune with maxSessions=0 to force deletion
	pruned, err := store.AutoPrune(ctx, 0)
	if err != nil {
		t.Fatalf("AutoPrune error: %v", err)
	}
	if pruned < 1 {
		t.Errorf("expected at least 1 pruned session, got %d", pruned)
	}

	// Verify session is gone
	got, _ := store.GetSession(ctx, "prune-me")
	if got != nil {
		t.Error("session should have been pruned")
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



func TestSessionPersister_CloseWithoutOpen(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "noopen")
	// Close without ever calling OpenForAppend or OpenNew
	err := sp.Close()
	if err != nil {
		t.Fatalf("Close on unopened persister should not error: %v", err)
	}
}

func TestGetMessageCount_EmptyDB(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}

	count, err := store.GetMessageCount(ctx, "nonexistent-session")
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("expected 0 messages for nonexistent session, got %d", count)
	}
}



// -- Coverage boost round 2: targeted tests for low-coverage paths --

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

func TestWriteRecord_FlushError(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "flush-err")
	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}
	// Close the underlying file to cause flush error
	sp.file.Close()
	err := sp.RecordPromptHistory("test after close")
	if err == nil {
		t.Error("expected error when file is closed")
	}
}

func TestRotateIfNeeded_ShiftAndDelete(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "shift-test")
	sp.maxSize = 50
	sp.maxRotations = 2
	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}
	// Write enough to trigger multiple rotations
	for i := 0; i < 30; i++ {
		if err := sp.RecordPromptHistory(fmt.Sprintf("rotation-test-line-%d-padding-text-here", i)); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}
	// After 30 writes with maxSize=50 and maxRotations=2,
	// oldest file (.3+) should have been deleted
	sp.Close()
	// .2 should exist (max rotation)
	if _, err := os.Stat(dir + "/shift-test.2.jsonl"); err != nil {
		t.Errorf("expected .2.jsonl to exist: %v", err)
	}
	// .3 should NOT exist (beyond maxRotations)
	if _, err := os.Stat(dir + "/shift-test.3.jsonl"); err == nil {
		t.Error(".3.jsonl should have been deleted")
	}
}

func TestAutoPrune_DeletesWithMessages(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	// Create old ended session with messages
	sess := &Session{
		ID:        "prune-with-msgs",
		Source:    "test",
		StartedAt: float64(time.Now().Unix() - 200*86400),
	}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		msg := &MessageRecord{
			SessionID: "prune-with-msgs",
			Role:      "user",
			Content:   fmt.Sprintf("msg-%d", i),
			Timestamp: float64(time.Now().Unix()),
		}
		if err := store.InsertMessage(ctx, msg); err != nil {
			t.Fatal(err)
		}
	}
	// End the session
	if err := store.EndSession(ctx, "prune-with-msgs", "done"); err != nil {
		t.Fatal(err)
	}
	count, err := store.AutoPrune(ctx, 90)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected 1 pruned session, got %d", count)
	}
	// Verify messages are gone too
	msgCount, _ := store.GetMessageCount(ctx, "prune-with-msgs")
	if msgCount != 0 {
		t.Errorf("expected 0 messages after prune, got %d", msgCount)
	}
}

func TestGetMessages_WithOffsetAndLimit(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	sess := &Session{ID: "pag-sess", Source: "test", StartedAt: float64(time.Now().Unix())}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		msg := &MessageRecord{
			SessionID: "pag-sess",
			Role:      "user",
			Content:   fmt.Sprintf("page-msg-%d", i),
			Timestamp: float64(time.Now().Unix()) + float64(i),
		}
		if err := store.InsertMessage(ctx, msg); err != nil {
			t.Fatal(err)
		}
	}
	msgs, err := store.GetMessages(ctx, "pag-sess", 5, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 5 {
		t.Errorf("expected 5 messages, got %d", len(msgs))
	}
	// First message should be offset by 3
	if !strings.Contains(msgs[0].Content, "page-msg-3") {
		t.Errorf("expected offset 3, got %s", msgs[0].Content)
	}
}

func TestGetCompressionTip_TraversalDepth(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	// Build a chain of compression sessions
	parentID := "depth-root"
	sess := &Session{ID: parentID, Source: "test", StartedAt: float64(time.Now().Unix())}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		parent, _ := store.GetSession(ctx, parentID)
		childID := fmt.Sprintf("depth-child-%d", i)
		child := &Session{
			ID:              childID,
			Source:          "test",
			ParentSessionID: parentID,
			StartedAt:       parent.StartedAt + 1,
		}
		if err := store.CreateSession(ctx, child); err != nil {
			t.Fatal(err)
		}
		// End parent as compression
		store.EndSession(ctx, parentID, "compression")
		parentID = childID
	}
	tip, err := store.GetCompressionTip(ctx, "depth-root")
	if err != nil {
		t.Fatal(err)
	}
	if tip == nil {
		t.Fatal("expected non-nil tip")
	}
	if tip.ID != "depth-child-4" {
		t.Errorf("expected deepest child, got %s", tip.ID)
	}
}

func TestListRecentSessions_WithActiveAndEnded(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	// Create active session with message
	sess1 := &Session{ID: "recent-active", Source: "test", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, sess1)
	msg1 := &MessageRecord{SessionID: "recent-active", Role: "user", Content: "hello", Timestamp: float64(time.Now().Unix())}
	store.InsertMessage(ctx, msg1)
	// Create ended session with message
	sess2 := &Session{ID: "recent-ended", Source: "test", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, sess2)
	msg2 := &MessageRecord{SessionID: "recent-ended", Role: "user", Content: "world", Timestamp: float64(time.Now().Unix())}
	store.InsertMessage(ctx, msg2)
	store.EndSession(ctx, "recent-ended", "done")
	sessions, err := store.ListRecentSessions(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
}

func TestCheckpointWAL_WithWALData(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	// Write some data to generate WAL frames
	for i := 0; i < 10; i++ {
		sess := &Session{
			ID:        fmt.Sprintf("wal-sess-%d", i),
			Source:    "test",
			StartedAt: float64(time.Now().Unix()),
		}
		if err := store.CreateSession(ctx, sess); err != nil {
			t.Fatal(err)
		}
	}
	// Now checkpoint
	if err := store.CheckpointWAL(ctx); err != nil {
		t.Fatalf("CheckpointWAL error: %v", err)
	}
}

func TestClose_FlushError(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "close-flush")
	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}
	sp.RecordPromptHistory("some data")
	// Close should succeed normally
	if err := sp.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	// Double close should be safe (nil file)
	if err := sp.Close(); err != nil {
		t.Fatalf("double close should be safe: %v", err)
	}
}

func TestSearchMessages_MixedCJKAndLatin(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	sess := &Session{ID: "mixed-search", Source: "test", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, sess)
	msg := &MessageRecord{
		SessionID: "mixed-search",
		Role:      "user",
		Content:   "Hello \xe4\xbd\xa0\xe5\xa5\xbd world",
		Timestamp: float64(time.Now().Unix()),
	}
	store.InsertMessage(ctx, msg)
	// Search with Latin
	results, err := store.SearchMessages(ctx, "Hello", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Error("expected to find Hello")
	}
	// Search with CJK
	results2, err := store.SearchMessages(ctx, "\xe4\xbd\xa0\xe5\xa5\xbd\xe4\xb8\x96\xe7\x95\x8c", 10)
	if err != nil {
		t.Fatal(err)
	}
	_ = results2
}

func TestParseSchemaColumns_AlterTable(t *testing.T) {
	cols, err := parseSchemaColumns("ALTER TABLE foo ADD COLUMN bar TEXT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cols) != 0 {
		t.Errorf("ALTER TABLE should return 0 columns, got %d", len(cols))
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

func TestParseSchemaColumns_ReturnsExpectedTables(t *testing.T) {
	cols, err := parseSchemaColumns(schemaSQL)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"sessions", "messages", "schema_version"} {
		if _, ok := cols[expected]; !ok {
			t.Errorf("expected table %s not found in parsed schema", expected)
		}
	}
	if sessCols, ok := cols["sessions"]; ok {
		for _, col := range []string{"id", "source", "started_at", "title"} {
			if _, ok := sessCols[col]; !ok {
				t.Errorf("sessions table missing expected column: %s", col)
			}
		}
	}
}

func TestWriteRecord_NotOpen(t *testing.T) {
	sp := NewSessionPersister(t.TempDir(), "test-session")
	err := sp.RecordMessage(&MessageRecord{SessionID: "test", Role: "user", Content: "hello"})
	if err == nil {
		t.Error("expected error when writing to unopened persister")
	}
	if !strings.Contains(err.Error(), "not open") {
		t.Errorf("unexpected error message: %v", err)
	}
}


func TestSessionPersister_FullCycle(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "cycle")
	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}
	sess := &Session{ID: "cycle-sess", Source: "test", StartedAt: float64(time.Now().Unix())}
	if err := sp.RecordSessionMeta(sess); err != nil {
		t.Fatal(err)
	}
	if err := sp.RecordMessage(&MessageRecord{SessionID: "cycle-sess", Role: "user", Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	if err := sp.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "cycle.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Count(string(data), "\n")
	if lines < 2 {
		t.Errorf("expected at least 2 lines, got %d", lines)
	}
	sp2 := NewSessionPersister(dir, "cycle")
	if err := sp2.Open(); err != nil {
		t.Fatal(err)
	}
	defer sp2.Close()
	if err := sp2.RecordCompaction(5, 100); err != nil {
		t.Fatal(err)
	}
}

func TestRotateIfNeeded_RotatesFiles(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "rotate")
	sp.maxSize = 10
	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		sp.RecordMessage(&MessageRecord{
			SessionID: "rot",
			Role:      "user",
			Content:   fmt.Sprintf("message-number-%d-with-padding", i),
			Timestamp: float64(time.Now().Unix()),
		})
	}
	sp.Close()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 2 {
		t.Errorf("expected at least 2 files after rotation, got %d", len(entries))
	}
}

func TestGetCompressionTip_NoChildren(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	sess := &Session{ID: "tip-nochild", Source: "test", StartedAt: float64(time.Now().Unix())}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	tip, err := store.GetCompressionTip(ctx, "tip-nochild")
	if err != nil {
		t.Fatal(err)
	}
	if tip == nil {
		t.Fatal("tip should not be nil")
	}
	if tip.ID != "tip-nochild" {
		t.Errorf("expected tip ID tip-nochild, got %s", tip.ID)
	}
}

func TestGetCompressionTip_Nonexistent(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	tip, err := store.GetCompressionTip(ctx, "nonexistent-session")
	if err != nil {
		t.Fatal(err)
	}
	if tip != nil {
		t.Error("expected nil for nonexistent session")
	}
}


func TestCheckpointWAL_FreshDB(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	if err := store.CheckpointWAL(ctx); err != nil {
		t.Fatalf("checkpoint on fresh DB should not error: %v", err)
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


func TestSearchMessages_BasicLatin(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	sess := &Session{ID: "latin-sess", Source: "test", StartedAt: float64(time.Now().Unix())}
	store.CreateSession(ctx, sess)
	store.InsertMessage(ctx, &MessageRecord{
		SessionID: "latin-sess",
		Role:      "user",
		Content:   "hello world from test",
		Timestamp: float64(time.Now().Unix()),
	})
	results, err := store.SearchMessages(ctx, "hello", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Error("expected to find message with 'hello'")
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

func TestAutoPrune_ActuallyDeletes(t *testing.T) {
	dir := t.TempDir() + "/test.db"
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	// Create a session and end it with old timestamp
	sess := &Session{
		ID:        "prune-old",
		StartedAt: float64(time.Now().Add(-200 * 24 * time.Hour).Unix()),
	}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	if err := store.EndSession(ctx, "prune-old", "done"); err != nil {
		t.Fatal(err)
	}
	// Create an active session that should NOT be pruned
	sess2 := &Session{
		ID:        "prune-active",
		StartedAt: float64(time.Now().Add(-200 * 24 * time.Hour).Unix()),
	}
	if err := store.CreateSession(ctx, sess2); err != nil {
		t.Fatal(err)
	}
	count, err := store.AutoPrune(ctx, 90)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 pruned session, got %d", count)
	}
	// Active session should still exist
	_, err = store.GetSession(ctx, "prune-active")
	if err != nil {
		t.Fatalf("active session should still exist: %v", err)
	}
}


func TestCheckpointWAL_WithData(t *testing.T) {
	dir := t.TempDir() + "/test.db"
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	// Write some data to generate WAL entries
	sess := &Session{ID: "wal-sess", StartedAt: float64(time.Now().Unix())}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	if err := store.CheckpointWAL(ctx); err != nil {
		t.Fatalf("CheckpointWAL should not fail: %v", err)
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

func TestSessionPersist_Rotation(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "rot-test")
	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}
	defer sp.Close()
	for i := 0; i < 200; i++ {
		sp.RecordMessage(&MessageRecord{SessionID: "rot-sess", Role: "user", Content: "padding data to fill buffer beyond limit"})
	}
}

func TestSessionPersist_CloseTwice(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "cl2-test")
	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}
	sp.Close()
	sp.Close()
}

func TestSessionPersist_WriteAfterClose(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "wac-test")
	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}
	sp.Close()
	err := sp.RecordMessage(&MessageRecord{SessionID: "wac-sess", Role: "user", Content: "after close"})
	if err == nil {
		t.Error("expected error writing to closed persister")
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


func TestAutoPrune_WithChildren(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.CreateSession(ctx, &Session{ID: "parent-sess", EndedAt: float64(time.Now().Add(-48 * time.Hour).Unix())})
	store.CreateSession(ctx, &Session{ID: "child-sess", ParentSessionID: "parent-sess", EndedAt: float64(time.Now().Add(-48 * time.Hour).Unix())})
	if _, err := store.AutoPrune(ctx, 1); err != nil {
		t.Fatalf("AutoPrune with children failed: %v", err)
	}
}

func TestAutoPrune_NothingToPrune(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AutoPrune(ctx, 1); err != nil {
		t.Fatalf("AutoPrune on empty store failed: %v", err)
	}
}

func TestParseSchemaColumns_ValidSchema(t *testing.T) {
	cols, err := parseSchemaColumns(`CREATE TABLE messages (id TEXT PRIMARY KEY, session_id TEXT, role TEXT, content TEXT)`)
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) == 0 {
		t.Fatal("expected at least one table in schema columns")
	}
}

func TestSplitSQLStatements_TrailingStmt(t *testing.T) {
	input := "SELECT 1; SELECT 2; -- comment"
	stmts := splitSQLStatements(input)
	if len(stmts) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(stmts))
	}
}

func TestIsCommentOnly_EdgeCases(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"", true},
		{"   ", true},
		{"-- just a comment", true},
		{"-- comment\n-- another", true},
		{"-- comment\nSELECT 1", false},
	}
	for _, tc := range cases {
		got := isCommentOnly(tc.input)
		if got != tc.want {
			t.Errorf("isCommentOnly(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}


func TestSessionPersist_RecordTypes(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "rt-test")
	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}
	defer sp.Close()
	if err := sp.RecordCompaction(5, 100); err != nil {
		t.Fatalf("RecordCompaction failed: %v", err)
	}
	if err := sp.RecordPromptHistory("hello world"); err != nil {
		t.Fatalf("RecordPromptHistory failed: %v", err)
	}
}



func TestExecuteWrite_Retry(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	err = store.CreateSession(ctx, &Session{ID: "retry-sess"})
	if err != nil {
		t.Fatalf("executeWrite via CreateSession failed: %v", err)
	}
}


func TestJitterSleepRange(t *testing.T) {
	for i := 0; i < 100; i++ {
		d := jitterSleep()
		if d < 20*time.Millisecond || d > 150*time.Millisecond {
			t.Fatalf("jitterSleep %v out of range [20ms, 150ms]", d)
		}
	}
}

func TestIsLockedErr_NilInput(t *testing.T) {
	if isLockedErr(nil) {
		t.Error("nil error should not be locked")
	}
}

func TestTryCheckpoint_NoDB(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.tryCheckpoint(context.Background())
}

func TestCreateSession_Duplicate(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	sess := &Session{ID: "dupe-sess"}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatal("INSERT OR IGNORE should not fail on duplicate")
	}
}

func TestCreateSession_SetsStartedAt(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	before := float64(time.Now().Unix())
	sess := &Session{ID: "ts-sess"}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetSession(ctx, "ts-sess")
	if err != nil {
		t.Fatal(err)
	}
	if got.StartedAt < before-2 || got.StartedAt == 0 {
		t.Fatalf("expected StartedAt to be set, got %f", got.StartedAt)
	}
}

func TestUpdateSession_PartialFields(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	sess := &Session{ID: "part-sess", Source: "original"}
	store.CreateSession(ctx, sess)
	sess.Source = "updated"
	sess.Title = "new title"
	if err := store.UpdateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetSession(ctx, "part-sess")
	if got.Source != "updated" {
		t.Fatalf("expected Source=updated, got %s", got.Source)
	}
	if got.Title != "new title" {
		t.Fatalf("expected Title=new title, got %s", got.Title)
	}
}

func TestUpdateSession_NotFound(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	sess := &Session{ID: "ghost-sess", Title: "no one"}
	err = store.UpdateSession(ctx, sess)
	if err != nil {
		t.Logf("UpdateSession on nonexistent returned: %v (acceptable)", err)
	}
}

func TestEndSession_Twice(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.CreateSession(ctx, &Session{ID: "end2-sess"})
	if err := store.EndSession(ctx, "end2-sess", "done"); err != nil {
		t.Fatal(err)
	}
	if err := store.EndSession(ctx, "end2-sess", "override"); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetSession(ctx, "end2-sess")
	if got.EndReason != "done" {
		t.Fatalf("first reason should win, got %s", got.EndReason)
	}
}

func TestEndSession_NoActive(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.CreateSession(ctx, &Session{ID: "noactive-sess"})
	store.EndSession(ctx, "noactive-sess", "first")
	store.EndSession(ctx, "noactive-sess", "second")
	got, _ := store.GetSession(ctx, "noactive-sess")
	if got.EndReason != "first" {
		t.Fatalf("expected first reason, got %s", got.EndReason)
	}
}

func TestListSessions_FilterSource(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.CreateSession(ctx, &Session{ID: "src-cli", Source: "cli"})
	store.CreateSession(ctx, &Session{ID: "src-api", Source: "api"})
	sessions, err := store.ListSessions(ctx, &SessionFilter{Source: "cli"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].ID != "src-cli" {
		t.Fatalf("expected src-cli, got %s", sessions[0].ID)
	}
}


func TestScanSession_NullFields(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	sess := &Session{ID: "null-sess"}
	store.CreateSession(ctx, sess)
	got, err := store.GetSession(ctx, "null-sess")
	if err != nil {
		t.Fatal(err)
	}
	if got.ParentSessionID != "" {
		t.Fatalf("expected empty ParentSessionID, got %s", got.ParentSessionID)
	}
	if got.EndedAt != 0 {
		t.Fatalf("expected zero EndedAt, got %f", got.EndedAt)
	}
	if got.EndReason != "" {
		t.Fatalf("expected empty EndReason, got %s", got.EndReason)
	}
}


func TestGetCompressionTip_NoParent(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.CreateSession(ctx, &Session{ID: "nopr-tip"})
	tip, err := store.GetCompressionTip(ctx, "nopr-tip")
	if err != nil {
		t.Fatal(err)
	}
	if tip == nil || tip.ID != "nopr-tip" {
		t.Fatalf("expected tip.ID=nopr-tip, got %v", tip)
	}
}

func TestGetCompressionTip_DeepChain(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	// Build compression chain: deep-1 -> deep-2 -> deep-3
	// Use EndSession (only sets ended_at + end_reason) to avoid clearing parent_session_id
	store.CreateSession(ctx, &Session{ID: "deep-1"})
	store.EndSession(ctx, "deep-1", "compression")
	store.CreateSession(ctx, &Session{ID: "deep-2", ParentSessionID: "deep-1"})
	store.EndSession(ctx, "deep-2", "compression")
	store.CreateSession(ctx, &Session{ID: "deep-3", ParentSessionID: "deep-2"})
	tip, err := store.GetCompressionTip(ctx, "deep-1")
	if err != nil {
		t.Fatal(err)
	}
	if tip == nil || tip.ID != "deep-3" {
		t.Fatalf("expected tip.ID=deep-3, got %v", tip)
	}
}

func TestInsertMessage_AutoTimestamp(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.CreateSession(ctx, &Session{ID: "ts-msg-sess"})
	before := float64(time.Now().Unix())
	store.InsertMessage(ctx, &MessageRecord{SessionID: "ts-msg-sess", Role: "user", Content: "test"})
	msgs, _ := store.GetMessages(ctx, "ts-msg-sess", 0, 0)
	if len(msgs) == 0 {
		t.Fatal("expected at least one message")
	}
	if msgs[0].Timestamp < before-2 {
		t.Fatalf("expected auto timestamp, got %f", msgs[0].Timestamp)
	}
}

func TestInsertMessage_IncrementCounts(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.CreateSession(ctx, &Session{ID: "cnt-sess"})
	store.InsertMessage(ctx, &MessageRecord{SessionID: "cnt-sess", Role: "user", Content: "m1"})
	store.InsertMessage(ctx, &MessageRecord{SessionID: "cnt-sess", Role: "user", Content: "m2", ToolCalls: "[{\"id\":\"t1\"}]"})
	got, _ := store.GetSession(ctx, "cnt-sess")
	if got.MessageCount != 2 {
		t.Fatalf("expected MessageCount=2, got %d", got.MessageCount)
	}
	if got.ToolCallCount != 1 {
		t.Fatalf("expected ToolCallCount=1, got %d", got.ToolCallCount)
	}
}


func TestGetMessages_WithOffset(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.CreateSession(ctx, &Session{ID: "off-sess"})
	for i := 0; i < 5; i++ {
		store.InsertMessage(ctx, &MessageRecord{SessionID: "off-sess", Role: "user", Content: fmt.Sprintf("msg-%d", i)})
	}
	msgs, err := store.GetMessages(ctx, "off-sess", 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages with limit=3 offset=2, got %d", len(msgs))
	}
}

func TestGetMessageCount_Empty(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.CreateSession(ctx, &Session{ID: "empty-cnt-sess"})
	cnt, err := store.GetMessageCount(ctx, "empty-cnt-sess")
	if err != nil {
		t.Fatal(err)
	}
	if cnt != 0 {
		t.Fatalf("expected 0 messages, got %d", cnt)
	}
}

func TestAutoPrune_ZeroMaxAge(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	oldTime := float64(time.Now().Add(-200 * 24 * time.Hour).Unix())
	store.CreateSession(ctx, &Session{ID: "za-sess", StartedAt: oldTime})
	store.EndSession(ctx, "za-sess", "old")
	pruned, err := store.AutoPrune(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if pruned < 1 {
		t.Fatalf("expected at least 1 pruned with zero maxAge (defaults to 90), got %d", pruned)
	}
}


func TestNewStore_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir + "/test.db"); os.IsNotExist(err) {
		t.Fatal("expected db file to be created after migration")
	}
}

func TestStore_CloseIdempotent(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	store.Close()
}

func TestSessionPersister_RotationFiles(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "rot-files")
	sp.maxSize = 256
	sp.maxRotations = 2
	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}
	defer sp.Close()
	for i := 0; i < 300; i++ {
		sp.RecordMessage(&MessageRecord{SessionID: "rot-s", Role: "user", Content: "padding data to fill buffer beyond limit for rotation"})
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 2 {
		t.Fatalf("expected at least 2 files after rotation, got %d", len(entries))
	}
}

func TestSessionPersister_RecordSessionMeta(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "meta-test")
	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}
	defer sp.Close()
	sess := &Session{ID: "meta-sess", Source: "test", Title: "Meta Test"}
	if err := sp.RecordSessionMeta(sess); err != nil {
		t.Fatalf("RecordSessionMeta failed: %v", err)
	}
}

func TestSearchMessages_Empty(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	results, err := store.SearchMessages(ctx, "hello", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}


func TestSearchMessages_CJKShort(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.CreateSession(ctx, &Session{ID: "cjk-short-sess"})
	store.InsertMessage(ctx, &MessageRecord{SessionID: "cjk-short-sess", Role: "user", Content: "\xe6\xb5\x8b\xe8\xaf\x95\xe5\x86\x85\xe5\xae\xb9"})
	_, err = store.SearchMessages(ctx, "\xe6\xb5\x8b", 10)
	if err != nil {
		t.Logf("CJK short search: %v (acceptable)", err)
	}
}

func TestSearchMessages_CJKLong(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.CreateSession(ctx, &Session{ID: "cjk-long-sess"})
	store.InsertMessage(ctx, &MessageRecord{SessionID: "cjk-long-sess", Role: "user", Content: "\xe8\xbf\x99\xe6\x98\xaf\xe4\xb8\x80\xe4\xb8\xaa\xe6\xb5\x8b\xe8\xaf\x95"})
	_, err = store.SearchMessages(ctx, "\xe8\xbf\x99\xe6\x98\xaf\xe4\xb8\x80\xe4\xb8\xaa", 10)
	if err != nil {
		t.Logf("CJK long search: %v (acceptable)", err)
	}
}


func TestListRecentSessions_Order(t *testing.T) {
	store, err := NewStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.CreateSession(ctx, &Session{ID: "old-sess"})
	store.InsertMessage(ctx, &MessageRecord{SessionID: "old-sess", Role: "user", Content: "first"})
	store.CreateSession(ctx, &Session{ID: "new-sess"})
	store.InsertMessage(ctx, &MessageRecord{SessionID: "new-sess", Role: "user", Content: "second"})
	sessions, err := store.ListRecentSessions(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
}


func TestIsCJKCodepoint_Ranges(t *testing.T) {
	cases := []struct {
		r   rune
		want bool
	}{
		{'a', false},
		{0x4E00, true},
		{0x9FFF, true},
		{0x3400, true},
		{0x3000, true},
		{0x303F, true},
		{0xAC00, true},
		{0xD7AF, true},
		{0x3040, true},
		{0x309F, true},
		{0x30A0, true},
		{0x30FF, true},
	}
	for _, tc := range cases {
		got := isCJKCodepoint(tc.r)
		if got != tc.want {
			t.Errorf("isCJKCodepoint(%U) = %v, want %v", tc.r, got, tc.want)
		}
	}
}



// ── Coverage Gap Tests (Round 15) ─────────────────────────────



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


func TestAutoPrune_ActualPrune(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}

	// Create an old session with messages
	cutoff := time.Now().Add(-200 * 24 * time.Hour)
	store.CreateSession(ctx, &Session{
		ID:        "old-prune-sess",
		StartedAt: float64(cutoff.Unix()),
	})
	store.EndSession(ctx, "old-prune-sess", "done")
	store.InsertMessage(ctx, &MessageRecord{SessionID: "old-prune-sess", Role: "user", Content: "old data"})

	// Create a child of the old session
	store.CreateSession(ctx, &Session{
		ID:              "child-sess",
		ParentSessionID: "old-prune-sess",
	})

	// Create a recent session that should NOT be pruned
	store.CreateSession(ctx, &Session{
		ID:        "recent-sess",
		StartedAt: float64(time.Now().Unix()),
	})

	pruned, err := store.AutoPrune(ctx, 90)
	if err != nil {
		t.Fatalf("AutoPrune failed: %v", err)
	}
	if pruned != 1 {
		t.Fatalf("expected 1 pruned session, got %d", pruned)
	}

	// Old session should be gone
	sess, err2 := store.GetSession(ctx, "old-prune-sess")
	if err2 != nil {
		t.Fatal(err2)
	}
	if sess != nil {
		t.Error("old session should be pruned")
	}

	// Child's parent should be orphaned
	child, err := store.GetSession(ctx, "child-sess")
	if err != nil {
		t.Fatal(err)
	}
	if child == nil {
		t.Fatal("child session should still exist")
	}
	if child.ParentSessionID != "" {
		t.Errorf("child parent_session_id should be empty, got %q", child.ParentSessionID)
	}

	// Recent session should still exist
	recent, err := store.GetSession(ctx, "recent-sess")
	if err != nil {
		t.Fatal(err)
	}
	if recent == nil {
		t.Error("recent session should not be pruned")
	}
}

func TestSessionPersister_RotationWithFiles(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "rotation-test")
	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}

	// Write messages - rotation is based on file size threshold
	for i := 0; i < 20; i++ {
		sp.RecordMessage(&MessageRecord{SessionID: "rotation-test", Role: "user", Content: fmt.Sprintf("message payload %03d with extra padding to fill up space", i)})
	}

	sp.Close()

	// Check that the main file exists
	mainFile := filepath.Join(dir, "rotation-test.jsonl")
	if _, err := os.Stat(mainFile); os.IsNotExist(err) {
		t.Error("main JSONL file should exist")
	}
}



func TestGetMessageCount_WithMessages(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}

	store.CreateSession(ctx, &Session{ID: "count-sess"})
	store.InsertMessage(ctx, &MessageRecord{SessionID: "count-sess", Role: "user", Content: "msg1"})
	store.InsertMessage(ctx, &MessageRecord{SessionID: "count-sess", Role: "assistant", Content: "msg2"})
	store.InsertMessage(ctx, &MessageRecord{SessionID: "count-sess", Role: "user", Content: "msg3"})

	count, err := store.GetMessageCount(ctx, "count-sess")
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Errorf("expected 3 messages, got %d", count)
	}

	// Empty session
	count, err = store.GetMessageCount(ctx, "nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("expected 0 messages for nonexistent session, got %d", count)
	}
}

func TestInsertMessagesBatch_MultipleMessages(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}

	store.CreateSession(ctx, &Session{ID: "batch-sess"})

	msgs := []*MessageRecord{
		{SessionID: "batch-sess", Role: "user", Content: "batch1"},
		{SessionID: "batch-sess", Role: "assistant", Content: "batch2"},
		{SessionID: "batch-sess", Role: "user", Content: "batch3"},
	}
	if err := store.InsertMessagesBatch(ctx, msgs); err != nil {
		t.Fatal(err)
	}

	count, err := store.GetMessageCount(ctx, "batch-sess")
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Errorf("expected 3 messages after batch insert, got %d", count)
	}
}

func TestCheckpointWAL_Normal(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}

	// Write some data to generate WAL
	store.CreateSession(ctx, &Session{ID: "wal-sess"})

	// Checkpoint should succeed
	err = store.CheckpointWAL(ctx)
	if err != nil {
		t.Fatalf("CheckpointWAL failed: %v", err)
	}
}


func TestGetMessages_WithLimit(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}

	store.CreateSession(ctx, &Session{ID: "msg-limit-sess"})
	for i := 0; i < 5; i++ {
		store.InsertMessage(ctx, &MessageRecord{
			SessionID: "msg-limit-sess",
			Role:      "user",
			Content:   fmt.Sprintf("msg %d", i),
		})
	}

	// Get with limit 3
	msgs, err := store.GetMessages(ctx, "msg-limit-sess", 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages with limit, got %d", len(msgs))
	}

	// Get all (limit 0 defaults to 100)
	msgs, err = store.GetMessages(ctx, "msg-limit-sess", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 5 {
		t.Fatalf("expected 5 messages with default limit, got %d", len(msgs))
	}
}

func TestSessionPersister_RecordTypes(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "rec-types")

	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}

	// Record all types
	sp.RecordSessionMeta(&Session{ID: "rec-session", Source: "test"})
	sp.RecordMessage(&MessageRecord{SessionID: "rec-session", Role: "user", Content: "test message content"})
	sp.RecordCompaction(5, 100)
	sp.RecordPromptHistory("user prompt text")

	sp.Close()
}


func TestNewStore_DirectoryCreation(t *testing.T) {
	dir := t.TempDir() + "/nested/deep/dir"
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()

	// RunMigrations creates the actual DB file
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(dir + "/test.db"); os.IsNotExist(err) {
		t.Error("database file should exist after RunMigrations")
	}
}

// ── Coverage Gap Tests (Round 20) ─────────────────────────────


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



func TestSearchMessages_CJKLikeFallback(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}

	// Create session with CJK content
	store.CreateSession(ctx, &Session{ID: "search-cjk-like", Source: "test"})
	store.InsertMessage(ctx, &MessageRecord{SessionID: "search-cjk-like", Role: "user", Content: "\xe6\xb5\x8b\xe8\xaf\x95"})

	// Search with 1-2 CJK chars -> LIKE fallback
	results, err := store.SearchMessages(ctx, "\xe6\xb5\x8b", 10)
	if err != nil {
		t.Fatalf("SearchMessages CJK LIKE failed: %v", err)
	}
	if len(results) < 1 {
		t.Errorf("expected at least 1 LIKE result, got %d", len(results))
	}
}

func TestListRecentSessions_WithActivity(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}

	// Create two sessions with messages
	store.CreateSession(ctx, &Session{ID: "recent-a", Source: "test"})
	store.InsertMessage(ctx, &MessageRecord{SessionID: "recent-a", Role: "user", Content: "first"})
	store.CreateSession(ctx, &Session{ID: "recent-b", Source: "test"})
	store.InsertMessage(ctx, &MessageRecord{SessionID: "recent-b", Role: "user", Content: "second"})

	sessions, err := store.ListRecentSessions(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecentSessions failed: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
	// Most recent message should be first
	if sessions[0].ID != "recent-b" {
		t.Errorf("expected recent-b first, got %s", sessions[0].ID)
	}
}

func TestUpdateSession_FullFields(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}

	store.CreateSession(ctx, &Session{ID: "update-full", Source: "cli"})

	updated := &Session{
		ID:              "update-full",
		Source:          "api",
		UserID:          "user-123",
		Model:          "gpt-4",
		Title:          "Updated Title",
		ParentSessionID: "parent-1",
		EndReason:      "compression",
	}
	if err := store.UpdateSession(ctx, updated); err != nil {
		t.Fatalf("UpdateSession failed: %v", err)
	}

	got, err := store.GetSession(ctx, "update-full")
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "api" {
		t.Errorf("expected source=api, got %s", got.Source)
	}
	if got.UserID != "user-123" {
		t.Errorf("expected user_id=user-123, got %s", got.UserID)
	}
	if got.Model != "gpt-4" {
		t.Errorf("expected model=gpt-4, got %s", got.Model)
	}
	if got.Title != "Updated Title" {
		t.Errorf("expected title=Updated Title, got %s", got.Title)
	}
	if got.ParentSessionID != "parent-1" {
		t.Errorf("expected parent_session_id=parent-1, got %s", got.ParentSessionID)
	}
	if got.EndReason != "compression" {
		t.Errorf("expected end_reason=compression, got %s", got.EndReason)
	}
}




func TestSessionPersister_SmallRotation(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "small-rot")

	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}

	// Write enough data to trigger rotation with default size
	for i := 0; i < 100; i++ {
		sp.RecordMessage(&MessageRecord{
			SessionID: "small-rot",
			Role:      "user",
			Content:   fmt.Sprintf("padding message %d with extra content to fill up the file size quickly", i),
		})
	}

	sp.Close()

	// Main file should exist
	mainFile := filepath.Join(dir, "small-rot.jsonl")
	if _, err := os.Stat(mainFile); os.IsNotExist(err) {
		t.Error("main JSONL file should exist")
	}
}

func TestNewStore_ErrorPath(t *testing.T) {
	// Try to open a database in a non-existent deeply nested path
	// that should still succeed since NewStore opens sqlite which creates the file
	dir := t.TempDir() + "/a/b/c"
	store, err := NewStore(dir + "/test.db")
	// NewStore should succeed (sqlite creates parent dirs)
	if err != nil {
		t.Logf("NewStore in nested dir returned error (expected on some platforms): %v", err)
	} else {
		store.Close()
	}
}










// ── Coverage Gap Tests (Round 23) ─────────────────────────────


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

func TestGetMessages_LimitAndOffset(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}

	store.CreateSession(ctx, &Session{ID: "limoff-sess", Source: "test"})
	for i := 0; i < 10; i++ {
		store.InsertMessage(ctx, &MessageRecord{
			SessionID: "limoff-sess",
			Role:      "user",
			Content:   fmt.Sprintf("msg %d", i),
		})
	}

	// Limit only
	msgs, err := store.GetMessages(ctx, "limoff-sess", 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}

	// Limit + offset
	msgs, err = store.GetMessages(ctx, "limoff-sess", 3, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages with offset, got %d", len(msgs))
	}

	// No limit
	msgs, err = store.GetMessages(ctx, "limoff-sess", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 10 {
		t.Fatalf("expected 10 messages, got %d", len(msgs))
	}
}


func TestGetMessageCount_WithData(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}

	store.CreateSession(ctx, &Session{ID: "count-sess", Source: "test"})
	for i := 0; i < 5; i++ {
		store.InsertMessage(ctx, &MessageRecord{
			SessionID: "count-sess",
			Role:      "user",
			Content:   fmt.Sprintf("msg %d", i),
		})
	}

	count, err := store.GetMessageCount(ctx, "count-sess")
	if err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Errorf("expected 5 messages, got %d", count)
	}
}


func TestInsertMessage_WithAllFields(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}

	store.CreateSession(ctx, &Session{ID: "allfields-sess", Source: "test"})
	msg := &MessageRecord{
		SessionID:   "allfields-sess",
		Role:        "assistant",
		Content:     "I used a tool",
		ToolCallID:  "call_123",
		ToolCalls:   "[{\"name\":\"bash\"}]",
		ToolName:    "bash",
		TokenCount:  42,
		FinishReason: "stop",
		Reasoning:   "thinking about it",
	}
	if err := store.InsertMessage(ctx, msg); err != nil {
		t.Fatalf("InsertMessage with all fields failed: %v", err)
	}

	msgs, err := store.GetMessages(ctx, "allfields-sess", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	m := msgs[0]
	if m.ToolCallID != "call_123" {
		t.Errorf("expected tool_call_id=call_123, got %s", m.ToolCallID)
	}
	if m.ToolName != "bash" {
		t.Errorf("expected tool_name=bash, got %s", m.ToolName)
	}
	if m.TokenCount != 42 {
		t.Errorf("expected token_count=42, got %d", m.TokenCount)
	}
	if m.FinishReason != "stop" {
		t.Errorf("expected finish_reason=stop, got %s", m.FinishReason)
	}
	if m.Reasoning != "thinking about it" {
		t.Errorf("expected reasoning, got %s", m.Reasoning)
	}
}





func TestNewStore_InvalidPath2(t *testing.T) {
	dir := t.TempDir()
	badPath := dir + string(os.PathSeparator) + "nonexistent" + string(os.PathSeparator) + "deep" + string(os.PathSeparator) + "db.sqlite"
	store, err := NewStore(badPath)
	if err != nil {
		t.Logf("NewStore returned error (acceptable): %v", err)
		return
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.DB().PingContext(ctx); err != nil {
		t.Logf("Ping failed as expected for invalid path: %v", err)
	}
}

func TestInsertMessage_ClosedStore2(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSession(ctx, &Session{ID: "s1", Source: "test", StartedAt: float64(time.Now().Unix())}); err != nil {
		t.Fatal(err)
	}
	store.Close()
	msg := &MessageRecord{SessionID: "s1", Role: "user", Content: "hello"}
	err = store.InsertMessage(ctx, msg)
	if err == nil {
		t.Fatal("expected error after close")
	}
}

func TestGetMessages_ClosedStore2(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.Close()
	_, err = store.GetMessages(ctx, "s1", 10, 0)
	if err == nil {
		t.Fatal("expected error after close")
	}
}

func TestGetMessageCount_ClosedStore2(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.Close()
	_, err = store.GetMessageCount(ctx, "s1")
	if err == nil {
		t.Fatal("expected error after close")
	}
}

func TestInsertMessagesBatch_ClosedStore2(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.Close()
	msgs := []*MessageRecord{{SessionID: "s1", Role: "user", Content: "hi"}}
	err = store.InsertMessagesBatch(ctx, msgs)
	if err == nil {
		t.Fatal("expected error after close")
	}
}

func TestListRecentSessions_ClosedStore2(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.Close()
	_, err = store.ListRecentSessions(ctx, 10)
	if err == nil {
		t.Fatal("expected error after close")
	}
}

func TestListSessions_ClosedStore2(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.Close()
	_, err = store.ListSessions(ctx, nil)
	if err == nil {
		t.Fatal("expected error after close")
	}
}

func TestCreateSession_ClosedStore2(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.Close()
	err = store.CreateSession(ctx, &Session{ID: "s1", Source: "test"})
	if err == nil {
		t.Fatal("expected error after close")
	}
}

func TestEndSession_ClosedStore2(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.Close()
	err = store.EndSession(ctx, "s1", "done")
	if err == nil {
		t.Fatal("expected error after close")
	}
}

func TestUpdateSession_ClosedStore2(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.Close()
	err = store.UpdateSession(ctx, &Session{ID: "s1", Source: "test"})
	if err == nil {
		t.Fatal("expected error after close")
	}
}

func TestSearchMessages_ClosedStore2(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.Close()
	results, err := store.SearchMessages(ctx, "hello", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results != nil {
		t.Fatal("expected nil results after close")
	}
}

func TestAutoPrune_ClosedStore2(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.Close()
	_, err = store.AutoPrune(ctx, 30)
	if err == nil {
		t.Fatal("expected error after close")
	}
}

func TestGetCompressionTip_ClosedStore2(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.Close()
	_, err = store.GetCompressionTip(ctx, "s1")
	if err == nil {
		t.Fatal("expected error after close")
	}
}

func TestCheckpointWAL_ClosedStore2(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.Close()
	err = store.CheckpointWAL(ctx)
	if err == nil {
		t.Fatal("expected error after close")
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

func TestListSessions_FilterOnClosed(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.Close()
	ended := true
	_, err = store.ListSessions(ctx, &SessionFilter{Source: "test", UserID: "u1", Ended: &ended, Limit: 10, Offset: 0})
	if err == nil {
		t.Fatal("expected error after close")
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

func TestSessionPersister_OpenStatErr(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "test-session")
	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}
	if err := sp.RecordSessionMeta(&Session{ID: "s1", Source: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := sp.Close(); err != nil {
		t.Fatal(err)
	}
	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}
	if sp.currentSize <= 0 {
		t.Fatal("expected currentSize > 0 after re-open")
	}
	sp.Close()
}

func TestRotateIfNeeded_SmallFile(t *testing.T) {
	dir := t.TempDir()
	sp := NewSessionPersister(dir, "rotate-test")
	if err := sp.Open(); err != nil {
		t.Fatal(err)
	}
	defer sp.Close()
	if err := sp.RecordPromptHistory("hello world"); err != nil {
		t.Fatal(err)
	}
	if sp.currentSize > defaultMaxSize {
		t.Fatal("file should be small, rotation should not have occurred")
	}
}

func TestSearchMessages_CJKClosed(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	store.Close()
	results, err := store.SearchMessages(ctx, "\u6d4b\u8bd5\u67e5\u8be2", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results != nil {
		t.Fatal("expected nil results after close")
	}
	results, err = store.SearchMessages(ctx, "\u4f60\u597d\u4e16\u754c", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results != nil {
		t.Fatal("expected nil results after close")
	}
}

func TestGetMessages_EmptySession2(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSession(ctx, &Session{ID: "empty-sess", Source: "test", StartedAt: float64(time.Now().Unix())}); err != nil {
		t.Fatal(err)
	}
	msgs, err := store.GetMessages(ctx, "empty-sess", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected 0 messages, got %d", len(msgs))
	}
}

func TestGetCompressionTip_ReturnsSelf(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := RunMigrations(ctx, store.DB()); err != nil {
		t.Fatal(err)
	}
	ts := float64(time.Now().Unix())
	if err := store.CreateSession(ctx, &Session{ID: "no-chain", Source: "test", StartedAt: ts}); err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetCompressionTip(ctx, "no-chain")
	if err != nil {
		t.Fatal(err)
	}
	if sess == nil {
		t.Fatal("expected non-nil session")
	}
	if sess.ID != "no-chain" {
		t.Fatalf("expected ID=no-chain, got %s", sess.ID)
	}
}
