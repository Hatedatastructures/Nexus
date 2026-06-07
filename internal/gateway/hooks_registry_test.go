package gateway

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"nexus-agent/internal/gateway/platforms"
)

// ---------------------------------------------------------------------------
// EmitCollect
// ---------------------------------------------------------------------------

func TestHookRegistry_EmitCollect(t *testing.T) {
	t.Parallel()

	t.Run("no hooks returns nil", func(t *testing.T) {
		t.Parallel()
		reg := NewHookRegistry()
		event := &platforms.MessageEvent{Text: "hello"}
		results := reg.EmitCollect(context.Background(), HookPreDispatch, event)
		if results != nil {
			t.Errorf("expected nil, got %v", results)
		}
	})

	t.Run("collects results from all hooks", func(t *testing.T) {
		t.Parallel()
		reg := NewHookRegistry()
		reg.Register(HookPreDispatch, func(ctx context.Context, event *platforms.MessageEvent) (*platforms.MessageEvent, error) {
			event.Text += "_a"
			return event, nil
		})
		reg.Register(HookPreDispatch, func(ctx context.Context, event *platforms.MessageEvent) (*platforms.MessageEvent, error) {
			event.Text += "_b"
			return event, nil
		})

		event := &platforms.MessageEvent{Text: "start"}
		results := reg.EmitCollect(context.Background(), HookPreDispatch, event)

		if len(results) != 2 {
			t.Fatalf("expected 2 results, got %d", len(results))
		}
		if results[0].Error != nil {
			t.Errorf("results[0].Error = %v, want nil", results[0].Error)
		}
		if results[1].Error != nil {
			t.Errorf("results[1].Error = %v, want nil", results[1].Error)
		}
		if results[0].Index != 0 {
			t.Errorf("results[0].Index = %d, want 0", results[0].Index)
		}
		if results[1].Index != 1 {
			t.Errorf("results[1].Index = %d, want 1", results[1].Index)
		}
	})

	t.Run("continues on error", func(t *testing.T) {
		t.Parallel()
		reg := NewHookRegistry()
		reg.Register(HookPreDispatch, func(ctx context.Context, event *platforms.MessageEvent) (*platforms.MessageEvent, error) {
			return event, errors.New("fail")
		})
		reg.Register(HookPreDispatch, func(ctx context.Context, event *platforms.MessageEvent) (*platforms.MessageEvent, error) {
			event.Text += "_ok"
			return event, nil
		})

		event := &platforms.MessageEvent{Text: "start"}
		results := reg.EmitCollect(context.Background(), HookPreDispatch, event)

		if len(results) != 2 {
			t.Fatalf("expected 2 results, got %d", len(results))
		}
		if results[0].Error == nil {
			t.Error("expected error in first result")
		}
		if results[1].Error != nil {
			t.Errorf("second result error = %v, want nil", results[1].Error)
		}
	})

	t.Run("hook returns nil event uses previous event", func(t *testing.T) {
		t.Parallel()
		reg := NewHookRegistry()
		reg.Register(HookPreDispatch, func(ctx context.Context, event *platforms.MessageEvent) (*platforms.MessageEvent, error) {
			return nil, nil
		})
		reg.Register(HookPreDispatch, func(ctx context.Context, event *platforms.MessageEvent) (*platforms.MessageEvent, error) {
			return event, nil
		})

		event := &platforms.MessageEvent{Text: "test"}
		results := reg.EmitCollect(context.Background(), HookPreDispatch, event)

		if len(results) != 2 {
			t.Fatalf("expected 2 results, got %d", len(results))
		}
		if results[1].Event == nil {
			t.Error("second result event should not be nil")
		}
	})

	t.Run("with timeout config", func(t *testing.T) {
		t.Parallel()
		reg := NewHookRegistry()
		reg.RegisterWithConfig(HookPreDispatch, func(ctx context.Context, event *platforms.MessageEvent) (*platforms.MessageEvent, error) {
			return event, nil
		}, HookConfig{Timeout: time.Second})

		event := &platforms.MessageEvent{Text: "test"}
		results := reg.EmitCollect(context.Background(), HookPreDispatch, event)
		if len(results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(results))
		}
		if results[0].Error != nil {
			t.Errorf("unexpected error: %v", results[0].Error)
		}
	})
}

