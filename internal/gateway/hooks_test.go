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
// isWildcardMatch
// ---------------------------------------------------------------------------

func TestIsWildcardMatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		pattern   string
		eventType string
		want      bool
	}{
		{"exact match no wildcard", "command:start", "command:start", false},
		{"wildcard matches prefix", "command:*", "command:start", true},
		{"wildcard matches empty suffix", "command:*", "command:", true},
		{"wildcard no match different prefix", "command:*", "event:start", false},
		{"no wildcard suffix returns false", "command:abc", "command:xyz", false},
		{"empty pattern no wildcard", "", "anything", false},
		{"wildcard matches deep event", "agent:*", "agent:step", true},
		{"partial prefix no match", "cmd:*", "command:start", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := isWildcardMatch(tc.pattern, tc.eventType)
			if got != tc.want {
				t.Errorf("isWildcardMatch(%q, %q) = %v, want %v", tc.pattern, tc.eventType, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// NewHookRegistry
// ---------------------------------------------------------------------------

func TestNewHookRegistry(t *testing.T) {
	t.Parallel()

	reg := NewHookRegistry()
	if reg == nil {
		t.Fatal("expected non-nil registry")
	}
	if reg.Count() != 0 {
		t.Errorf("new registry should have 0 hooks, got %d", reg.Count())
	}
	if types := reg.ListTypes(); len(types) != 0 {
		t.Errorf("new registry should have no types, got %v", types)
	}
}

// ---------------------------------------------------------------------------
// Register / RegisterWithConfig
// ---------------------------------------------------------------------------

func TestHookRegistry_Register(t *testing.T) {
	t.Parallel()

	reg := NewHookRegistry()
	called := false
	hook := func(ctx context.Context, event *platforms.MessageEvent) (*platforms.MessageEvent, error) {
		called = true
		return event, nil
	}

	reg.Register(HookPreDispatch, hook)
	if reg.Count() != 1 {
		t.Errorf("Count() = %d, want 1", reg.Count())
	}

	types := reg.ListTypes()
	if len(types) != 1 || types[0] != HookPreDispatch {
		t.Errorf("ListTypes() = %v, want [pre_dispatch]", types)
	}

	// Verify hook is callable via Run
	event := &platforms.MessageEvent{Text: "hello"}
	got, err := reg.Run(context.Background(), HookPreDispatch, event)
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("hook was not called")
	}
	if got.Text != "hello" {
		t.Errorf("got.Text = %q, want %q", got.Text, "hello")
	}
}

func TestHookRegistry_RegisterWithConfig(t *testing.T) {
	t.Parallel()

	reg := NewHookRegistry()
	called := false
	hook := func(ctx context.Context, event *platforms.MessageEvent) (*platforms.MessageEvent, error) {
		called = true
		return event, nil
	}

	reg.RegisterWithConfig(HookPostDelivery, hook, HookConfig{Timeout: 5 * time.Second})
	if reg.Count() != 1 {
		t.Errorf("Count() = %d, want 1", reg.Count())
	}

	event := &platforms.MessageEvent{Text: "test"}
	got, err := reg.Run(context.Background(), HookPostDelivery, event)
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("hook was not called")
	}
	if got.Text != "test" {
		t.Errorf("got.Text = %q, want %q", got.Text, "test")
	}
}

func TestHookRegistry_RegisterMultiple(t *testing.T) {
	t.Parallel()

	reg := NewHookRegistry()
	var order []int

	reg.Register(HookPreDispatch, func(ctx context.Context, event *platforms.MessageEvent) (*platforms.MessageEvent, error) {
		order = append(order, 1)
		event.Text += "_1"
		return event, nil
	})
	reg.Register(HookPreDispatch, func(ctx context.Context, event *platforms.MessageEvent) (*platforms.MessageEvent, error) {
		order = append(order, 2)
		event.Text += "_2"
		return event, nil
	})

	if reg.Count() != 2 {
		t.Errorf("Count() = %d, want 2", reg.Count())
	}

	event := &platforms.MessageEvent{Text: "start"}
	got, err := reg.Run(context.Background(), HookPreDispatch, event)
	if err != nil {
		t.Fatal(err)
	}

	if len(order) != 2 || order[0] != 1 || order[1] != 2 {
		t.Errorf("execution order = %v, want [1 2]", order)
	}
	if got.Text != "start_1_2" {
		t.Errorf("got.Text = %q, want %q", got.Text, "start_1_2")
	}
}

// ---------------------------------------------------------------------------
// Run
// ---------------------------------------------------------------------------

func TestHookRegistry_Run(t *testing.T) {
	t.Parallel()

	t.Run("no hooks returns event unchanged", func(t *testing.T) {
		t.Parallel()
		reg := NewHookRegistry()
		event := &platforms.MessageEvent{Text: "hello"}
		got, err := reg.Run(context.Background(), HookPreDispatch, event)
		if err != nil {
			t.Fatal(err)
		}
		if got != event {
			t.Error("expected same event pointer when no hooks")
		}
	})

	t.Run("hook modifies event", func(t *testing.T) {
		t.Parallel()
		reg := NewHookRegistry()
		reg.Register(HookPreDispatch, func(ctx context.Context, event *platforms.MessageEvent) (*platforms.MessageEvent, error) {
			event.Text = "modified"
			return event, nil
		})
		event := &platforms.MessageEvent{Text: "original"}
		got, err := reg.Run(context.Background(), HookPreDispatch, event)
		if err != nil {
			t.Fatal(err)
		}
		if got.Text != "modified" {
			t.Errorf("got.Text = %q, want %q", got.Text, "modified")
		}
	})

	t.Run("hook returns error stops chain", func(t *testing.T) {
		t.Parallel()
		reg := NewHookRegistry()
		secondCalled := false

		reg.Register(HookPreDispatch, func(ctx context.Context, event *platforms.MessageEvent) (*platforms.MessageEvent, error) {
			return event, errors.New("hook failed")
		})
		reg.Register(HookPreDispatch, func(ctx context.Context, event *platforms.MessageEvent) (*platforms.MessageEvent, error) {
			secondCalled = true
			return event, nil
		})

		event := &platforms.MessageEvent{Text: "test"}
		_, err := reg.Run(context.Background(), HookPreDispatch, event)
		if err == nil {
			t.Fatal("expected error")
		}
		if err.Error() != "hook failed" {
			t.Errorf("err = %q, want %q", err.Error(), "hook failed")
		}
		if secondCalled {
			t.Error("second hook should not be called after error")
		}
	})

	t.Run("hook returns nil event", func(t *testing.T) {
		t.Parallel()
		reg := NewHookRegistry()
		secondCalled := false

		reg.Register(HookPreDispatch, func(ctx context.Context, event *platforms.MessageEvent) (*platforms.MessageEvent, error) {
			return nil, nil
		})
		reg.Register(HookPreDispatch, func(ctx context.Context, event *platforms.MessageEvent) (*platforms.MessageEvent, error) {
			secondCalled = true
			return event, nil
		})

		event := &platforms.MessageEvent{Text: "test"}
		got, err := reg.Run(context.Background(), HookPreDispatch, event)
		if err != nil {
			t.Fatal(err)
		}
		if got != nil {
			t.Error("expected nil event after hook returns nil")
		}
		if secondCalled {
			t.Error("second hook should not be called after nil event")
		}
	})

	t.Run("with timeout config", func(t *testing.T) {
		t.Parallel()
		reg := NewHookRegistry()
		reg.RegisterWithConfig(HookPreDispatch, func(ctx context.Context, event *platforms.MessageEvent) (*platforms.MessageEvent, error) {
			return event, nil
		}, HookConfig{Timeout: 2 * time.Second})

		event := &platforms.MessageEvent{Text: "test"}
		got, err := reg.Run(context.Background(), HookPreDispatch, event)
		if err != nil {
			t.Fatal(err)
		}
		if got.Text != "test" {
			t.Errorf("got.Text = %q, want %q", got.Text, "test")
		}
	})

	t.Run("cancelled context with timeout", func(t *testing.T) {
		t.Parallel()
		reg := NewHookRegistry()
		reg.RegisterWithConfig(HookPreDispatch, func(ctx context.Context, event *platforms.MessageEvent) (*platforms.MessageEvent, error) {
			time.Sleep(2 * time.Second)
			return event, nil
		}, HookConfig{Timeout: 100 * time.Millisecond})

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		event := &platforms.MessageEvent{Text: "test"}
		_, err := reg.Run(ctx, HookPreDispatch, event)
		if err == nil {
			t.Error("expected timeout error")
		}
	})
}

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
