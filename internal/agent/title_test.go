package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"nexus-agent/internal/llm"
	"nexus-agent/internal/state"
)

// mockProvider 用于测试标题生成。
type mockTitleProvider struct {
	name    string
	content string
	err     error
}

func (m *mockTitleProvider) CreateChatCompletion(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &llm.ChatResponse{Content: m.content}, nil
}

func (m *mockTitleProvider) CreateChatCompletionStream(_ context.Context, _ *llm.ChatRequest) (<-chan *llm.StreamDelta, error) {
	ch := make(chan *llm.StreamDelta, 1)
	ch <- &llm.StreamDelta{Content: m.content}
	close(ch)
	return ch, nil
}

func (m *mockTitleProvider) ListModels(_ context.Context) ([]llm.ModelInfo, error) {
	return nil, nil
}

func (m *mockTitleProvider) Name() string {
	return m.name
}

func TestGenerateTitle_NilProvider(t *testing.T) {
	_, err := GenerateTitle(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected error for nil provider")
	}
	if !strings.Contains(err.Error(), "LLM") {
		t.Errorf("error should mention LLM: %v", err)
	}
}

func TestGenerateTitle_Success(t *testing.T) {
	provider := &mockTitleProvider{name: "test-model", content: "Test Title"}
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi there"},
	}
	title, err := GenerateTitle(context.Background(), provider, msgs)
	if err != nil {
		t.Fatalf("GenerateTitle: %v", err)
	}
	if title != "Test Title" {
		t.Errorf("title = %q, want %q", title, "Test Title")
	}
}

func TestGenerateTitle_ProviderError(t *testing.T) {
	provider := &mockTitleProvider{name: "test", err: fmt.Errorf("api error")}
	msgs := []llm.Message{{Role: llm.RoleUser, Content: "hi"}}
	_, err := GenerateTitle(context.Background(), provider, msgs)
	if err == nil {
		t.Fatal("expected error from provider")
	}
}

func TestGenerateTitle_EmptyResponse(t *testing.T) {
	provider := &mockTitleProvider{name: "test", content: "   "}
	msgs := []llm.Message{{Role: llm.RoleUser, Content: "hi"}}
	title, err := GenerateTitle(context.Background(), provider, msgs)
	if err != nil {
		t.Fatalf("GenerateTitle: %v", err)
	}
	if title != "" {
		t.Errorf("empty content should return empty title, got %q", title)
	}
}

func TestExtractSnippets(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "system prompt"},
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi there"},
		{Role: llm.RoleUser, Content: "how are you"},
		{Role: llm.RoleAssistant, Content: "fine"},
	}
	result := extractSnippets(msgs, 3)
	if !strings.Contains(result, "用户") {
		t.Error("should contain 用户 label")
	}
	if !strings.Contains(result, "助手") {
		t.Error("should contain 助手 label")
	}
	if strings.Contains(result, "system prompt") {
		t.Error("should skip system messages")
	}
}

func TestExtractSnippets_MaxTurns(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "m1"},
		{Role: llm.RoleAssistant, Content: "r1"},
		{Role: llm.RoleUser, Content: "m2"},
		{Role: llm.RoleAssistant, Content: "r2"},
		{Role: llm.RoleUser, Content: "m3"},
		{Role: llm.RoleAssistant, Content: "r3"},
	}
	result := extractSnippets(msgs, 2)
	if strings.Contains(result, "m3") {
		t.Error("should be limited to maxTurns*2 messages")
	}
}

func TestExtractSnippets_Empty(t *testing.T) {
	result := extractSnippets(nil, 3)
	if result != "" {
		t.Errorf("empty input: got %q", result)
	}
}

func TestExtractSnippets_LongContent(t *testing.T) {
	longContent := strings.Repeat("a", 600)
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: longContent},
	}
	result := extractSnippets(msgs, 1)
	if !strings.Contains(result, "...") {
		t.Error("long content should be truncated with ...")
	}
}

func TestExtractSnippets_SkipsToolRole(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleTool, Content: "tool result"},
		{Role: llm.RoleAssistant, Content: "hi"},
	}
	result := extractSnippets(msgs, 2)
	if strings.Contains(result, "tool result") {
		t.Error("should skip tool role messages")
	}
}

