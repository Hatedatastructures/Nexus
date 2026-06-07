package gateway

import (
	"context"
	"testing"

	"nexus-agent/internal/gateway/platforms"
)

// ---------------------------------------------------------------------------
// mock adapters for channel_dir tests
// ---------------------------------------------------------------------------

// mockChannelListerAdapter implements PlatformAdapter + ChannelLister
type mockChannelListerAdapter struct {
	platformType platforms.Platform
	channels     []ChannelEntry
	listErr      error
}

func (m *mockChannelListerAdapter) Name() string                     { return "mockLister" }
func (m *mockChannelListerAdapter) PlatformType() platforms.Platform { return m.platformType }
func (m *mockChannelListerAdapter) Connect(_ context.Context) (<-chan *platforms.MessageEvent, error) {
	return nil, nil
}
func (m *mockChannelListerAdapter) Disconnect(_ context.Context) error { return nil }
func (m *mockChannelListerAdapter) Send(_ context.Context, _, _ string, _ *platforms.SendOptions) (*platforms.SendResult, error) {
	return &platforms.SendResult{Success: true, MessageID: "m1"}, nil
}
func (m *mockChannelListerAdapter) EditMessage(_ context.Context, _, _, _ string) (*platforms.SendResult, error) {
	return &platforms.SendResult{Success: true}, nil
}
func (m *mockChannelListerAdapter) DeleteMessage(_ context.Context, _, _ string) error { return nil }
func (m *mockChannelListerAdapter) SendTyping(_ context.Context, _ string) error       { return nil }
func (m *mockChannelListerAdapter) SendImage(_ context.Context, _, _, _ string, _ *platforms.SendOptions) (*platforms.SendResult, error) {
	return &platforms.SendResult{Success: true, MessageID: "img1"}, nil
}
func (m *mockChannelListerAdapter) SendVoice(_ context.Context, _, _ string, _ *platforms.SendOptions) (*platforms.SendResult, error) {
	return &platforms.SendResult{Success: true, MessageID: "voice1"}, nil
}
func (m *mockChannelListerAdapter) SendVideo(_ context.Context, _, _, _ string, _ *platforms.SendOptions) (*platforms.SendResult, error) {
	return &platforms.SendResult{Success: true, MessageID: "vid1"}, nil
}
func (m *mockChannelListerAdapter) SendDocument(_ context.Context, _, _, _ string, _ *platforms.SendOptions) (*platforms.SendResult, error) {
	return &platforms.SendResult{Success: true, MessageID: "doc1"}, nil
}
func (m *mockChannelListerAdapter) MaxMessageLength() int   { return 4096 }
func (m *mockChannelListerAdapter) SupportsStreaming() bool { return false }
func (m *mockChannelListerAdapter) ListChannels(_ context.Context) ([]ChannelEntry, error) {
	return m.channels, m.listErr
}

// mockNoListerAdapter implements PlatformAdapter only (no ChannelLister)
type mockNoListerAdapter struct {
	platformType platforms.Platform
}

func (m *mockNoListerAdapter) Name() string                     { return "mockNoLister" }
func (m *mockNoListerAdapter) PlatformType() platforms.Platform { return m.platformType }
func (m *mockNoListerAdapter) Connect(_ context.Context) (<-chan *platforms.MessageEvent, error) {
	return nil, nil
}
func (m *mockNoListerAdapter) Disconnect(_ context.Context) error { return nil }
func (m *mockNoListerAdapter) Send(_ context.Context, _, _ string, _ *platforms.SendOptions) (*platforms.SendResult, error) {
	return &platforms.SendResult{Success: true}, nil
}
func (m *mockNoListerAdapter) EditMessage(_ context.Context, _, _, _ string) (*platforms.SendResult, error) {
	return &platforms.SendResult{Success: true}, nil
}
func (m *mockNoListerAdapter) DeleteMessage(_ context.Context, _, _ string) error { return nil }
func (m *mockNoListerAdapter) SendTyping(_ context.Context, _ string) error       { return nil }
func (m *mockNoListerAdapter) SendImage(_ context.Context, _, _, _ string, _ *platforms.SendOptions) (*platforms.SendResult, error) {
	return &platforms.SendResult{Success: true}, nil
}
func (m *mockNoListerAdapter) SendVoice(_ context.Context, _, _ string, _ *platforms.SendOptions) (*platforms.SendResult, error) {
	return &platforms.SendResult{Success: true}, nil
}
func (m *mockNoListerAdapter) SendVideo(_ context.Context, _, _, _ string, _ *platforms.SendOptions) (*platforms.SendResult, error) {
	return &platforms.SendResult{Success: true}, nil
}
func (m *mockNoListerAdapter) SendDocument(_ context.Context, _, _, _ string, _ *platforms.SendOptions) (*platforms.SendResult, error) {
	return &platforms.SendResult{Success: true}, nil
}
func (m *mockNoListerAdapter) MaxMessageLength() int   { return 4096 }
func (m *mockNoListerAdapter) SupportsStreaming() bool { return false }

