package gateway

import (
	"context"
	"testing"
	"time"

	"nexus-agent/internal/gateway/platforms"
)

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
			name:      "mock",
			platform:  platforms.PlatformTelegram,
			connectCh: ch,
			streaming: true,
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
		_ = r.Stop(stopCtx)
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
		_ = r.Stop(stopCtx)
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
		_ = r.Stop(stopCtx)
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
