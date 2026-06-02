package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"nexus-agent/internal/config"
	"nexus-agent/internal/gateway/platforms"
	"nexus-agent/internal/llm"
	"nexus-agent/internal/state"
)

// ---------------------------------------------------------------------------
// test helpers for runner tests
// ---------------------------------------------------------------------------

// newTestGatewayConfig 创建测试用的 GatewayConfig。
func newTestGatewayConfig() *config.GatewayConfig {
	return &config.GatewayConfig{
		Enabled: true,
		Cache: config.CacheConfig{
			MaxSize: 10,
			IdleTTL: time.Hour,
		},
		Stream: config.StreamConfig{
			Enabled:      true,
			BufferSize:   5,
			EditInterval: 10 * time.Millisecond,
		},
	}
}

// newTestFullConfig 创建测试用的完整 Config。
func newTestFullConfig() *config.Config {
	return &config.Config{
		Agent: config.AgentConfig{
			Model:         "test-model",
			Provider:      "test-provider",
			MaxTokens:     1024,
			MaxIterations: 10,
		},
	}
}

// newTestRunnerState 创建测试用的 state.Store。
func newTestRunnerState(t *testing.T) *state.Store {
	t.Helper()
	dir := t.TempDir()
	store, err := state.NewStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if err := state.RunMigrations(context.Background(), store.DB()); err != nil {
		t.Fatalf("RunMigrations failed: %v", err)
	}
	return store
}

// runnerMockAdapter 满足 platforms.PlatformAdapter 接口的 mock。
type runnerMockAdapter struct {
	name        string
	platform    platforms.Platform
	connectErr  error
	disconnect  atomic.Int32
	connectCh   chan *platforms.MessageEvent
	sendCount   atomic.Int32
	disconnErr  error
	streaming   bool
}

func (m *runnerMockAdapter) Name() string { return m.name }
func (m *runnerMockAdapter) PlatformType() platforms.Platform {
	return m.platform
}
func (m *runnerMockAdapter) Connect(_ context.Context) (<-chan *platforms.MessageEvent, error) {
	if m.connectErr != nil {
		return nil, m.connectErr
	}
	return m.connectCh, nil
}
func (m *runnerMockAdapter) Disconnect(_ context.Context) error {
	m.disconnect.Add(1)
	return m.disconnErr
}
func (m *runnerMockAdapter) Send(_ context.Context, _ string, _ string, _ *platforms.SendOptions) (*platforms.SendResult, error) {
	m.sendCount.Add(1)
	return &platforms.SendResult{Success: true, MessageID: "msg"}, nil
}
func (m *runnerMockAdapter) EditMessage(_ context.Context, _ string, _ string, _ string) (*platforms.SendResult, error) {
	return &platforms.SendResult{Success: true}, nil
}
func (m *runnerMockAdapter) DeleteMessage(_ context.Context, _ string, _ string) error { return nil }
func (m *runnerMockAdapter) SendTyping(_ context.Context, _ string) error               { return nil }
func (m *runnerMockAdapter) SendImage(_ context.Context, _, _, _ string, _ *platforms.SendOptions) (*platforms.SendResult, error) {
	return &platforms.SendResult{Success: true, MessageID: "img"}, nil
}
func (m *runnerMockAdapter) SendVoice(_ context.Context, _, _ string, _ *platforms.SendOptions) (*platforms.SendResult, error) {
	return &platforms.SendResult{Success: true, MessageID: "voice"}, nil
}
func (m *runnerMockAdapter) SendVideo(_ context.Context, _, _, _ string, _ *platforms.SendOptions) (*platforms.SendResult, error) {
	return &platforms.SendResult{Success: true, MessageID: "vid"}, nil
}
func (m *runnerMockAdapter) SendDocument(_ context.Context, _, _, _ string, _ *platforms.SendOptions) (*platforms.SendResult, error) {
	return &platforms.SendResult{Success: true, MessageID: "doc"}, nil
}
func (m *runnerMockAdapter) MaxMessageLength() int   { return 4096 }
func (m *runnerMockAdapter) SupportsStreaming() bool { return m.streaming }

// ---------------------------------------------------------------------------
// NewGatewayRunner
// ---------------------------------------------------------------------------

