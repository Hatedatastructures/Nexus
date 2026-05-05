package gateway

import (
	"testing"
	"time"

	"nexus-agent/internal/gateway/platforms"
)

func TestSessionManager_GetOrCreate(t *testing.T) {
	mgr := NewSessionManager()

	source := &platforms.SessionSource{
		Platform: platforms.PlatformTelegram,
		ChatID:   "123",
		UserID:   "user1",
		ChatType: "dm",
	}

	sess1 := mgr.GetOrCreate(source)
	if sess1 == nil {
		t.Fatal("expected non-nil session")
	}
	if sess1.AgentID == "" {
		t.Error("expected non-empty AgentID")
	}

	// 再次获取应该返回同一个 session
	sess2 := mgr.GetOrCreate(source)
	if sess1.Key != sess2.Key {
		t.Errorf("expected same session key, got %q and %q", sess1.Key, sess2.Key)
	}
}

func TestSessionManager_Get(t *testing.T) {
	mgr := NewSessionManager()

	source := &platforms.SessionSource{
		Platform: platforms.PlatformDiscord,
		ChatID:   "guild-456",
		UserID:   "user2",
		ChatType: "group",
	}

	sess := mgr.GetOrCreate(source)

	// 能够通过 key 获取
	got, ok := mgr.Get(sess.Key)
	if !ok {
		t.Error("expected to find session")
	}
	if got.Key != sess.Key {
		t.Errorf("Key = %q, want %q", got.Key, sess.Key)
	}

	// 不存在的 key
	_, ok = mgr.Get("nonexistent")
	if ok {
		t.Error("expected not to find session")
	}
}

func TestSessionManager_Reset(t *testing.T) {
	mgr := NewSessionManager()

	source := &platforms.SessionSource{
		Platform: platforms.PlatformSlack,
		ChatID:   "channel-789",
		UserID:   "user3",
		ChatType: "dm",
	}

	sess1 := mgr.GetOrCreate(source)
	sess2 := mgr.Reset(sess1.Key)

	// Reset 会重置会话，Key 保持不变
	if sess2.Key != sess1.Key {
		t.Error("expected same Key after reset")
	}
	// ResetCount 应该增加
	if sess2.ResetCount < sess1.ResetCount {
		t.Error("expected ResetCount to increase")
	}
}

func TestSessionManager_SweepExpired(t *testing.T) {
	mgr := NewSessionManager()

	source := &platforms.SessionSource{
		Platform: platforms.PlatformTelegram,
		ChatID:   "sweep-test",
		UserID:   "user4",
		ChatType: "dm",
	}

	mgr.GetOrCreate(source)

	// 立即扫描不应移除任何会话
	removed := mgr.SweepExpired(1 * time.Hour)
	if len(removed) != 0 {
		t.Errorf("expected 0 removed, got %d", len(removed))
	}

	// 负超时应该移除所有会话
	removed = mgr.SweepExpired(-1 * time.Second)
	if len(removed) == 0 {
		t.Error("expected at least 1 removed")
	}
}

func TestSessionManager_Size(t *testing.T) {
	mgr := NewSessionManager()

	if mgr.Size() != 0 {
		t.Errorf("initial size = %d, want 0", mgr.Size())
	}

	mgr.GetOrCreate(&platforms.SessionSource{
		Platform: platforms.PlatformTelegram,
		ChatID:   "size-1",
		UserID:   "u1",
		ChatType: "dm",
	})

	if mgr.Size() != 1 {
		t.Errorf("size = %d, want 1", mgr.Size())
	}
}
