package plugin

import (
	"context"
	"sync"
	"testing"

	"nexus-agent/internal/tool"
)

// ─── Registry construction ───

func TestNewRegistry(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	if r == nil {
		t.Fatal("NewRegistry() returned nil")
	}
	if r.Size() != 0 {
		t.Errorf("Size() = %d, want 0", r.Size())
	}
}

// ─── Register ───

func TestRegistry_RegisterAndGet(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	p := &stubPlugin{name: "test-plugin", version: "1.0.0"}

	r.Register("test-plugin", p)

	got, ok := r.Get("test-plugin")
	if !ok {
		t.Fatal("Get() returned not ok")
	}
	if got.Name() != "test-plugin" {
		t.Errorf("Get().Name() = %q, want %q", got.Name(), "test-plugin")
	}
}

func TestRegistry_RegisterOverwrite(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	p1 := &stubPlugin{name: "p", version: "1.0.0"}
	p2 := &stubPlugin{name: "p", version: "2.0.0"}

	r.Register("p", p1)
	r.Register("p", p2)

	got, ok := r.Get("p")
	if !ok {
		t.Fatal("Get() returned not ok")
	}
	if got.Version() != "2.0.0" {
		t.Errorf("overwritten plugin version = %q, want %q", got.Version(), "2.0.0")
	}
	if r.Size() != 1 {
		t.Errorf("Size() = %d, want 1", r.Size())
	}
}

func TestRegistry_RegisterMultiple(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	r.Register("a", &stubPlugin{name: "a"})
	r.Register("b", &stubPlugin{name: "b"})
	r.Register("c", &stubPlugin{name: "c"})

	if r.Size() != 3 {
		t.Errorf("Size() = %d, want 3", r.Size())
	}
}

// ─── Unregister ───

func TestRegistry_Unregister(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	p := &stubPlugin{name: "to-remove"}
	r.Register("to-remove", p)

	removed := r.Unregister("to-remove")
	if removed == nil {
		t.Fatal("Unregister() returned nil")
	}
	if removed.Name() != "to-remove" {
		t.Errorf("removed.Name() = %q", removed.Name())
	}
	if r.Has("to-remove") {
		t.Error("Has() = true after unregister")
	}
}

func TestRegistry_UnregisterNonExistent(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	removed := r.Unregister("nonexistent")
	if removed != nil {
		t.Errorf("Unregister() = %v, want nil", removed)
	}
}

// ─── Get ───

func TestRegistry_GetNonExistent(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	_, ok := r.Get("absent")
	if ok {
		t.Error("Get() = true for non-existent plugin")
	}
}

// ─── Has ───

func TestRegistry_Has(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	r.Register("exists", &stubPlugin{name: "exists"})

	if !r.Has("exists") {
		t.Error("Has() = false for registered plugin")
	}
	if r.Has("nope") {
		t.Error("Has() = true for unregistered plugin")
	}
}

// ─── List ───

func TestRegistry_List(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	r.Register("alpha", &stubPlugin{name: "alpha"})
	r.Register("beta", &stubPlugin{name: "beta"})

	names := r.List()
	if len(names) != 2 {
		t.Fatalf("List() len = %d, want 2", len(names))
	}

	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	if !set["alpha"] || !set["beta"] {
		t.Errorf("List() = %v, missing expected entries", names)
	}
}

func TestRegistry_ListEmpty(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	names := r.List()
	if len(names) != 0 {
		t.Errorf("List() len = %d, want 0", len(names))
	}
}

// ─── Size ───

func TestRegistry_Size(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	if r.Size() != 0 {
		t.Errorf("Size() = %d, want 0", r.Size())
	}
	r.Register("a", &stubPlugin{name: "a"})
	if r.Size() != 1 {
		t.Errorf("Size() = %d, want 1", r.Size())
	}
	r.Unregister("a")
	if r.Size() != 0 {
		t.Errorf("Size() = %d, want 0 after unregister", r.Size())
	}
}

// ─── Range ───

func TestRegistry_Range(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	r.Register("x", &stubPlugin{name: "x", version: "1.0"})
	r.Register("y", &stubPlugin{name: "y", version: "2.0"})

	visited := map[string]string{}
	r.Range(func(name string, plugin Plugin) bool {
		visited[name] = plugin.Version()
		return true
	})

	if len(visited) != 2 {
		t.Errorf("Range visited %d entries, want 2", len(visited))
	}
	if visited["x"] != "1.0" {
		t.Errorf("visited[x] = %q", visited["x"])
	}
	if visited["y"] != "2.0" {
		t.Errorf("visited[y] = %q", visited["y"])
	}
}

func TestRegistry_RangeStopEarly(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	r.Register("a", &stubPlugin{name: "a"})
	r.Register("b", &stubPlugin{name: "b"})
	r.Register("c", &stubPlugin{name: "c"})

	count := 0
	r.Range(func(name string, plugin Plugin) bool {
		count++
		return false // stop immediately
	})

	if count != 1 {
		t.Errorf("Range visited %d entries, want 1 (early stop)", count)
	}
}

// ─── CollectTools ───

func TestRegistry_CollectTools(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	r.Register("normal", &stubPlugin{name: "normal"})
	r.Register("tooler", &toolProviderPlugin{
		stubPlugin: stubPlugin{name: "tooler"},
		tools:      []tool.Tool{&stubTool{name: "t1"}, &stubTool{name: "t2"}},
	})

	result := r.CollectTools()
	if len(result) != 1 {
		t.Fatalf("CollectTools() has %d entries, want 1", len(result))
	}
	names := result["tooler"]
	if len(names) != 2 {
		t.Fatalf("tool names = %d, want 2", len(names))
	}
}

func TestRegistry_CollectToolsEmpty(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	result := r.CollectTools()
	if len(result) != 0 {
		t.Errorf("CollectTools() = %v, want empty", result)
	}
}

// ─── CollectHooks ───

func TestRegistry_CollectHooks(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	r.Register("normal", &stubPlugin{name: "normal"})
	r.Register("hooker", &hookProviderPlugin{
		stubPlugin: stubPlugin{name: "hooker"},
		hooks: map[string][]HookHandler{
			"pre_dispatch":  {func(_ context.Context, _ any) (any, error) { return nil, nil }},
			"post_delivery": {func(_ context.Context, _ any) (any, error) { return nil, nil }},
		},
	})

	result := r.CollectHooks()
	if len(result) != 1 {
		t.Fatalf("CollectHooks() has %d entries, want 1", len(result))
	}
	events := result["hooker"]
	if len(events) != 2 {
		t.Fatalf("event types = %d, want 2", len(events))
	}
}

func TestRegistry_CollectHooksEmpty(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	result := r.CollectHooks()
	if len(result) != 0 {
		t.Errorf("CollectHooks() = %v, want empty", result)
	}
}

// ─── Summary ───

func TestRegistry_Summary(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	s := r.Summary()
	if s == "" {
		t.Error("Summary() returned empty string")
	}

	r.Register("p", &stubPlugin{name: "p"})
	s2 := r.Summary()
	if s2 == "" {
		t.Error("Summary() returned empty after register")
	}
}

// ─── Concurrency safety ───

func TestRegistry_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	var wg sync.WaitGroup

	for i := range 50 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := string(rune('a' + i%26))
			r.Register(name, &stubPlugin{name: name})
			_ = r.Has(name)
			_ = r.Size()
			_ = r.List()
		}(i)
	}
	wg.Wait()

	if r.Size() == 0 {
		t.Error("expected some plugins registered after concurrent access")
	}
}
