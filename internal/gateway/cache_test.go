package gateway

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"nexus-agent/internal/agent"
)

// ---------------------------------------------------------------------------
// cache tests
// ---------------------------------------------------------------------------

func TestNewAgentCache(t *testing.T) {
	t.Parallel()

	t.Run("applies defaults when zero", func(t *testing.T) {
		t.Parallel()
		c := NewAgentCache(0, 0)
		if c.maxSize != 128 {
			t.Errorf("maxSize = %d, want 128", c.maxSize)
		}
		if c.idleTTL != time.Hour {
			t.Errorf("idleTTL = %v, want 1h", c.idleTTL)
		}
	})

	t.Run("uses provided values", func(t *testing.T) {
		t.Parallel()
		c := NewAgentCache(50, 30*time.Minute)
		if c.maxSize != 50 {
			t.Errorf("maxSize = %d, want 50", c.maxSize)
		}
		if c.idleTTL != 30*time.Minute {
			t.Errorf("idleTTL = %v, want 30m", c.idleTTL)
		}
	})
}

func TestAgentCache_GetOrCreate(t *testing.T) {
	t.Parallel()

	t.Run("creates new on cache miss", func(t *testing.T) {
		t.Parallel()
		c := NewAgentCache(10, time.Hour)
		var created atomic.Bool
		a, err := c.GetOrCreate("key1", func() (*agent.AIAgent, string) {
			created.Store(true)
			return agent.NewAgent(), "sig1"
		})
		if err != nil {
			t.Fatal(err)
		}
		if a == nil {
			t.Fatal("expected non-nil agent")
		}
		if !created.Load() {
			t.Error("expected factory to be called")
		}
	})

	t.Run("returns cached on hit", func(t *testing.T) {
		t.Parallel()
		c := NewAgentCache(10, time.Hour)
		var calls atomic.Int32
		factory := func() (*agent.AIAgent, string) {
			calls.Add(1)
			return agent.NewAgent(), "sig1"
		}

		a1, _ := c.GetOrCreate("key1", factory)
		c.ReleaseInUse("key1")
		a2, _ := c.GetOrCreate("key1", factory)

		if calls.Load() != 1 {
			t.Errorf("factory calls = %d, want 1", calls.Load())
		}
		if a1 != a2 {
			t.Error("expected same agent instance")
		}
	})

	t.Run("double-create race: second factory agent gets shut down", func(t *testing.T) {
		t.Parallel()
		c := NewAgentCache(10, time.Hour)
		first := agent.NewAgent()
		second := agent.NewAgent()

		_, _ = c.GetOrCreate("race_key", func() (*agent.AIAgent, string) {
			return first, "sig"
		})
		c.ReleaseInUse("race_key")

		a, _ := c.GetOrCreate("race_key", func() (*agent.AIAgent, string) {
			return second, "sig"
		})
		if a != first {
			t.Error("expected original agent to be returned")
		}
	})

	t.Run("evicts LRU when full", func(t *testing.T) {
		t.Parallel()
		c := NewAgentCache(2, time.Hour)

		_, _ = c.GetOrCreate("k1", func() (*agent.AIAgent, string) { return agent.NewAgent(), "s1" })
		c.ReleaseInUse("k1")
		_, _ = c.GetOrCreate("k2", func() (*agent.AIAgent, string) { return agent.NewAgent(), "s2" })
		c.ReleaseInUse("k2")

		_, _ = c.GetOrCreate("k3", func() (*agent.AIAgent, string) { return agent.NewAgent(), "s3" })
		c.ReleaseInUse("k3")

		if c.Size() > 2 {
			t.Errorf("size = %d, expected <= 2", c.Size())
		}
	})
}

func TestAgentCache_ReleaseInUse(t *testing.T) {
	t.Parallel()

	t.Run("releases in-use flag", func(t *testing.T) {
		t.Parallel()
		c := NewAgentCache(10, time.Hour)
		_, _ = c.GetOrCreate("r1", func() (*agent.AIAgent, string) {
			return agent.NewAgent(), "s"
		})

		c.ReleaseInUse("r1")

		c.mu.Lock()
		entry := c.entries["r1"]
		c.mu.Unlock()
		if entry.inUse {
			t.Error("expected inUse = false after release")
		}
	})

	t.Run("non-existent key is no-op", func(t *testing.T) {
		t.Parallel()
		c := NewAgentCache(10, time.Hour)
		c.ReleaseInUse("nonexistent")
	})
}

