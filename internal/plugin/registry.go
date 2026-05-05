// Package plugin 提供插件注册中心。
// Registry 管理已加载插件的注册和查询，线程安全。
package plugin

import (
	"fmt"
	"log/slog"
	"sync"
)

// ───────────────────────────── 插件注册中心 ─────────────────────────────

// Registry 是插件注册中心，并发安全。
// 管理所有已加载插件的注册、查询和生命周期状态。
type Registry struct {
	mu      sync.RWMutex
	plugins map[string]Plugin // name → plugin instance
}

// NewRegistry 创建空的插件注册中心。
func NewRegistry() *Registry {
	return &Registry{
		plugins: make(map[string]Plugin),
	}
}

// ───────────────────────────── 注册 ─────────────────────────────

// Register 向注册中心注册一个插件。
// 如果插件名称冲突，后注册的会覆盖先注册的 (预警日志)。
func (r *Registry) Register(name string, plugin Plugin) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.plugins[name]; exists {
		slog.Warn("插件名称冲突，覆盖注册", "name", name)
	}

	r.plugins[name] = plugin
	slog.Debug("插件已注册", "name", name, "version", plugin.Version())
}

// Unregister 从注册中心移除指定名称的插件。
// 返回被移除的插件实例，如果不存在则返回 nil。
func (r *Registry) Unregister(name string) Plugin {
	r.mu.Lock()
	defer r.mu.Unlock()

	p, exists := r.plugins[name]
	if !exists {
		return nil
	}

	delete(r.plugins, name)
	slog.Debug("插件已注销", "name", name)
	return p
}

// ───────────────────────────── 查询 ─────────────────────────────

// Get 根据名称获取已注册的插件。
// 返回插件实例和是否存在。
func (r *Registry) Get(name string) (Plugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.plugins[name]
	return p, ok
}

// List 返回所有已注册插件的名称列表。
// 列表按注册顺序排列。
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.plugins))
	for name := range r.plugins {
		names = append(names, name)
	}
	return names
}

// Size 返回已注册插件的数量。
func (r *Registry) Size() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.plugins)
}

// Has 检查指定名称的插件是否已注册。
func (r *Registry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, ok := r.plugins[name]
	return ok
}

// ───────────────────────────── 遍历 ─────────────────────────────

// Range 遍历所有已注册的插件。
// 回调函数返回 false 时停止遍历。
func (r *Registry) Range(fn func(name string, plugin Plugin) bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for name, plugin := range r.plugins {
		if !fn(name, plugin) {
			break
		}
	}
}

// ───────────────────────────── 工具收集 ─────────────────────────────

// CollectTools 收集所有实现了 ToolProvider 接口的插件提供的工具。
func (r *Registry) CollectTools() map[string][]string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string][]string)
	for name, plugin := range r.plugins {
		if tp, ok := plugin.(ToolProvider); ok {
			tools := tp.Tools()
			toolNames := make([]string, 0, len(tools))
			for _, t := range tools {
				toolNames = append(toolNames, t.Name())
			}
			if len(toolNames) > 0 {
				result[name] = toolNames
			}
		}
	}

	return result
}

// CollectHooks 收集所有实现了 HookProvider 接口的插件注册的钩子。
func (r *Registry) CollectHooks() map[string][]string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string][]string)
	for name, plugin := range r.plugins {
		if hp, ok := plugin.(HookProvider); ok {
			hooks := hp.Hooks()
			var eventTypes []string
			for eventType := range hooks {
				eventTypes = append(eventTypes, eventType)
			}
			if len(eventTypes) > 0 {
				result[name] = eventTypes
			}
		}
	}

	return result
}

// ───────────────────────────── 辅助方法 ─────────────────────────────

// Summary 返回注册中心的摘要信息。
func (r *Registry) Summary() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.plugins) == 0 {
		return "插件注册中心: 空"
	}

	return fmt.Sprintf("插件注册中心: %d 个插件已注册", len(r.plugins))
}