func TestNewGatewayRunner(t *testing.T) {
	t.Parallel()

	t.Run("creates runner with defaults", func(t *testing.T) {
		t.Parallel()
		cfg := &config.GatewayConfig{
			Cache: config.CacheConfig{},
			Stream: config.StreamConfig{},
		}
		fullCfg := newTestFullConfig()
		st := newTestRunnerState(t)

		r := NewGatewayRunner(cfg, fullCfg, st, nil)
		if r == nil {
			t.Fatal("expected non-nil runner")
		}
		if r.agentCache == nil {
			t.Error("expected non-nil agentCache")
		}
		if r.sessionMgr == nil {
			t.Error("expected non-nil sessionMgr")
		}
		if r.deliveryMgr == nil {
			t.Error("expected non-nil deliveryMgr")
		}
		if r.hookReg == nil {
			t.Error("expected non-nil hookReg")
		}
		if r.shutdownCh == nil {
			t.Error("expected non-nil shutdownCh")
		}
		if r.msgSem == nil {
			t.Error("expected non-nil msgSem")
		}
	})

	t.Run("respects env var for concurrency", func(t *testing.T) {
		t.Parallel()
		cfg := newTestGatewayConfig()
		fullCfg := newTestFullConfig()
		st := newTestRunnerState(t)

		os.Setenv("NEXUS_MAX_CONCURRENT_MESSAGES", "5")
		defer os.Unsetenv("NEXUS_MAX_CONCURRENT_MESSAGES")

		r := NewGatewayRunner(cfg, fullCfg, st, nil)
		if cap(r.msgSem) != 5 {
			t.Errorf("msgSem cap = %d, want 5", cap(r.msgSem))
		}
	})

	t.Run("ignores invalid env var", func(t *testing.T) {
		t.Parallel()
		cfg := newTestGatewayConfig()
		fullCfg := newTestFullConfig()
		st := newTestRunnerState(t)

		os.Setenv("NEXUS_MAX_CONCURRENT_MESSAGES", "abc")
		defer os.Unsetenv("NEXUS_MAX_CONCURRENT_MESSAGES")

		r := NewGatewayRunner(cfg, fullCfg, st, nil)
		if cap(r.msgSem) != 10 {
			t.Errorf("msgSem cap = %d, want 10 (default)", cap(r.msgSem))
		}
	})

	t.Run("nil state and cron are allowed", func(t *testing.T) {
		t.Parallel()
		cfg := newTestGatewayConfig()
		fullCfg := newTestFullConfig()

		r := NewGatewayRunner(cfg, fullCfg, nil, nil)
		if r.state != nil {
			t.Error("expected nil state")
		}
		if r.cronSched != nil {
			t.Error("expected nil cronSched")
		}
	})
}

// ---------------------------------------------------------------------------
// RegisterAdapter
// ---------------------------------------------------------------------------

func TestGatewayRunner_RegisterAdapter(t *testing.T) {
	t.Parallel()

	t.Run("appends adapter", func(t *testing.T) {
		t.Parallel()
		cfg := newTestGatewayConfig()
		fullCfg := newTestFullConfig()
		st := newTestRunnerState(t)

		r := NewGatewayRunner(cfg, fullCfg, st, nil)
		a := &runnerMockAdapter{name: "test", platform: platforms.PlatformTelegram}

		r.RegisterAdapter(a)

		if len(r.adapters) != 1 {
			t.Errorf("adapters len = %d, want 1", len(r.adapters))
		}
		if r.adapters[0].Name() != "test" {
			t.Errorf("adapter name = %q, want %q", r.adapters[0].Name(), "test")
		}
	})

	t.Run("multiple adapters", func(t *testing.T) {
		t.Parallel()
		cfg := newTestGatewayConfig()
		fullCfg := newTestFullConfig()
		st := newTestRunnerState(t)

		r := NewGatewayRunner(cfg, fullCfg, st, nil)
		r.RegisterAdapter(&runnerMockAdapter{name: "a1", platform: platforms.PlatformTelegram})
		r.RegisterAdapter(&runnerMockAdapter{name: "a2", platform: platforms.PlatformDiscord})

		if len(r.adapters) != 2 {
			t.Errorf("adapters len = %d, want 2", len(r.adapters))
		}
	})
}