// ---------------------------------------------------------------------------
// BuildChannelDirectory
// ---------------------------------------------------------------------------

func TestBuildChannelDirectory(t *testing.T) {
	t.Parallel()

	t.Run("empty adapters returns empty", func(t *testing.T) {
		t.Parallel()
		entries := BuildChannelDirectory(context.Background(), nil, nil)
		if len(entries) != 0 {
			t.Errorf("expected 0 entries, got %d", len(entries))
		}
	})

	t.Run("enumerates from ChannelLister", func(t *testing.T) {
		t.Parallel()
		adapter := &mockChannelListerAdapter{
			platformType: platforms.PlatformTelegram,
			channels: []ChannelEntry{
				{ChannelID: "ch1", Name: "general"},
				{ChannelID: "ch2", Name: "random"},
			},
		}
		entries := BuildChannelDirectory(context.Background(), []platforms.PlatformAdapter{adapter}, nil)
		if len(entries) != 2 {
			t.Fatalf("expected 2 entries, got %d", len(entries))
		}
		if entries[0].Platform != platforms.PlatformTelegram {
			t.Errorf("entry[0].Platform = %v, want telegram", entries[0].Platform)
		}
		if entries[0].ChannelID != "ch1" {
			t.Errorf("entry[0].ChannelID = %q, want %q", entries[0].ChannelID, "ch1")
		}
	})

	t.Run("falls back to sessions on lister error", func(t *testing.T) {
		t.Parallel()
		adapter := &mockChannelListerAdapter{
			platformType: platforms.PlatformDiscord,
			listErr:      context.DeadlineExceeded,
		}
		mgr := NewSessionManager()
		mgr.GetOrCreate(&platforms.SessionSource{
			Platform: platforms.PlatformDiscord,
			ChatID:   "sess_ch1",
			ChatName: "session-channel",
			ChatType: "group",
		})

		entries := BuildChannelDirectory(context.Background(), []platforms.PlatformAdapter{adapter}, mgr)
		if len(entries) != 1 {
			t.Fatalf("expected 1 entry from sessions, got %d", len(entries))
		}
		if entries[0].ChannelID != "sess_ch1" {
			t.Errorf("ChannelID = %q, want %q", entries[0].ChannelID, "sess_ch1")
		}
	})

	t.Run("non-Lister adapter uses sessions", func(t *testing.T) {
		t.Parallel()
		adapter := &mockNoListerAdapter{platformType: platforms.PlatformSlack}
		mgr := NewSessionManager()
		mgr.GetOrCreate(&platforms.SessionSource{
			Platform: platforms.PlatformSlack,
			ChatID:   "sl1",
			ChatName: "slack-ch",
			ChatType: "channel",
		})

		entries := BuildChannelDirectory(context.Background(), []platforms.PlatformAdapter{adapter}, mgr)
		if len(entries) != 1 {
			t.Fatalf("expected 1 entry, got %d", len(entries))
		}
		if entries[0].ChannelID != "sl1" {
			t.Errorf("ChannelID = %q, want %q", entries[0].ChannelID, "sl1")
		}
	})

	t.Run("deduplicates entries", func(t *testing.T) {
		t.Parallel()
		adapter := &mockChannelListerAdapter{
			platformType: platforms.PlatformTelegram,
			channels: []ChannelEntry{
				{ChannelID: "dup1", Name: "chan"},
			},
		}
		mgr := NewSessionManager()
		mgr.GetOrCreate(&platforms.SessionSource{
			Platform: platforms.PlatformTelegram,
			ChatID:   "dup1",
			ChatName: "chan",
		})

		// When lister succeeds, session fallback is NOT used (due to continue)
		entries := BuildChannelDirectory(context.Background(), []platforms.PlatformAdapter{adapter}, mgr)
		if len(entries) != 1 {
			t.Errorf("expected 1 entry (no dup), got %d", len(entries))
		}
	})

	t.Run("nil session manager skipped", func(t *testing.T) {
		t.Parallel()
		adapter := &mockNoListerAdapter{platformType: platforms.PlatformTelegram}
		entries := BuildChannelDirectory(context.Background(), []platforms.PlatformAdapter{adapter}, nil)
		if len(entries) != 0 {
			t.Errorf("expected 0 entries with nil session mgr, got %d", len(entries))
		}
	})

	t.Run("multiple adapters", func(t *testing.T) {
		t.Parallel()
		a1 := &mockChannelListerAdapter{
			platformType: platforms.PlatformTelegram,
			channels:     []ChannelEntry{{ChannelID: "tg1", Name: "tg-chan"}},
		}
		a2 := &mockChannelListerAdapter{
			platformType: platforms.PlatformDiscord,
			channels:     []ChannelEntry{{ChannelID: "dc1", Name: "dc-chan"}},
		}
		entries := BuildChannelDirectory(context.Background(), []platforms.PlatformAdapter{a1, a2}, nil)
		if len(entries) != 2 {
			t.Fatalf("expected 2 entries, got %d", len(entries))
		}
	})
}

// ---------------------------------------------------------------------------
// ResolveChannelName
// ---------------------------------------------------------------------------