// ---------------------------------------------------------------------------
// getMatchingHooks (wildcard)
// ---------------------------------------------------------------------------

func TestHookRegistry_getMatchingHooks(t *testing.T) {
	t.Parallel()

	t.Run("exact match only", func(t *testing.T) {
		t.Parallel()
		reg := NewHookRegistry()
		reg.Register(HookPreDispatch, func(ctx context.Context, event *platforms.MessageEvent) (*platforms.MessageEvent, error) {
			return event, nil
		})

		hooks := reg.getMatchingHooks(HookPreDispatch)
		if len(hooks) != 1 {
			t.Errorf("expected 1 hook, got %d", len(hooks))
		}

		hooks = reg.getMatchingHooks(HookPostDelivery)
		if len(hooks) != 0 {
			t.Errorf("expected 0 hooks for unmatched type, got %d", len(hooks))
		}
	})

	t.Run("wildcard match", func(t *testing.T) {
		t.Parallel()
		reg := NewHookRegistry()

		wildcardType := HookType("command:*")
		reg.Register(wildcardType, func(ctx context.Context, event *platforms.MessageEvent) (*platforms.MessageEvent, error) {
			return event, nil
		})

		hooks := reg.getMatchingHooks(HookType("command:start"))
		if len(hooks) != 1 {
			t.Errorf("expected 1 hook for wildcard match, got %d", len(hooks))
		}

		hooks = reg.getMatchingHooks(HookType("agent:start"))
		if len(hooks) != 0 {
			t.Errorf("expected 0 hooks for non-matching type, got %d", len(hooks))
		}
	})

	t.Run("exact + wildcard combined", func(t *testing.T) {
		t.Parallel()
		reg := NewHookRegistry()

		reg.Register(HookType("command:start"), func(ctx context.Context, event *platforms.MessageEvent) (*platforms.MessageEvent, error) {
			return event, nil
		})
		reg.Register(HookType("command:*"), func(ctx context.Context, event *platforms.MessageEvent) (*platforms.MessageEvent, error) {
			return event, nil
		})

		hooks := reg.getMatchingHooks(HookType("command:start"))
		if len(hooks) != 2 {
			t.Errorf("expected 2 hooks (exact + wildcard), got %d", len(hooks))
		}
	})
}

// ---------------------------------------------------------------------------
// runWithTimeout
// ---------------------------------------------------------------------------