// ---------------------------------------------------------------------------
// loadMessageHistory
// ---------------------------------------------------------------------------

func TestGatewayRunner_loadMessageHistory(t *testing.T) {
	t.Parallel()

	t.Run("returns nil when state is nil", func(t *testing.T) {
		t.Parallel()
		cfg := newTestGatewayConfig()
		fullCfg := newTestFullConfig()
		r := NewGatewayRunner(cfg, fullCfg, nil, nil)

		msgs, err := r.loadMessageHistory(context.Background(), "sess1", 10)
		if err != nil {
			t.Fatal(err)
		}
		if msgs != nil {
			t.Error("expected nil messages")
		}
	})

	t.Run("returns nil when sessionID is empty", func(t *testing.T) {
		t.Parallel()
		cfg := newTestGatewayConfig()
		fullCfg := newTestFullConfig()
		st := newTestRunnerState(t)
		r := NewGatewayRunner(cfg, fullCfg, st, nil)

		msgs, err := r.loadMessageHistory(context.Background(), "", 10)
		if err != nil {
			t.Fatal(err)
		}
		if msgs != nil {
			t.Error("expected nil messages")
		}
	})

	t.Run("returns empty for non-existent session", func(t *testing.T) {
		t.Parallel()
		cfg := newTestGatewayConfig()
		fullCfg := newTestFullConfig()
		st := newTestRunnerState(t)
		r := NewGatewayRunner(cfg, fullCfg, st, nil)

		msgs, err := r.loadMessageHistory(context.Background(), "nonexistent", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 0 {
			t.Errorf("expected 0 messages, got %d", len(msgs))
		}
	})

	t.Run("loads messages with basic fields", func(t *testing.T) {
		t.Parallel()
		cfg := newTestGatewayConfig()
		fullCfg := newTestFullConfig()
		st := newTestRunnerState(t)
		r := NewGatewayRunner(cfg, fullCfg, st, nil)

		ctx := context.Background()
		sessionID := "hist-basic"

		// 先创建 session
		_, err := st.DB().ExecContext(ctx,
			`INSERT INTO sessions (id, source, user_id, model, started_at)
			 VALUES (?, ?, ?, ?, ?)`,
			sessionID, "tg:dm:123", "u1", "model", float64(time.Now().Unix()),
		)
		if err != nil {
			t.Fatal(err)
		}

		// 插入消息
		_, err = st.DB().ExecContext(ctx,
			`INSERT INTO messages (session_id, role, content, timestamp)
			 VALUES (?, ?, ?, ?)`,
			sessionID, "user", "hello", float64(time.Now().Unix()),
		)
		if err != nil {
			t.Fatal(err)
		}
		_, err = st.DB().ExecContext(ctx,
			`INSERT INTO messages (session_id, role, content, timestamp)
			 VALUES (?, ?, ?, ?)`,
			sessionID, "assistant", "world", float64(time.Now().Unix()),
		)
		if err != nil {
			t.Fatal(err)
		}

		msgs, err := r.loadMessageHistory(ctx, sessionID, 50)
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(msgs))
		}
		if msgs[0].Role != llm.MessageRole("user") {
			t.Errorf("msgs[0].Role = %q, want %q", msgs[0].Role, "user")
		}
		if msgs[0].Content != "hello" {
			t.Errorf("msgs[0].Content = %q, want %q", msgs[0].Content, "hello")
		}
		if msgs[1].Role != llm.MessageRole("assistant") {
			t.Errorf("msgs[1].Role = %q, want %q", msgs[1].Role, "assistant")
		}
	})

	t.Run("deserializes tool_calls from JSON", func(t *testing.T) {
		t.Parallel()
		cfg := newTestGatewayConfig()
		fullCfg := newTestFullConfig()
		st := newTestRunnerState(t)
		r := NewGatewayRunner(cfg, fullCfg, st, nil)

		ctx := context.Background()
		sessionID := "hist-tools"

		_, err := st.DB().ExecContext(ctx,
			`INSERT INTO sessions (id, source, user_id, model, started_at)
			 VALUES (?, ?, ?, ?, ?)`,
			sessionID, "tg:dm:456", "u2", "model", float64(time.Now().Unix()),
		)
		if err != nil {
			t.Fatal(err)
		}

		toolCalls := []llm.ToolCall{
			{ID: "tc1", Name: "bash", Arguments: `{"cmd":"ls"}`},
		}
		tcJSON, _ := json.Marshal(toolCalls)

		_, err = st.DB().ExecContext(ctx,
			`INSERT INTO messages (session_id, role, content, tool_calls, timestamp)
			 VALUES (?, ?, ?, ?, ?)`,
			sessionID, "assistant", "", string(tcJSON), float64(time.Now().Unix()),
		)
		if err != nil {
			t.Fatal(err)
		}

		msgs, err := r.loadMessageHistory(ctx, sessionID, 50)
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 1 {
			t.Fatalf("expected 1 message, got %d", len(msgs))
		}
		if len(msgs[0].ToolCalls) != 1 {
			t.Fatalf("expected 1 tool call, got %d", len(msgs[0].ToolCalls))
		}
		if msgs[0].ToolCalls[0].ID != "tc1" {
			t.Errorf("ToolCalls[0].ID = %q, want %q", msgs[0].ToolCalls[0].ID, "tc1")
		}
		if msgs[0].ToolCalls[0].Name != "bash" {
			t.Errorf("ToolCalls[0].Name = %q, want %q", msgs[0].ToolCalls[0].Name, "bash")
		}
	})

	t.Run("handles invalid tool_calls JSON gracefully", func(t *testing.T) {
		t.Parallel()
		cfg := newTestGatewayConfig()
		fullCfg := newTestFullConfig()
		st := newTestRunnerState(t)
		r := NewGatewayRunner(cfg, fullCfg, st, nil)

		ctx := context.Background()
		sessionID := "hist-badjson"

		_, err := st.DB().ExecContext(ctx,
			`INSERT INTO sessions (id, source, user_id, model, started_at)
			 VALUES (?, ?, ?, ?, ?)`,
			sessionID, "tg:dm:789", "u3", "model", float64(time.Now().Unix()),
		)
		if err != nil {
			t.Fatal(err)
		}

		_, err = st.DB().ExecContext(ctx,
			`INSERT INTO messages (session_id, role, content, tool_calls, timestamp)
			 VALUES (?, ?, ?, ?, ?)`,
			sessionID, "assistant", "some content", "not-valid-json", float64(time.Now().Unix()),
		)
		if err != nil {
			t.Fatal(err)
		}

		msgs, err := r.loadMessageHistory(ctx, sessionID, 50)
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 1 {
			t.Fatalf("expected 1 message, got %d", len(msgs))
		}
		// invalid JSON -> ToolCalls should be nil (graceful degradation)
		if len(msgs[0].ToolCalls) != 0 {
			t.Errorf("expected 0 tool calls on invalid JSON, got %d", len(msgs[0].ToolCalls))
		}
	})

	t.Run("preserves tool_call_id field", func(t *testing.T) {
		t.Parallel()
		cfg := newTestGatewayConfig()
		fullCfg := newTestFullConfig()
		st := newTestRunnerState(t)
		r := NewGatewayRunner(cfg, fullCfg, st, nil)

		ctx := context.Background()
		sessionID := "hist-tcid"

		_, err := st.DB().ExecContext(ctx,
			`INSERT INTO sessions (id, source, user_id, model, started_at)
			 VALUES (?, ?, ?, ?, ?)`,
			sessionID, "tg:dm:000", "u4", "model", float64(time.Now().Unix()),
		)
		if err != nil {
			t.Fatal(err)
		}

		_, err = st.DB().ExecContext(ctx,
			`INSERT INTO messages (session_id, role, content, tool_call_id, timestamp)
			 VALUES (?, ?, ?, ?, ?)`,
			sessionID, "tool", "result", "call_123", float64(time.Now().Unix()),
		)
		if err != nil {
			t.Fatal(err)
		}

		msgs, err := r.loadMessageHistory(ctx, sessionID, 50)
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) != 1 {
			t.Fatalf("expected 1 message, got %d", len(msgs))
		}
		if msgs[0].ToolCallID != "call_123" {
			t.Errorf("ToolCallID = %q, want %q", msgs[0].ToolCallID, "call_123")
		}
	})
}

