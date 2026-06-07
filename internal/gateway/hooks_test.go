package gateway

import (
	"context"
	"errors"
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
