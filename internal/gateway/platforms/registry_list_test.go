package platforms

import (
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// List
// ---------------------------------------------------------------------------

func TestList(t *testing.T) {
	t.Parallel()

	r := newTestRegistry()

	r.Register(&AdapterEntry{
		Platform: PlatformSlack,
		Name:     "Slack",
		Factory:  func() PlatformAdapter { return &SlackAdapter{} },
	})
	r.Register(&AdapterEntry{
		Platform: PlatformMatrix,
		Name:     "Matrix",
		Factory:  func() PlatformAdapter { return &MatrixAdapter{} },
	})

	platforms := r.List()
	if len(platforms) != 2 {
		t.Fatalf("List() returned %d, want 2", len(platforms))
	}

	found := map[Platform]bool{}
	for _, p := range platforms {
		found[p] = true
	}
	if !found[PlatformSlack] {
		t.Error("expected Slack in List result")
	}
	if !found[PlatformMatrix] {
		t.Error("expected Matrix in List result")
	}
}

func TestListEmpty(t *testing.T) {
	t.Parallel()

	r := newTestRegistry()

	platforms := r.List()
	if len(platforms) != 0 {
		t.Errorf("List() on empty registry returned %d, want 0", len(platforms))
	}
}

// ---------------------------------------------------------------------------
// GetEntry
// ---------------------------------------------------------------------------

func TestGetEntryFound(t *testing.T) {
	t.Parallel()

	r := newTestRegistry()

	r.Register(&AdapterEntry{
		Platform: PlatformWhatsApp,
		Name:     "WhatsApp",
		Factory:  func() PlatformAdapter { return NewWhatsAppAdapter("", "") },
	})

	entry := r.GetEntry(PlatformWhatsApp)
	if entry == nil {
		t.Fatal("expected entry for WhatsApp")
	}
	if entry.Name != "WhatsApp" {
		t.Errorf("entry.Name = %q, want %q", entry.Name, "WhatsApp")
	}
	if entry.Platform != PlatformWhatsApp {
		t.Errorf("entry.Platform = %q, want %q", entry.Platform, PlatformWhatsApp)
	}
}

func TestGetEntryNotFound(t *testing.T) {
	t.Parallel()

	r := newTestRegistry()

	entry := r.GetEntry(PlatformSMS)
	if entry != nil {
		t.Errorf("expected nil for unregistered platform, got %+v", entry)
	}
}

// ---------------------------------------------------------------------------
// Concurrent access safety
// ---------------------------------------------------------------------------

func TestRegistryConcurrentAccess(t *testing.T) {
	t.Parallel()

	r := newTestRegistry()

	platforms := []Platform{PlatformTelegram, PlatformDiscord, PlatformSlack, PlatformSignal, PlatformWebhook}
	for _, p := range platforms {
		p := p
		r.Register(&AdapterEntry{
			Platform: p,
			Name:     string(p),
			Factory:  func() PlatformAdapter { return &TelegramAdapter{} },
		})
	}

	var wg sync.WaitGroup
	const goroutines = 50

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.Has(PlatformTelegram)
			_ = r.List()
			_ = r.GetEntry(PlatformDiscord)
		}()
	}

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = r.Create(PlatformSlack)
		}()
	}

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			plat := Platform("test-concurrent")
			r.Register(&AdapterEntry{
				Platform: plat,
				Name:     "test",
				Factory:  func() PlatformAdapter { return &TelegramAdapter{} },
			})
		}(i)
	}

	wg.Wait()

	if !r.Has(PlatformTelegram) {
		t.Error("registry should still have Telegram after concurrent access")
	}
}

// ---------------------------------------------------------------------------
// AdapterEntry struct
// ---------------------------------------------------------------------------

func TestAdapterEntryFields(t *testing.T) {
	t.Parallel()

	factory := func() PlatformAdapter { return &TelegramAdapter{} }
	entry := &AdapterEntry{
		Platform: PlatformTelegram,
		Name:     "Telegram",
		Factory:  factory,
	}

	if entry.Platform != PlatformTelegram {
		t.Errorf("Platform = %q, want %q", entry.Platform, PlatformTelegram)
	}
	if entry.Name != "Telegram" {
		t.Errorf("Name = %q, want %q", entry.Name, "Telegram")
	}
	if entry.Factory == nil {
		t.Error("Factory should not be nil")
	}

	adapter := entry.Factory()
	if adapter == nil {
		t.Fatal("Factory() returned nil")
	}
	if adapter.Name() != "Telegram" {
		t.Errorf("Factory().Name() = %q, want %q", adapter.Name(), "Telegram")
	}
}

// ---------------------------------------------------------------------------
// AdapterFactory type
// ---------------------------------------------------------------------------

func TestAdapterFactoryType(t *testing.T) {
	t.Parallel()

	var f AdapterFactory = func() PlatformAdapter {
		return NewDiscordAdapter("test")
	}

	adapter := f()
	if adapter == nil {
		t.Fatal("AdapterFactory returned nil")
	}
	var _ = adapter
}

// ---------------------------------------------------------------------------
// NewAdapterRegistry + RegisterAllAdapters
// ---------------------------------------------------------------------------

func TestNewAdapterRegistryWithAdapters(t *testing.T) {
	t.Parallel()

	reg := NewAdapterRegistry()
	RegisterAllAdapters(reg)
	if reg == nil {
		t.Fatal("NewAdapterRegistry() returned nil")
	}
}