// ---------------------------------------------------------------------------
// Stop
// ---------------------------------------------------------------------------

func TestGatewayRunner_Stop(t *testing.T) {
	t.Parallel()

	t.Run("stops cleanly with no adapters", func(t *testing.T) {
		t.Parallel()
		cfg := newTestGatewayConfig()
		fullCfg := newTestFullConfig()
		st := newTestRunnerState(t)
		r := NewGatewayRunner(cfg, fullCfg, st, nil)

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		if err := r.Stop(ctx); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("disconnects all adapters", func(t *testing.T) {
		t.Parallel()
		cfg := newTestGatewayConfig()
		fullCfg := newTestFullConfig()
		st := newTestRunnerState(t)
		r := NewGatewayRunner(cfg, fullCfg, st, nil)

		a1 := &runnerMockAdapter{name: "a1", platform: platforms.PlatformTelegram}
		a2 := &runnerMockAdapter{name: "a2", platform: platforms.PlatformDiscord}
		r.RegisterAdapter(a1)
		r.RegisterAdapter(a2)

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		if err := r.Stop(ctx); err != nil {
			t.Fatal(err)
		}
		if a1.disconnect.Load() != 1 {
			t.Error("expected a1 to be disconnected")
		}
		if a2.disconnect.Load() != 1 {
			t.Error("expected a2 to be disconnected")
		}
	})

	t.Run("double stop is safe", func(t *testing.T) {
		t.Parallel()
		cfg := newTestGatewayConfig()
		fullCfg := newTestFullConfig()
		st := newTestRunnerState(t)
		r := NewGatewayRunner(cfg, fullCfg, st, nil)

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		r.Stop(ctx)
		r.Stop(ctx) // 不应 panic
	})

	t.Run("stop with context timeout", func(t *testing.T) {
		t.Parallel()
		cfg := newTestGatewayConfig()
		fullCfg := newTestFullConfig()
		st := newTestRunnerState(t)
		r := NewGatewayRunner(cfg, fullCfg, st, nil)

		// 使用一个极短的超时 context
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
		defer cancel()
		time.Sleep(2 * time.Millisecond)

		// Stop 本身不会因为 context 超时返回错误，只是日志警告
		if err := r.Stop(ctx); err != nil {
			t.Fatal(err)
		}
	})
}

// ---------------------------------------------------------------------------
// Start
// ---------------------------------------------------------------------------

func TestGatewayRunner_Start(t *testing.T) {
	t.Parallel()

	t.Run("starts and stops with channel-based adapter", func(t *testing.T) {
		t.Parallel()
		cfg := newTestGatewayConfig()
		fullCfg := newTestFullConfig()
		st := newTestRunnerState(t)
		r := NewGatewayRunner(cfg, fullCfg, st, nil)

		ch := make(chan *platforms.MessageEvent)
		a := &runnerMockAdapter{
			name:       "mock",
			platform:   platforms.PlatformTelegram,
			connectCh:  ch,
			streaming:  true,
		}
		r.RegisterAdapter(a)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		if err := r.Start(ctx); err != nil {
			t.Fatal(err)
		}

		// 给 goroutine 一点时间启动
		time.Sleep(50 * time.Millisecond)

		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		r.Stop(stopCtx)
	})

	t.Run("start with nil cron scheduler", func(t *testing.T) {
		t.Parallel()
		cfg := newTestGatewayConfig()
		fullCfg := newTestFullConfig()
		st := newTestRunnerState(t)
		r := NewGatewayRunner(cfg, fullCfg, st, nil)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		if err := r.Start(ctx); err != nil {
			t.Fatal(err)
		}

		time.Sleep(30 * time.Millisecond)

		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		r.Stop(stopCtx)
	})

	t.Run("adapter connect error is logged but not fatal", func(t *testing.T) {
		t.Parallel()
		cfg := newTestGatewayConfig()
		fullCfg := newTestFullConfig()
		st := newTestRunnerState(t)
		r := NewGatewayRunner(cfg, fullCfg, st, nil)

		a := &runnerMockAdapter{
			name:       "fail-adapter",
			platform:   platforms.PlatformTelegram,
			connectErr: context.DeadlineExceeded,
		}
		r.RegisterAdapter(a)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Start 不应返回错误，只是跳过失败的适配器
		if err := r.Start(ctx); err != nil {
			t.Fatal(err)
		}

		time.Sleep(30 * time.Millisecond)

		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		r.Stop(stopCtx)
	})
}

// ---------------------------------------------------------------------------
// handlePlatform
// ---------------------------------------------------------------------------

func TestGatewayRunner_handlePlatform(t *testing.T) {
	t.Parallel()

	t.Run("exits on channel close", func(t *testing.T) {
		t.Parallel()
		cfg := newTestGatewayConfig()
		fullCfg := newTestFullConfig()
		st := newTestRunnerState(t)
		r := NewGatewayRunner(cfg, fullCfg, st, nil)

		ch := make(chan *platforms.MessageEvent)
		a := &runnerMockAdapter{name: "mock", platform: platforms.PlatformTelegram}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		r.wg.Add(1)
		go r.handlePlatform(ctx, a, ch)

		// 关闭 channel 应该让 handlePlatform 退出
		close(ch)

		done := make(chan struct{})
		go func() {
			r.wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			// 成功退出
		case <-time.After(2 * time.Second):
			t.Error("handlePlatform did not exit after channel close")
		}
	})

	t.Run("exits on context cancel", func(t *testing.T) {
		t.Parallel()
		cfg := newTestGatewayConfig()
		fullCfg := newTestFullConfig()
		st := newTestRunnerState(t)
		r := NewGatewayRunner(cfg, fullCfg, st, nil)

		ch := make(chan *platforms.MessageEvent)
		a := &runnerMockAdapter{name: "mock", platform: platforms.PlatformTelegram}

		ctx, cancel := context.WithCancel(context.Background())

		r.wg.Add(1)
		go r.handlePlatform(ctx, a, ch)

		cancel()

		done := make(chan struct{})
		go func() {
			r.wg.Wait()
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("handlePlatform did not exit after context cancel")
		}
	})

	t.Run("exits on shutdown signal", func(t *testing.T) {
		t.Parallel()
		cfg := newTestGatewayConfig()
		fullCfg := newTestFullConfig()
		st := newTestRunnerState(t)
		r := NewGatewayRunner(cfg, fullCfg, st, nil)

		ch := make(chan *platforms.MessageEvent)
		a := &runnerMockAdapter{name: "mock", platform: platforms.PlatformTelegram}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		r.wg.Add(1)
		go r.handlePlatform(ctx, a, ch)

		close(r.shutdownCh)

		done := make(chan struct{})
		go func() {
			r.wg.Wait()
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("handlePlatform did not exit after shutdown")
		}
	})
}

// ---------------------------------------------------------------------------
// cacheCleaner
// ---------------------------------------------------------------------------

func TestGatewayRunner_cacheCleaner(t *testing.T) {
	t.Parallel()

	t.Run("exits on context cancel", func(t *testing.T) {
		t.Parallel()
		cfg := newTestGatewayConfig()
		fullCfg := newTestFullConfig()
		st := newTestRunnerState(t)
		r := NewGatewayRunner(cfg, fullCfg, st, nil)

		ctx, cancel := context.WithCancel(context.Background())

		r.wg.Add(1)
		go r.cacheCleaner(ctx)

		cancel()

		done := make(chan struct{})
		go func() {
			r.wg.Wait()
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("cacheCleaner did not exit after context cancel")
		}
	})

	t.Run("exits on shutdown signal", func(t *testing.T) {
		t.Parallel()
		cfg := newTestGatewayConfig()
		fullCfg := newTestFullConfig()
		st := newTestRunnerState(t)
		r := NewGatewayRunner(cfg, fullCfg, st, nil)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		r.wg.Add(1)
		go r.cacheCleaner(ctx)

		close(r.shutdownCh)

		done := make(chan struct{})
		go func() {
			r.wg.Wait()
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("cacheCleaner did not exit after shutdown")
		}
	})
}
