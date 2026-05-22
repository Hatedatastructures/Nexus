// Package tool 提供工具注册中心。
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
)

// ───────────────────────────── 注册中心 ─────────────────────────────

// Registry 是工具注册中心，并发安全。
// 所有工具通过 Register 方法注册到此中心。
// 代理通过 Dispatch 方法调用工具。
type Registry struct {
	mu             sync.RWMutex
	tools          map[string]*ToolEntry       // name → entry
	toolsets       map[string][]string         // toolset → tool names
	aliases        map[string]string           // alias → canonical toolset
	toolsetChecks  map[string]func() bool      // toolset → availability check
}

// 全局注册中心单例
var globalRegistry = &Registry{
	tools:         make(map[string]*ToolEntry),
	toolsets:      make(map[string][]string),
	aliases:       make(map[string]string),
	toolsetChecks: make(map[string]func() bool),
}

// GetRegistry 返回全局注册中心实例
func GetRegistry() *Registry {
	return globalRegistry
}

// ───────────────────────────── 注册 ─────────────────────────────

// Register 向注册中心注册一个工具。
// 通常在 init() 函数中调用。
// 如果工具名称冲突，后注册的会覆盖先注册的 (预警日志)。
func (r *Registry) Register(tool Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := tool.Name()
	toolset := tool.Toolset()

	// 重复注册警告 (保留原有行为: 后注册覆盖先注册)
	if _, exists := r.tools[name]; exists {
		slog.Warn("工具名冲突，覆盖注册", "name", name)
	}

	r.tools[name] = &ToolEntry{
		Tool:           tool,
		IsAsync:        false,
		MaxResultChars: tool.MaxResultChars(),
	}
	r.toolsets[toolset] = appendUnique(r.toolsets[toolset], name)
}

// appendUnique 向切片追加不重复的元素
func appendUnique(slice []string, val string) []string {
	for _, s := range slice {
		if s == val {
			return slice
		}
	}
	return append(slice, val)
}

// ───────────────────────────── 分发 ─────────────────────────────

// Dispatch 执行指定名称的工具。
// args 是从模型工具调用中解析的参数键值对。
// 返回 JSON 格式的结果字符串。
func (r *Registry) Dispatch(ctx context.Context, name string, args map[string]any) (string, error) {
	entry := r.getEntry(name)
	if entry == nil {
		errJSON := map[string]string{"error": fmt.Sprintf("未知工具: %s", name)}
		data, _ := json.Marshal(errJSON)
		return string(data), nil
	}

	result, err := entry.Tool.Execute(ctx, args)
	if err != nil {
		slog.Warn("工具执行失败", "tool", name, "err", err)
		errJSON := map[string]string{"error": fmt.Sprintf("工具执行失败: %v", err)}
		data, _ := json.Marshal(errJSON)
		return string(data), nil
	}
	return result, nil
}

// ───────────────────────────── 查询 ─────────────────────────────

// GetDefinitions 返回指定工具的 Schema 列表。
// 如果 toolNames 为空，返回所有可用工具的 Schema。
func (r *Registry) GetDefinitions(toolNames []string) []*ToolSchema {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*ToolSchema
	if len(toolNames) == 0 {
		// 返回所有可用工具
		for _, entry := range r.tools {
			if entry.Tool.IsAvailable() {
				result = append(result, entry.Tool.Schema())
			}
		}
	} else {
		// 只返回指定工具
		for _, name := range toolNames {
			if entry, ok := r.tools[name]; ok && entry.Tool.IsAvailable() {
				result = append(result, entry.Tool.Schema())
			}
		}
	}
	return result
}

// GetEntry 返回指定名称的工具条目 (别名)
func (r *Registry) GetEntry(name string) *ToolEntry {
	return r.getEntry(name)
}

// getEntry 内部不加锁版本
func (r *Registry) getEntry(name string) *ToolEntry {
	entry, ok := r.tools[name]
	if !ok {
		return nil
	}
	return entry
}

// ListTools 返回所有已注册工具的名称列表
func (r *Registry) ListTools() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	return names
}

// RegisterToolsetAlias 注册工具集别名
func (r *Registry) RegisterToolsetAlias(alias, canonical string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.aliases[alias] = canonical
}

// ───────────────────────────── 工具函数 ─────────────────────────────

// ToolError 格式化工具错误结果为 JSON 字符串
func ToolError(message string) string {
	data, _ := json.Marshal(map[string]string{"error": message})
	return string(data)
}

// ToolResult 格式化工具成功结果为 JSON 字符串
func ToolResult(data map[string]any) string {
	result, _ := json.Marshal(data)
	return string(result)
}
