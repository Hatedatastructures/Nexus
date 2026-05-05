// Package plugin 提供插件系统的核心接口定义。
// 插件通过实现 Plugin 接口接入 Nexus 运行时，可选提供工具和钩子扩展。
package plugin

import (
	"context"

	"nexus-agent/internal/tool"
)

// ───────────────────────────── 核心插件接口 ─────────────────────────────

// Plugin 是所有插件必须实现的核心接口。
// 插件的生命周期由 Manager 管理: Initialize → 运行中 → Shutdown。
type Plugin interface {
	// Name 返回插件的唯一标识名。
	// 用于注册中心的键和配置引用。
	// 例如: "weather", "calendar", "code_review"
	Name() string

	// Version 返回插件的语义版本号。
	// 格式: "主版本.次版本.修订号" (如 "1.2.3")
	Version() string

	// Initialize 初始化插件。
	// config 来自 manifest 和运行时配置的合并。
	// 初始化失败时返回错误，Manager 会跳过此插件。
	Initialize(ctx context.Context, config map[string]any) error

	// Shutdown 优雅关闭插件。
	// 释放资源、保存状态、断开连接等。
	// 调用时机: 网关关闭或插件热重载前。
	Shutdown(ctx context.Context) error
}

// ───────────────────────────── 工具提供者接口 ─────────────────────────────

// ToolProvider 是可提供工具的插件扩展接口。
// 实现此接口的插件会将其工具注册到全局工具注册中心。
type ToolProvider interface {
	// Tools 返回此插件提供的工具列表。
	// 每个工具必须实现 tool.Tool 接口。
	Tools() []tool.Tool
}

// ───────────────────────────── 钩子提供者接口 ─────────────────────────────

// HookHandler 是钩子处理函数。
// 接收事件对象，返回修改后的事件或错误。
// 事件类型由钩子的注册类型决定。
type HookHandler func(ctx context.Context, event any) (any, error)

// HookProvider 是可提供钩子的插件扩展接口。
// 实现此接口的插件会在对应事件阶段执行钩子函数。
type HookProvider interface {
	// Hooks 返回此插件注册的钩子映射。
	// 键为事件类型 (如 "pre_dispatch", "post_delivery", "session:start")，
	// 值为该事件类型的处理函数列表。
	Hooks() map[string][]HookHandler
}

// ───────────────────────────── 内存提供者接口 ─────────────────────────────

// MemoryProvider 是可提供外部记忆功能的插件扩展接口。
// 实现此接口的插件可以替代内置记忆系统。
type MemoryProvider interface {
	// MemoryProviderName 返回记忆提供者的标识名。
	MemoryProviderName() string

	// InitializeMemory 初始化记忆提供者。
	// sessionID 为当前会话标识。
	InitializeMemory(ctx context.Context, sessionID string) error

	// SyncTurn 同步对话轮次到外部记忆系统。
	SyncTurn(ctx context.Context, userContent, assistantContent string) error

	// Prefetch 预取与查询相关的记忆上下文。
	Prefetch(ctx context.Context, query string) (string, error)

	// ShutdownMemory 关闭记忆提供者。
	ShutdownMemory(ctx context.Context) error
}
