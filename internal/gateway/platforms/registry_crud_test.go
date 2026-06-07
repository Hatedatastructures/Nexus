package platforms

import (
	"testing"
)

// newTestRegistry creates a fresh registry for tests, avoiding global singleton pollution.
func newTestRegistry() *AdapterRegistry {
	return &AdapterRegistry{
		entries: make(map[Platform]*AdapterEntry),
	}
}

// ---------------------------------------------------------------------------
// Register + Has
// ---------------------------------------------------------------------------

func TestRegisterAndHas(t *testing.T) {
	t.Parallel()

	r := newTestRegistry()

	if r.Has(PlatformTelegram) {
		t.Error("empty registry should not have Telegram")
	}

	r.Register(&AdapterEntry{
		Platform: PlatformTelegram,
		Name:     "Telegram",
		Factory:  func() PlatformAdapter { return &TelegramAdapter{} },
	})

	if !r.Has(PlatformTelegram) {
		t.Error("registry should have Telegram after registration")
	}
	if r.Has(PlatformDiscord) {
		t.Error("registry should not have Discord when only Telegram registered")
	}
}

// ---------------------------------------------------------------------------
// Register overwrites on conflict
// ---------------------------------------------------------------------------

func TestRegisterOverwrite(t *testing.T) {
	t.Parallel()

	r := newTestRegistry()

	r.Register(&AdapterEntry{
		Platform: PlatformTelegram,
		Name:     "First",
		Factory:  func() PlatformAdapter { return &TelegramAdapter{} },
	})
	r.Register(&AdapterEntry{
		Platform: PlatformTelegram,
		Name:     "Second",
		Factory:  func() PlatformAdapter { return &TelegramAdapter{} },
	})

	entry := r.GetEntry(PlatformTelegram)
	if entry == nil {
		t.Fatal("expected entry for Telegram")
	}
	if entry.Name != "Second" {
		t.Errorf("Name = %q, want %q (second registration should win)", entry.Name, "Second")
	}
}

// ---------------------------------------------------------------------------
// Create
// ---------------------------------------------------------------------------

func TestCreateSuccess(t *testing.T) {
	t.Parallel()

	r := newTestRegistry()

	r.Register(&AdapterEntry{
		Platform: PlatformDiscord,
		Name:     "Discord",
		Factory:  func() PlatformAdapter { return NewDiscordAdapter("tok") },
	})

	adapter, err := r.Create(PlatformDiscord)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if adapter == nil {
		t.Fatal("Create() returned nil adapter")
	}
	if adapter.Name() != "Discord" {
		t.Errorf("adapter.Name() = %q, want %q", adapter.Name(), "Discord")
	}
	if adapter.PlatformType() != PlatformDiscord {
		t.Errorf("adapter.PlatformType() = %q, want %q", adapter.PlatformType(), PlatformDiscord)
	}
}

func TestCreateUnregistered(t *testing.T) {
	t.Parallel()

	r := newTestRegistry()

	_, err := r.Create(PlatformEmail)
	if err == nil {
		t.Fatal("expected error for unregistered platform")
	}
}

func TestCreateNilFactory(t *testing.T) {
	t.Parallel()

	r := newTestRegistry()

	r.Register(&AdapterEntry{
		Platform: PlatformEmail,
		Name:     "Broken",
		Factory:  func() PlatformAdapter { return nil },
	})

	_, err := r.Create(PlatformEmail)
	if err == nil {
		t.Fatal("expected error when factory returns nil")
	}
}

// ---------------------------------------------------------------------------
// CreateAll
// ---------------------------------------------------------------------------

func TestCreateAll(t *testing.T) {
	t.Parallel()

	r := newTestRegistry()

	r.Register(&AdapterEntry{
		Platform: PlatformTelegram,
		Name:     "Telegram",
		Factory:  func() PlatformAdapter { return &TelegramAdapter{} },
	})
	r.Register(&AdapterEntry{
		Platform: PlatformSignal,
		Name:     "Signal",
		Factory:  func() PlatformAdapter { return NewSignalAdapter(nil) },
	})

	adapters := r.CreateAll()
	if len(adapters) != 2 {
		t.Fatalf("CreateAll() returned %d adapters, want 2", len(adapters))
	}

	names := map[string]bool{}
	for _, a := range adapters {
		names[a.Name()] = true
	}
	if !names["Telegram"] {
		t.Error("expected Telegram adapter in CreateAll result")
	}
	if !names["Signal"] {
		t.Error("expected Signal adapter in CreateAll result")
	}
}

func TestCreateAllEmpty(t *testing.T) {
	t.Parallel()

	r := newTestRegistry()

	adapters := r.CreateAll()
	if len(adapters) != 0 {
		t.Errorf("CreateAll() on empty registry returned %d, want 0", len(adapters))
	}
}

func TestCreateAllSkipsNilFactory(t *testing.T) {
	t.Parallel()

	r := newTestRegistry()

	r.Register(&AdapterEntry{
		Platform: PlatformTelegram,
		Name:     "Telegram",
		Factory:  func() PlatformAdapter { return &TelegramAdapter{} },
	})
	r.Register(&AdapterEntry{
		Platform: PlatformEmail,
		Name:     "Broken",
		Factory:  func() PlatformAdapter { return nil },
	})

	adapters := r.CreateAll()
	if len(adapters) != 1 {
		t.Fatalf("CreateAll() returned %d adapters, want 1 (nil factory skipped)", len(adapters))
	}
	if adapters[0].Name() != "Telegram" {
		t.Errorf("adapter.Name() = %q, want %q", adapters[0].Name(), "Telegram")
	}
}
