package gateway

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"nexus-agent/internal/config"
	"nexus-agent/internal/gateway/platforms"
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
	store, err := state.NewStore(dir + "/test.db")
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := state.RunMigrations(context.Background(), store.DB()); err != nil {
		t.Fatalf("RunMigrations failed: %v", err)
	}
	return store
}

// runnerMockAdapter 满足 platforms.PlatformAdapter 接口的 mock。
type runnerMockAdapter struct {
	name       string
	platform   platforms.Platform
	connectErr error
	disconnect atomic.Int32
	connectCh  chan *platforms.MessageEvent
	sendCount  atomic.Int32
	disconnErr error
	streaming  bool
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
func (m *runnerMockAdapter) SendTyping(_ context.Context, _ string) error              { return nil }
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
			Cache:  config.CacheConfig{},
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

		_ = os.Setenv("NEXUS_MAX_CONCURRENT_MESSAGES", "5")
		defer func() { _ = os.Unsetenv("NEXUS_MAX_CONCURRENT_MESSAGES") }()

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

		_ = os.Setenv("NEXUS_MAX_CONCURRENT_MESSAGES", "abc")
		defer func() { _ = os.Unsetenv("NEXUS_MAX_CONCURRENT_MESSAGES") }()

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

		_ = r.Stop(ctx)
		_ = r.Stop(ctx) // 不应 panic
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
