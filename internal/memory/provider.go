// Package memory 提供 AI 代理的记忆系统抽象。
// 内置提供者使用文件存储 (MEMORY.md / USER.md)。
// 外部提供者通过插件机制接入 (如 Honcho 等用户画像系统)。
package memory

import (
	"context"

	"nexus-agent/internal/llm"
)

// ───────────────────────────── 记忆提供者接口 ─────────────────────────────

// Provider 是记忆提供者的抽象接口。
// 内置实现 (BuiltinProvider) 使用本地文件存储。
// 外部插件实现可以通过此接口接入。
// 系统最多允许一个外部提供者同时激活 (外加内置提供者)。
type Provider interface {
	// Name 返回提供者的唯一名称。
	Name() string

	// Initialize 初始化提供者 (连接外部服务 / 创建资源)。
	// sessionID 是当前会话的唯一标识。
	Initialize(ctx context.Context, sessionID string) error

	// SystemPromptBlock 返回注入到系统提示词的静态文本块。
	// 这是一段不变的描述，告诉模型如何使用此提供者的工具。
	SystemPromptBlock() string

	// Prefetch 为即将到来的对话回合召回相关记忆。
	// query 是用户当前的输入文本。
	// 返回格式化的记忆上下文文本，将被注入到下一轮的系统提示词中。
	// 返回空字符串表示无相关记忆。
	Prefetch(ctx context.Context, query string) (string, error)

	// QueuePrefetch 在回合结束后触发异步预取。
	// 为下一轮对话提前准备记忆，不阻塞当前回合。
	// 默认实现是空操作。
	QueuePrefetch(ctx context.Context, query string)

	// SyncTurn 将已完成的对话回合持久化到记忆。
	// userContent 是用户消息内容，assistantContent 是助理的完整回复。
	SyncTurn(ctx context.Context, userContent, assistantContent string) error

	// GetToolSchemas 返回此提供者暴露给模型的工具 Schema 列表。
	// 记忆工具通过 ToolRegistry 注册，此方法返回的工具 Schema
	// 也会被加入可用工具列表中。
	GetToolSchemas() []llm.ToolSchema

	// HandleToolCall 处理模型发起的记忆工具调用。
	// toolName 是调用的工具名称，args 是解析后的参数。
	// 返回 JSON 格式的结果字符串。
	HandleToolCall(ctx context.Context, toolName string, args map[string]any) (string, error)

	// Shutdown 优雅关闭提供者。
	// 包括刷盘缓冲数据、关闭连接等。
	Shutdown(ctx context.Context) error

	// ── 可选的生命周期钩子 (以下方法有默认空实现) ──

	// OnTurnStart 在每轮对话开始时调用。
	// turnNum 是当前回合编号 (从 1 开始)。
	OnTurnStart(ctx context.Context, turnNum int, message string) error

	// OnSessionEnd 在会话结束时调用。
	// messages 是完整的对话历史。
	OnSessionEnd(ctx context.Context, messages []llm.Message) error

	// OnPreCompress 在上下文压缩前调用。
	// 提供者可以从中提取重要信息以避免丢失。
	OnPreCompress(ctx context.Context, messages []llm.Message) error

	// OnDelegation 在子代理完成委派任务时调用。
	// 父代理可以观察子代理的工作成果。
	OnDelegation(ctx context.Context, task, result string, childSessionID string) error
}

// ───────────────────────────── 提供者基类 (默认空实现) ─────────────────────────────

// BaseProvider 提供所有可选钩子的默认空实现。
// 自定义提供者可以嵌入此结构体，只覆写需要的方法。
type BaseProvider struct{}

func (BaseProvider) QueuePrefetch(ctx context.Context, query string)            {}
func (BaseProvider) OnTurnStart(ctx context.Context, turnNum int, message string) error {
	return nil
}
func (BaseProvider) OnSessionEnd(ctx context.Context, messages []llm.Message) error {
	return nil
}
func (BaseProvider) OnPreCompress(ctx context.Context, messages []llm.Message) error {
	return nil
}
func (BaseProvider) OnDelegation(ctx context.Context, task, result string, childSessionID string) error {
	return nil
}