func TestResolveChannelName(t *testing.T) {
	t.Parallel()

	entries := []ChannelEntry{
		{Platform: platforms.PlatformTelegram, ChannelID: "tg1", Name: "general"},
		{Platform: platforms.PlatformDiscord, ChannelID: "dc1", Name: "random"},
		{Platform: platforms.PlatformSlack, ChannelID: "sl1", Name: "dev-team"},
	}

	tests := []struct {
		name    string
		entries []ChannelEntry
		input   string
		wantID  string
		wantNil bool
	}{
		{"empty name returns nil", entries, "", "", true},
		{"exact name match", entries, "general", "tg1", false},
		{"case insensitive", entries, "GENERAL", "tg1", false},
		{"ChannelID exact match", entries, "dc1", "dc1", false},
		{"prefix match", entries, "dev", "sl1", false},
		{"contains match", entries, "rando", "dc1", false},
		{"no match returns nil", entries, "nonexistent", "", true},
		{"strip # prefix", entries, "#general", "tg1", false},
		{"strip @ prefix", entries, "@random", "dc1", false},
		{"exact name priority over ChannelID", entries, "tg1", "tg1", false},
		{"empty entries returns nil", nil, "general", "", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ResolveChannelName(tc.entries, tc.input)
			if tc.wantNil {
				if got != nil {
					t.Errorf("expected nil, got %+v", got)
				}
			} else {
				if got == nil {
					t.Fatalf("expected non-nil result for input %q", tc.input)
				}
				if got.ChannelID != tc.wantID {
					t.Errorf("ChannelID = %q, want %q", got.ChannelID, tc.wantID)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// normalizeChannelName
// ---------------------------------------------------------------------------

func TestNormalizeChannelName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"general", "general"},
		{"General", "general"},
		{"#general", "general"},
		{"@user", "user"},
		{" General ", "general"},
		{"#@Channel", "channel"},
		{"", ""},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got := normalizeChannelName(tc.input)
			if got != tc.want {
				t.Errorf("normalizeChannelName(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// entriesFromSessions
// ---------------------------------------------------------------------------

func TestEntriesFromSessions(t *testing.T) {
	t.Parallel()

	t.Run("filters by platform", func(t *testing.T) {
		t.Parallel()
		mgr := NewSessionManager()
		mgr.GetOrCreate(&platforms.SessionSource{
			Platform: platforms.PlatformTelegram,
			ChatID:   "tg1",
			ChatName: "tg-chan",
			ChatType: "group",
		})
		mgr.GetOrCreate(&platforms.SessionSource{
			Platform: platforms.PlatformDiscord,
			ChatID:   "dc1",
			ChatName: "dc-chan",
			ChatType: "dm",
		})

		tgEntries := entriesFromSessions(mgr, platforms.PlatformTelegram)
		if len(tgEntries) != 1 {
			t.Fatalf("expected 1 telegram entry, got %d", len(tgEntries))
		}
		if tgEntries[0].ChannelID != "tg1" {
			t.Errorf("ChannelID = %q, want %q", tgEntries[0].ChannelID, "tg1")
		}

		dcEntries := entriesFromSessions(mgr, platforms.PlatformDiscord)
		if len(dcEntries) != 1 {
			t.Fatalf("expected 1 discord entry, got %d", len(dcEntries))
		}

		slackEntries := entriesFromSessions(mgr, platforms.PlatformSlack)
		if len(slackEntries) != 0 {
			t.Errorf("expected 0 slack entries, got %d", len(slackEntries))
		}
	})

	t.Run("skips nil source", func(t *testing.T) {
		t.Parallel()
		mgr := NewSessionManager()
		mgr.mu.Lock()
		mgr.sessions["test_key"] = &Session{
			Key:    "test_key",
			Source: nil,
		}
		mgr.mu.Unlock()

		entries := entriesFromSessions(mgr, platforms.PlatformTelegram)
		if len(entries) != 0 {
			t.Errorf("expected 0 entries for nil source, got %d", len(entries))
		}
	})

	t.Run("empty session manager", func(t *testing.T) {
		t.Parallel()
		mgr := NewSessionManager()
		entries := entriesFromSessions(mgr, platforms.PlatformTelegram)
		if len(entries) != 0 {
			t.Errorf("expected 0 entries, got %d", len(entries))
		}
	})

	t.Run("sets GuildInfo from ChatType", func(t *testing.T) {
		t.Parallel()
		mgr := NewSessionManager()
		mgr.GetOrCreate(&platforms.SessionSource{
			Platform: platforms.PlatformTelegram,
			ChatID:   "tg_guild",
			ChatName: "guild-chan",
			ChatType: "supergroup",
		})

		entries := entriesFromSessions(mgr, platforms.PlatformTelegram)
		if len(entries) != 1 {
			t.Fatalf("expected 1 entry, got %d", len(entries))
		}
		if entries[0].GuildInfo != "supergroup" {
			t.Errorf("GuildInfo = %q, want %q", entries[0].GuildInfo, "supergroup")
		}
	})
}
