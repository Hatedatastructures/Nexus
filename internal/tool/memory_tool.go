// Package tool 提供记忆管理工具。
// 将模型对记忆的读写操作委托给 memory.Manager，
// 通过 context 值或全局引用获取管理器实例。
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
)

// ───────────────────────────── context 键 ─────────────────────────────

// contextKey 是 context 中存储值的键类型。
type contextKey string

const (
	// memoryManagerKey 是 context 中存储 MemoryManager 引用的键。
	memoryManagerKey contextKey = "nexus_memory_manager"
)

// ───────────────────────────── 记忆管理器接口 ─────────────────────────────

// MemoryHandler 是记忆工具使用的内存管理器抽象。
// 解耦 tool 包和 memory 包的循环依赖。
type MemoryHandler interface {
	// HandleToolCall 处理记忆相关的工具调用。
	HandleToolCall(ctx context.Context, toolName string, args map[string]any) (string, error)
}

// ───────────────────────────── 全局记忆管理器引用 ─────────────────────────────

// globalMemoryManager 存储全局的记忆管理器引用。
// 在代理初始化时通过 SetMemoryManager 设置。
var (
	globalMemoryManager  MemoryHandler
	globalMemoryManagerMu sync.RWMutex
)

// SetMemoryManager 设置全局记忆管理器。
// 在代理启动时调用一次。
func SetMemoryManager(mgr MemoryHandler) {
	globalMemoryManagerMu.Lock()
	defer globalMemoryManagerMu.Unlock()
	globalMemoryManager = mgr
}

// ContextWithMemoryManager 返回携带记忆管理器的 context。
func ContextWithMemoryManager(ctx context.Context, mgr MemoryHandler) context.Context {
	return context.WithValue(ctx, memoryManagerKey, mgr)
}

// getMemoryManager 从 context 获取记忆管理器，fallback 到全局引用。
func getMemoryManager(ctx context.Context) MemoryHandler {
	if mgr, ok := ctx.Value(memoryManagerKey).(MemoryHandler); ok && mgr != nil {
		return mgr
	}
	globalMemoryManagerMu.RLock()
	defer globalMemoryManagerMu.RUnlock()
	return globalMemoryManager
}

// ───────────────────────────── 记忆工具 ─────────────────────────────

// MemoryTool 实现对记忆的读、写、替换、删除操作。
// 将操作委托给 memory.Manager (内置提供者或外部提供者)。
type MemoryTool struct{}

// Name 返回工具名称。
func (t *MemoryTool) Name() string { return "memory" }

// Description 返回工具描述。
func (t *MemoryTool) Description() string {
	return "管理持久化记忆。支持读取、添加、替换和删除记忆条目。记忆分为 agent (MEMORY.md) 和 user (USER.md) 两个目标。"
}

// Toolset 返回工具所属工具集。
func (t *MemoryTool) Toolset() string { return "memory" }

// Emoji 返回工具图标。
func (t *MemoryTool) Emoji() string { return "🧠" }

// IsAvailable 记忆工具始终可用。
func (t *MemoryTool) IsAvailable() bool { return true }

// MaxResultChars 返回结果最大字符数。
func (t *MemoryTool) MaxResultChars() int { return 10000 }

// Schema 返回工具的 JSON Schema。
func (t *MemoryTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "memory",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "操作类型: add (添加记忆), replace (替换记忆), remove (删除记忆)",
					"enum":        []string{"add", "replace", "remove"},
				},
				"target": map[string]any{
					"type":        "string",
					"description": "目标记忆存储: memory (agent 记忆) 或 user (用户记忆)",
					"enum":        []string{"memory", "user"},
				},
				"content": map[string]any{
					"type":        "string",
					"description": "要写入的记忆内容 (action=write 时必填)",
				},
				"old_text": map[string]any{
					"type":        "string",
					"description": "要被替换或删除的原文文本 (action=replace/remove 时必填)",
				},
			},
			"required": []string{"action"},
		},
	}
}

// Execute 执行记忆操作。
// 委托给 memory.Manager.HandleToolCall。
func (t *MemoryTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	action, ok := args["action"].(string)
	if !ok || action == "" {
		return ToolError("参数 action 是必填项且必须为字符串"), nil
	}

	// 验证 action 值
	switch action {
	case "add", "replace", "remove":
		// 合法操作
	default:
		return ToolError(fmt.Sprintf("不支持的 action: %s。支持的操作: read, write, replace, remove", action)), nil
	}

	// 写入/替换/删除操作需要 target
	if action != "add" {
		target, ok := args["target"].(string)
		if !ok || target == "" {
			return ToolError("参数 target 是必填项 (memory 或 user)"), nil
		}
		if target != "memory" && target != "user" {
			return ToolError("参数 target 必须是 memory 或 user"), nil
		}
	}

	// 委托给记忆管理器
	mgr := getMemoryManager(ctx)
	if mgr != nil {
		result, err := mgr.HandleToolCall(ctx, "memory", args)
		if err != nil {
			slog.Error("memory manager processing failed", "action", action, "err", err)
			return ToolError(fmt.Sprintf("记忆操作失败: %v", err)), nil
		}
		return result, nil
	}

	// 无记忆管理器时的降级处理
	slog.Warn("memory manager not configured, returning placeholder result", "action", action)

	result, _ := json.Marshal(map[string]any{
		"output": fmt.Sprintf("记忆操作 '%s' 已接收。记忆管理器未配置，操作未持久化。", action),
		"action": action,
		"status": "degraded",
	})

	return string(result), nil
}

// ───────────────────────────── init 注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&MemoryTool{})
}