func TestAgentCache_SweepIdle(t *testing.T) {
	t.Parallel()

	t.Run("sweeps expired idle entries", func(t *testing.T) {
		t.Parallel()
		c := NewAgentCache(10, 1*time.Nanosecond)

		_, _ = c.GetOrCreate("sw1", func() (*agent.AIAgent, string) {
			return agent.NewAgent(), "s"
		})
		c.ReleaseInUse("sw1")

		time.Sleep(10 * time.Millisecond)

		evicted := c.SweepIdle(context.Background())
		if evicted != 1 {
			t.Errorf("evicted = %d, want 1", evicted)
		}
		if c.Size() != 0 {
			t.Errorf("size = %d, want 0", c.Size())
		}
	})

	t.Run("skips in-use entries", func(t *testing.T) {
		t.Parallel()
		c := NewAgentCache(10, 1*time.Nanosecond)

		_, _ = c.GetOrCreate("sw2", func() (*agent.AIAgent, string) {
			return agent.NewAgent(), "s"
		})

		time.Sleep(10 * time.Millisecond)

		evicted := c.SweepIdle(context.Background())
		if evicted != 0 {
			t.Errorf("evicted = %d, want 0 (in-use)", evicted)
		}
	})

	t.Run("no expired entries returns 0", func(t *testing.T) {
		t.Parallel()
		c := NewAgentCache(10, time.Hour)
		_, _ = c.GetOrCreate("sw3", func() (*agent.AIAgent, string) {
			return agent.NewAgent(), "s"
		})
		c.ReleaseInUse("sw3")

		evicted := c.SweepIdle(context.Background())
		if evicted != 0 {
			t.Errorf("evicted = %d, want 0", evicted)
		}
	})

	t.Run("empty cache returns 0", func(t *testing.T) {
		t.Parallel()
		c := NewAgentCache(10, time.Hour)
		evicted := c.SweepIdle(context.Background())
		if evicted != 0 {
			t.Errorf("evicted = %d, want 0", evicted)
		}
	})
}

func TestAgentCache_EnforceCap(t *testing.T) {
	t.Parallel()

	t.Run("evicts entries above max", func(t *testing.T) {
		t.Parallel()
		c := NewAgentCache(2, time.Hour)

		_, _ = c.GetOrCreate("ec1", func() (*agent.AIAgent, string) { return agent.NewAgent(), "s" })
		c.ReleaseInUse("ec1")
		_, _ = c.GetOrCreate("ec2", func() (*agent.AIAgent, string) { return agent.NewAgent(), "s" })
		c.ReleaseInUse("ec2")
		_, _ = c.GetOrCreate("ec3", func() (*agent.AIAgent, string) { return agent.NewAgent(), "s" })
		c.ReleaseInUse("ec3")

		c.EnforceCap()
		if c.Size() > 2 {
			t.Errorf("size = %d, expected <= 2 after EnforceCap", c.Size())
		}
	})

	t.Run("no eviction when under cap", func(t *testing.T) {
		t.Parallel()
		c := NewAgentCache(10, time.Hour)
		_, _ = c.GetOrCreate("ec4", func() (*agent.AIAgent, string) { return agent.NewAgent(), "s" })
		c.ReleaseInUse("ec4")

		c.EnforceCap()
		if c.Size() != 1 {
			t.Errorf("size = %d, want 1", c.Size())
		}
	})

	t.Run("all in-use: no eviction possible", func(t *testing.T) {
		t.Parallel()
		c := NewAgentCache(1, time.Hour)
		_, _ = c.GetOrCreate("ec5", func() (*agent.AIAgent, string) { return agent.NewAgent(), "s" })
		_, _ = c.GetOrCreate("ec6", func() (*agent.AIAgent, string) { return agent.NewAgent(), "s" })

		c.EnforceCap()
	})
}

func TestAgentCache_Size(t *testing.T) {
	t.Parallel()

	t.Run("starts at 0", func(t *testing.T) {
		t.Parallel()
		c := NewAgentCache(10, time.Hour)
		if c.Size() != 0 {
			t.Errorf("initial size = %d, want 0", c.Size())
		}
	})

	t.Run("increments on create", func(t *testing.T) {
		t.Parallel()
		c := NewAgentCache(10, time.Hour)
		_, _ = c.GetOrCreate("sz1", func() (*agent.AIAgent, string) { return agent.NewAgent(), "s" })
		if c.Size() != 1 {
			t.Errorf("size = %d, want 1", c.Size())
		}
	})
}

func TestAgentCache_evictLRU(t *testing.T) {
	t.Parallel()

	t.Run("evicts least recently used", func(t *testing.T) {
		t.Parallel()
		c := NewAgentCache(10, time.Hour)

		_, _ = c.GetOrCreate("lru1", func() (*agent.AIAgent, string) { return agent.NewAgent(), "s" })
		c.ReleaseInUse("lru1")
		_, _ = c.GetOrCreate("lru2", func() (*agent.AIAgent, string) { return agent.NewAgent(), "s" })
		c.ReleaseInUse("lru2")

		c.mu.Lock()
		ok := c.evictLRU()
		c.mu.Unlock()

		if !ok {
			t.Fatal("expected eviction to succeed")
		}
		if c.Size() != 1 {
			t.Errorf("size = %d, want 1", c.Size())
		}
	})

	t.Run("returns false when all in use", func(t *testing.T) {
		t.Parallel()
		c := NewAgentCache(10, time.Hour)
		_, _ = c.GetOrCreate("lru3", func() (*agent.AIAgent, string) { return agent.NewAgent(), "s" })

		c.mu.Lock()
		ok := c.evictLRU()
		c.mu.Unlock()

		if ok {
			t.Error("expected false when all entries in use")
		}
	})

	t.Run("returns false on empty cache", func(t *testing.T) {
		t.Parallel()
		c := NewAgentCache(10, time.Hour)

		c.mu.Lock()
		ok := c.evictLRU()
		c.mu.Unlock()

		if ok {
			t.Error("expected false on empty cache")
		}
	})
}