func TestHookRegistry_runWithTimeout(t *testing.T) {
	t.Parallel()

	t.Run("completes within timeout", func(t *testing.T) {
		t.Parallel()
		reg := NewHookRegistry()
		hook := func(ctx context.Context, event *platforms.MessageEvent) (*platforms.MessageEvent, error) {
			return event, nil
		}
		event := &platforms.MessageEvent{Text: "test"}
		got, err := reg.runWithTimeout(context.Background(), hook, event, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if got.Text != "test" {
			t.Errorf("got.Text = %q, want %q", got.Text, "test")
		}
	})

	t.Run("timeout returns error", func(t *testing.T) {
		t.Parallel()
		reg := NewHookRegistry()
		hook := func(ctx context.Context, event *platforms.MessageEvent) (*platforms.MessageEvent, error) {
			time.Sleep(2 * time.Second)
			return event, nil
		}
		event := &platforms.MessageEvent{Text: "test"}
		_, err := reg.runWithTimeout(context.Background(), hook, event, 50*time.Millisecond)
		if err == nil {
			t.Fatal("expected timeout error")
		}
	})

	t.Run("hook error propagated", func(t *testing.T) {
		t.Parallel()
		reg := NewHookRegistry()
		hook := func(ctx context.Context, event *platforms.MessageEvent) (*platforms.MessageEvent, error) {
			return event, errors.New("hook error")
		}
		event := &platforms.MessageEvent{Text: "test"}
		_, err := reg.runWithTimeout(context.Background(), hook, event, time.Second)
		if err == nil || err.Error() != "hook error" {
			t.Errorf("expected hook error, got %v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// Count
// ---------------------------------------------------------------------------

func TestHookRegistry_Count(t *testing.T) {
	t.Parallel()

	reg := NewHookRegistry()
	if got := reg.Count(); got != 0 {
		t.Errorf("Count() = %d, want 0", got)
	}

	noop := func(ctx context.Context, event *platforms.MessageEvent) (*platforms.MessageEvent, error) {
		return event, nil
	}

	reg.Register(HookPreDispatch, noop)
	if got := reg.Count(); got != 1 {
		t.Errorf("Count() = %d, want 1", got)
	}

	reg.Register(HookPostDelivery, noop)
	reg.Register(HookPreDispatch, noop)
	if got := reg.Count(); got != 3 {
		t.Errorf("Count() = %d, want 3", got)
	}
}

// ---------------------------------------------------------------------------
// ListTypes
// ---------------------------------------------------------------------------

func TestHookRegistry_ListTypes(t *testing.T) {
	t.Parallel()

	reg := NewHookRegistry()
	noop := func(ctx context.Context, event *platforms.MessageEvent) (*platforms.MessageEvent, error) {
		return event, nil
	}

	reg.Register(HookPreDispatch, noop)
	reg.Register(HookPostDelivery, noop)
	reg.Register(HookPreDispatch, noop)

	types := reg.ListTypes()
	if len(types) != 2 {
		t.Fatalf("expected 2 types, got %d: %v", len(types), types)
	}

	hasPreDispatch := false
	hasPostDelivery := false
	for _, ht := range types {
		if ht == HookPreDispatch {
			hasPreDispatch = true
		}
		if ht == HookPostDelivery {
			hasPostDelivery = true
		}
	}
	if !hasPreDispatch || !hasPostDelivery {
		t.Errorf("expected pre_dispatch and post_delivery in types, got %v", types)
	}
}

// ---------------------------------------------------------------------------
// Concurrent access
// ---------------------------------------------------------------------------

func TestHookRegistry_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	reg := NewHookRegistry()
	noop := func(ctx context.Context, event *platforms.MessageEvent) (*platforms.MessageEvent, error) {
		return event, nil
	}

	var wg sync.WaitGroup
	const goroutines = 20

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reg.Register(HookPreDispatch, noop)
		}()
	}
	wg.Wait()

	if got := reg.Count(); got != goroutines {
		t.Errorf("Count() = %d, want %d", got, goroutines)
	}

	event := &platforms.MessageEvent{Text: "concurrent"}
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := reg.Run(context.Background(), HookPreDispatch, event)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	for i := 0; i < goroutines; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			reg.Count()
		}()
		go func() {
			defer wg.Done()
			reg.ListTypes()
		}()
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// EmitCollect with wildcard
// ---------------------------------------------------------------------------

func TestHookRegistry_EmitCollectWildcard(t *testing.T) {
	t.Parallel()

	reg := NewHookRegistry()
	wildcardType := HookType("command:*")

	reg.Register(wildcardType, func(ctx context.Context, event *platforms.MessageEvent) (*platforms.MessageEvent, error) {
		event.Text += "_wild"
		return event, nil
	})

	event := &platforms.MessageEvent{Text: "start"}
	results := reg.EmitCollect(context.Background(), HookType("command:execute"), event)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Event.Text != "start_wild" {
		t.Errorf("got %q, want %q", results[0].Event.Text, "start_wild")
	}
}
