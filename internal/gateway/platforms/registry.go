// Package platforms 提供平台适配器自动注册中心。
// 所有适配器通过 init() 函数自注册到全局注册中心。
// RegisterFromRegistry 在 runner 层获取所有已注册适配器并创建实例。
package platforms

import (
	"fmt"
	"log/slog"
	"sync"
)

// ───────────────────────────── 注册中心类型 ─────────────────────────────

// AdapterFactory 是平台适配器工厂函数。
// 返回一个新的、未配置的适配器实例。
type AdapterFactory func() PlatformAdapter

// AdapterEntry 注册中心中的适配器条目。
type AdapterEntry struct {
	Platform Platform      // 平台类型枚举
	Name     string        // 适配器显示名称
	Factory  AdapterFactory // 工厂函数
}

// AdapterRegistry 平台适配器注册中心，并发安全。
// 所有适配器通过 Register 方法注册到此中心。
type AdapterRegistry struct {
	mu      sync.RWMutex
	entries map[Platform]*AdapterEntry
}

// 全局注册中心单例
var defaultRegistry = &AdapterRegistry{
	entries: make(map[Platform]*AdapterEntry),
}

// GetRegistry 返回全局注册中心实例。
func GetRegistry() *AdapterRegistry {
	return defaultRegistry
}

// ───────────────────────────── 可配置适配器接口 ─────────────────────────────

// ConfigurableAdapter 支持配置注入的适配器扩展接口。
// 适配器工厂创建实例后，通过 Configure 方法注入平台配置参数。
type ConfigurableAdapter interface {
	// Configure 注入平台配置参数。
	// settings 来自 config.PlatformEntry 的 Settings 和 Token 字段。
	// Token 字段以 "token" 键传入。
	Configure(settings map[string]any) error
}

// ───────────────────────────── 注册 ─────────────────────────────

// Register 向注册中心注册一个适配器工厂。
// 通常在 init() 函数中调用。
// 如果平台类型冲突，后注册的会覆盖先注册的 (预警日志)。
func (r *AdapterRegistry) Register(entry *AdapterEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.entries[entry.Platform]; exists {
		slog.Warn("platform type conflict, overwriting registration",
			"platform", string(entry.Platform),
			"name", entry.Name,
		)
	}

	r.entries[entry.Platform] = entry
	slog.Debug("platform adapter registered",
		"platform", string(entry.Platform),
		"name", entry.Name,
	)
}

// ───────────────────────────── 创建 ─────────────────────────────

// Create 创建指定平台的适配器实例。
// 如果平台未注册，返回错误。
func (r *AdapterRegistry) Create(platform Platform) (PlatformAdapter, error) {
	r.mu.RLock()
	entry, exists := r.entries[platform]
	r.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("未注册的平台适配器: %s", platform)
	}

	adapter := entry.Factory()
	if adapter == nil {
		return nil, fmt.Errorf("平台适配器工厂返回 nil: %s", platform)
	}

	return adapter, nil
}

// CreateAll 创建所有已注册平台的适配器实例。
func (r *AdapterRegistry) CreateAll() []PlatformAdapter {
	r.mu.RLock()
	entries := make([]*AdapterEntry, 0, len(r.entries))
	for _, entry := range r.entries {
		entries = append(entries, entry)
	}
	r.mu.RUnlock()

	adapters := make([]PlatformAdapter, 0, len(entries))
	for _, entry := range entries {
		adapter := entry.Factory()
		if adapter != nil {
			adapters = append(adapters, adapter)
		} else {
			slog.Warn("platform adapter factory returned nil, skipping",
				"platform", string(entry.Platform),
				"name", entry.Name,
			)
		}
	}

	return adapters
}

// ───────────────────────────── 查询 ─────────────────────────────

// List 返回所有已注册的平台类型列表。
func (r *AdapterRegistry) List() []Platform {
	r.mu.RLock()
	defer r.mu.RUnlock()

	platforms := make([]Platform, 0, len(r.entries))
	for p := range r.entries {
		platforms = append(platforms, p)
	}
	return platforms
}

// GetEntry 返回指定平台的注册条目。
func (r *AdapterRegistry) GetEntry(platform Platform) *AdapterEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.entries[platform]
}

// Has 检查指定平台是否已注册。
func (r *AdapterRegistry) Has(platform Platform) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, exists := r.entries[platform]
	return exists
}

// ───────────────────────────── 自动发现 ─────────────────────────────

// DiscoverAdapters 列出所有已注册的平台适配器。
// 类似于 tool.DiscoverBuiltin，用于显式触发和日志记录。
func DiscoverAdapters() {
	registry := GetRegistry()
	platforms := registry.List()

	names := make([]string, 0, len(platforms))
	for _, p := range platforms {
		entry := registry.GetEntry(p)
		if entry != nil {
			names = append(names, entry.Name)
		}
	}

	slog.Info("built-in platform adapter discovery complete",
		"count", len(platforms),
		"platforms", names,
	)
}
