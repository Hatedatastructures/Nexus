// Package gateway 提供消息网关的会话管理。
// SessionManager 维护会话键到代理会话的映射，支持过期扫描和重置。
package gateway

import (
	"sync"
	"time"

	"nexus-agent/internal/gateway/platforms"
)

// ───────────────────────────── 网关会话 ─────────────────────────────

// Session 表示一个网关会话。
// 每个会话对应一个唯一的会话键和代理会话 ID。
type Session struct {
	Key        string                // 会话键: "agent:main:{platform}:{chat_type}:{chat_id}"
	Source     *platforms.SessionSource // 会话来源信息
	AgentID    string                // 代理会话 ID (UUID 格式)
	CreatedAt  time.Time             // 创建时间
	LastActive time.Time             // 最后活跃时间
	ResetCount int                   // 重置次数
}

// ───────────────────────────── 会话管理器 ─────────────────────────────

// SessionManager 管理所有网关会话。
// 使用读写锁保护并发访问，支持过期扫描和强制重置。
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*Session // key → Session
}

// NewSessionManager 创建会话管理器。
func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*Session),
	}
}

// GetOrCreate 获取或创建会话。
// 如果会话已存在，更新 LastActive 并返回；否则创建新会话。
func (m *SessionManager) GetOrCreate(source *platforms.SessionSource) *Session {
	key := platforms.BuildSessionKey(source)

	m.mu.Lock()
	defer m.mu.Unlock()

	if session, ok := m.sessions[key]; ok {
		session.LastActive = time.Now()
		return session
	}

	now := time.Now()
	session := &Session{
		Key:        key,
		Source:     source,
		AgentID:    newSessionID(now),
		CreatedAt:  now,
		LastActive: now,
		ResetCount: 0,
	}
	m.sessions[key] = session
	return session
}

// Get 根据会话键获取会话。
// 返回会话和是否存在的布尔值。
func (m *SessionManager) Get(key string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	session, ok := m.sessions[key]
	return session, ok
}

// Reset 强制重置会话: 生成新 AgentID，递增重置计数。
// 返回新会话，旧会话保留在历史记录中。
func (m *SessionManager) Reset(key string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	session := &Session{
		Key:        key,
		Source:     nil, // 由调用方设置
		AgentID:    newSessionID(now),
		CreatedAt:  now,
		LastActive: now,
		ResetCount: 0,
	}

	if old, ok := m.sessions[key]; ok {
		session.Source = old.Source
		session.ResetCount = old.ResetCount + 1
	}

	m.sessions[key] = session
	return session
}

// Delete 删除指定会话键的会话。
func (m *SessionManager) Delete(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.sessions, key)
}

// SweepExpired 扫描并删除空闲超时的会话。
// maxIdle 为最大空闲时长，返回被删除的会话键列表。
func (m *SessionManager) SweepExpired(maxIdle time.Duration) []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	var expired []string

	for key, session := range m.sessions {
		if now.Sub(session.LastActive) > maxIdle {
			expired = append(expired, key)
		}
	}

	for _, key := range expired {
		delete(m.sessions, key)
	}

	return expired
}

// Size 返回当前会话数量。
func (m *SessionManager) Size() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}

// ───────────────────────────── 内部辅助 ─────────────────────────────

// newSessionID 基于时间戳生成简单的会话 ID。
// 格式: "sess_" + 纳秒时间戳的十六进制表示。
func newSessionID(t time.Time) string {
	// 使用纳秒时间戳和秒数组合生成唯一 ID
	const hexDigits = "0123456789abcdef"
	n := t.UnixNano()
	buf := make([]byte, 20)
	buf[0] = 's'
	buf[1] = 'e'
	buf[2] = 's'
	buf[3] = 's'
	buf[4] = '_'
	for i := 19; i >= 5; i-- {
		buf[i] = hexDigits[n&0xf]
		n >>= 4
	}
	return string(buf)
}
