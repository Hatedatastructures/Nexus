// Package gateway 提供代理实例缓存。
// AgentCache 使用 LRU + TTL 策略管理 agent.AIAgent 实例的生命周期。
package gateway

import (
	"container/list"
	"context"
	"log/slog"
	"sync"
	"time"

	"nexus-agent/internal/agent"
)

// ───────────────────────────── 缓存条目 ─────────────────────────────

// cacheEntry 是 AgentCache 中的单个缓存条目。
// 包含代理实例、配置签名、访问时间和 LRU 链表节点。
type cacheEntry struct {
	agent      *agent.AIAgent // 代理实例
	signature  string         // 配置签名 (检测配置变化)
	lastAccess time.Time      // 最后访问时间
	inUse      bool           // 是否正在使用 (防止驱逐)
	element    *list.Element  // LRU 链表元素引用
}

// ───────────────────────────── 代理缓存 ─────────────────────────────

// AgentCache 使用 LRU + TTL 策略的代理实例缓存。
// 空闲超时的代理会被异步清理，LRU 条目在容量超限时被驱逐。
type AgentCache struct {
	mu      sync.Mutex
	entries map[string]*cacheEntry // key → entry
	maxSize int                     // 最大缓存条目数 (默认 128)
	idleTTL time.Duration           // 空闲超时 (默认 1 小时)
	lruList *list.List              // LRU 链表 (最近使用在前)
	wg     sync.WaitGroup           // track background goroutines
	closed bool                     // prevent use after Close
}

// NewAgentCache 创建代理缓存。
// maxSize 为最大条目数，idleTTL 为空闲超时。
func NewAgentCache(maxSize int, idleTTL time.Duration) *AgentCache {
	if maxSize <= 0 {
		maxSize = 128
	}
	if idleTTL <= 0 {
		idleTTL = time.Hour
	}
	return &AgentCache{
		entries: make(map[string]*cacheEntry),
		maxSize: maxSize,
		idleTTL: idleTTL,
		lruList: list.New(),
	}
}

// GetOrCreate 获取或创建代理实例。
// sessionKey 为会话键，factory 在缓存未命中时创建新代理并返回 (agent, signature)。
// 调用者负责在完成后调用 releaseInUse 释放 inUse 标记。
func (c *AgentCache) GetOrCreate(sessionKey string, factory func() (*agent.AIAgent, string)) (*agent.AIAgent, error) {
	// Fast path: check cache under lock
	c.mu.Lock()
	if entry, ok := c.entries[sessionKey]; ok {
		entry.lastAccess = time.Now()
		entry.inUse = true
		if entry.element != nil {
			c.lruList.MoveToFront(entry.element)
		}
		c.mu.Unlock()
		return entry.agent, nil
	}
	c.mu.Unlock()

	// Slow path: create agent outside lock (避免工厂创建串行化)
	newAgent, sig := factory()

	// Re-acquire lock to store
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check: another goroutine might have created it
	if entry, ok := c.entries[sessionKey]; ok {
		entry.lastAccess = time.Now()
		entry.inUse = true
		if entry.element != nil {
			c.lruList.MoveToFront(entry.element)
		}
		// 泄露的 agent 实例需要清理
		if newAgent != nil {
			c.wg.Add(1)
			go func() {
				defer c.wg.Done()
				defer func() {
					if r := recover(); r != nil {
						slog.Warn("agent shutdown panicked", "err", r)
					}
				}()
				newAgent.Shutdown()
			}()
		}
		return entry.agent, nil
	}

	// 容量检查: 超出上限则驱逐最少使用的条目
	if len(c.entries) >= c.maxSize {
		c.evictLRU()
	}

	entry := &cacheEntry{
		agent:      newAgent,
		signature:  sig,
		lastAccess: time.Now(),
		inUse:      true,
	}
	entry.element = c.lruList.PushFront(sessionKey)
	c.entries[sessionKey] = entry

	slog.Debug("agent cache miss, created new agent",
		"session_key", sessionKey,
		"cache_size", len(c.entries),
	)

	return newAgent, nil
}

// ReleaseInUse 释放代理的使用中标记。
// 在对话处理完成后调用，以便该条目可以被驱逐。
func (c *AgentCache) ReleaseInUse(sessionKey string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if entry, ok := c.entries[sessionKey]; ok {
		entry.inUse = false
	}
}

// SweepIdle 扫描并驱逐空闲超时的代理。
// 跳过 inUse=true 的条目。返回被驱逐的条目数。
func (c *AgentCache) SweepIdle(ctx context.Context) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	var expired []*cacheEntry

	for key, entry := range c.entries {
		if entry.inUse {
			continue // 跳过正在使用的代理
		}
		if now.Sub(entry.lastAccess) > c.idleTTL {
			expired = append(expired, entry)
			c.entries[key] = nil // 帮助 GC
			delete(c.entries, key)
			if entry.element != nil {
				c.lruList.Remove(entry.element)
			}
		}
	}

	// 异步清理被驱逐的代理
	for _, entry := range expired {
		c.wg.Add(1)
		go func(e *cacheEntry) {
			defer c.wg.Done()
			defer func() {
				if r := recover(); r != nil {
					slog.Warn("agent shutdown panicked", "err", r)
				}
			}()
			if e.agent != nil {
				e.agent.Shutdown()
			}
			slog.Debug("evicted idle agent from cache", "idle_duration", now.Sub(e.lastAccess))
		}(entry)
	}

	if len(expired) > 0 {
		slog.Info("swept idle agents from cache",
			"evicted", len(expired),
			"remaining", len(c.entries),
		)
	}

	return len(expired)
}

// EnforceCap 强制驱逐 LRU 条目至容量上限。
// 跳过 inUse=true 的条目。
func (c *AgentCache) EnforceCap() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for len(c.entries) > c.maxSize {
		if !c.evictLRU() {
			break // 没有可驱逐的条目 (全部 inUse)
		}
	}
}

// Size 返回当前缓存条目数。
func (c *AgentCache) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// Close waits for all background goroutines to finish and shuts down cached agents.
func (c *AgentCache) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.mu.Unlock()

	c.wg.Wait()

	// Shutdown all remaining agents
	c.mu.Lock()
	for key, entry := range c.entries {
		if entry.agent != nil {
			entry.agent.Shutdown()
		}
		delete(c.entries, key)
	}
	c.lruList.Init()
	c.mu.Unlock()
}

// ───────────────────────────── 内部方法 ─────────────────────────────

// evictLRU 驱逐一个最少使用的条目。
// 必须持有 c.mu 锁。跳过 inUse=true 的条目。
// 返回 true 表示成功驱逐，false 表示没有可驱逐的条目。
func (c *AgentCache) evictLRU() bool {
	// 从尾部（最久未使用）开始遍历
	for elem := c.lruList.Back(); elem != nil; elem = elem.Prev() {
		key, ok := elem.Value.(string)
		if !ok {
			continue
		}
		entry, exists := c.entries[key]
		if !exists {
			continue
		}
		if entry.inUse {
			continue // 跳过正在使用的条目
		}

		// 驱逐此条目
		delete(c.entries, key)
		c.lruList.Remove(elem)

		slog.Debug("evicted LRU agent from cache",
			"session_key", key,
			"cache_size", len(c.entries),
		)

			// 异步清理被驱逐的代理
			c.wg.Add(1)
			go func(e *cacheEntry) {
				defer c.wg.Done()
				if e.agent != nil {
					e.agent.Shutdown()
				}
			}(entry)

		return true
	}
	return false
}