func TestCleanTitle_Basic(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"  hello  ", "hello"},
		{`"quoted title"`, "quoted title"},
		{`'single quotes'`, "single quotes"},
		{"Title: My Title", "My Title"},
		{"标题: 我的标题", "我的标题"},
		{"标题：另一个标题", "另一个标题"},
		{"「bracket title」", "bracket title"},
		{"", ""},
		{"   ", ""},
	}
	for _, tt := range tests {
		got := cleanTitle(tt.input)
		if got != tt.want {
			t.Errorf("cleanTitle(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestCleanTitle_Truncation(t *testing.T) {
	longTitle := strings.Repeat("a", 100)
	result := cleanTitle(longTitle)
	if len([]rune(result)) > titleMaxLen {
		t.Errorf("title should be truncated to %d runes, got %d", titleMaxLen, len([]rune(result)))
	}
}

func TestMaybeAutoTitle_NilProvider(t *testing.T) {
	// Should not panic with nil provider
	MaybeAutoTitle(context.Background(), nil, nil, "s1", nil)
}

func TestMaybeAutoTitle_NilStore(t *testing.T) {
	provider := &mockTitleProvider{name: "test", content: "title"}
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "hi"},
		{Role: llm.RoleUser, Content: "hello"},
	}
	MaybeAutoTitle(context.Background(), provider, nil, "s1", msgs)
}

func TestMaybeAutoTitle_TooFewMessages(t *testing.T) {
	provider := &mockTitleProvider{name: "test", content: "title"}
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "hi"},
	}
	MaybeAutoTitle(context.Background(), provider, nil, "s1", msgs)
	// Should not trigger (userCount < titleTriggerTurn)
}

func TestMaybeAutoTitle_TooManyMessages(t *testing.T) {
	provider := &mockTitleProvider{name: "test", content: "title"}
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "m1"},
		{Role: llm.RoleUser, Content: "m2"},
		{Role: llm.RoleUser, Content: "m3"},
	}
	MaybeAutoTitle(context.Background(), provider, nil, "s1", msgs)
	// Should not trigger (userCount > titleTriggerTurn)
}

func TestMaybeAutoTitle_WithStore(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := state.NewStore(tmpDir + "/test.db")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	ctx := context.Background()
	if err := state.RunMigrations(ctx, store.DB()); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	sess := &state.Session{ID: "sess-title-1", Source: "test"}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	provider := &mockTitleProvider{name: "test-model", content: "Auto Generated Title"}
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleUser, Content: "world"},
	}

	MaybeAutoTitle(ctx, provider, store, "sess-title-1", msgs)

	// 轮询等待异步 goroutine 完成
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		updated, err := store.GetSession(ctx, "sess-title-1")
		if err != nil {
			t.Fatalf("GetSession: %v", err)
		}
		if updated.Title == "Auto Generated Title" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for auto-generated title")
}

func TestMaybeAutoTitle_SessionAlreadyTitled(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := state.NewStore(tmpDir + "/test.db")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	ctx := context.Background()
	if err := state.RunMigrations(ctx, store.DB()); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	sess := &state.Session{ID: "sess-titled", Source: "test"}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	// 用 UpdateSession 设置初始标题（CreateSession 不插入 title 列）
	sess.Title = "Existing Title"
	if err := store.UpdateSession(ctx, sess); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}

	provider := &mockTitleProvider{name: "test-model", content: "New Title"}
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleUser, Content: "world"},
	}

	MaybeAutoTitle(ctx, provider, store, "sess-titled", msgs)

	// 已有标题的 session 应直接跳过，等待一小段时间后验证
	time.Sleep(200 * time.Millisecond)

	updated, err := store.GetSession(ctx, "sess-titled")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if updated.Title != "Existing Title" {
		t.Errorf("title should stay %q, got %q", "Existing Title", updated.Title)
	}
}

func TestMaybeAutoTitle_SessionNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := state.NewStore(tmpDir + "/test.db")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	if err := state.RunMigrations(context.Background(), store.DB()); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	provider := &mockTitleProvider{name: "test-model", content: "Title"}
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleUser, Content: "world"},
	}

	// nonexistent session → GetSession returns nil → should not panic
	MaybeAutoTitle(context.Background(), provider, store, "nonexistent", msgs)
	time.Sleep(300 * time.Millisecond)
}

func TestMaybeAutoTitle_ProviderError(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := state.NewStore(tmpDir + "/test.db")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	ctx := context.Background()
	if err := state.RunMigrations(ctx, store.DB()); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	sess := &state.Session{ID: "sess-err", Source: "test"}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	provider := &mockTitleProvider{name: "test-model", err: fmt.Errorf("api failure")}
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleUser, Content: "world"},
	}

	MaybeAutoTitle(ctx, provider, store, "sess-err", msgs)
	time.Sleep(300 * time.Millisecond)

	updated, err := store.GetSession(ctx, "sess-err")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if updated.Title != "" {
		t.Errorf("title should be empty on provider error, got %q", updated.Title)
	}
}
