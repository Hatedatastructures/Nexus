// Package testutil 提供测试用的模拟对象和辅助工具。
// 本文件实现 memory.Provider 的假实现，嵌入 BaseProvider 提供默认空操作。
package testutil

import (
	"context"
	"fmt"
	"sync"

	"nexus-agent/internal/llm"
	"nexus-agent/internal/memory"
)

// ───────────────────────────── FakeMemoryProvider ─────────────────────────────

// FakeMemoryProvider 是 memory.Provider 的假实现。
// 嵌入 memory.BaseProvider 以获得可选钩子的默认空实现。
type FakeMemoryProvider struct {
	// 嵌入基类，提供 OnTurnStart/OnSessionEnd/OnPreCompress/OnDelegation/QueuePrefetch 的默认实现。
	memory.BaseProvider

	mu sync.Mutex

	// ── 配置字段 ──

	// ProviderName 返回提供者名称。
	ProviderName string

	// InitError 预设的初始化错误。
	InitError error

	// SystemPrompt 返回注入到系统提示词的文本。
	SystemPrompt string

	// PrefetchResult 预设的预取结果。
	PrefetchResult string

	// PrefetchError 预设的预取错误。
	PrefetchError error

	// SyncTurnError 预设的同步错误。
	SyncTurnError error

	// ToolSchemas 预设的工具 Schema 列表。
	ToolSchemas []llm.ToolSchema

	// HandleToolCallFunc 可选的自定义工具调用处理函数。
	HandleToolCallFunc func(ctx context.Context, toolName string, args map[string]any) (string, error)

	// HandleToolCallResult 预设的工具调用结果。
	HandleToolCallResult string

	// HandleToolCallError 预设的工具调用错误。
	HandleToolCallError error

	// ShutdownError 预设的关闭错误。
	ShutdownError error

	// ── 记录字段 (用于断言) ──

	// Initialized 标记是否已初始化。
	Initialized bool

	// SessionID 记录初始化时的会话 ID。
	SessionID string

	// PrefetchedQueries 记录所有预取查询。
	PrefetchedQueries []string

	// SyncedTurns 记录所有同步的对话轮次。
	SyncedTurns []SyncedTurn

	// ToolCalls 记录所有处理过的工具调用。
	ToolCalls []RecordedMemoryToolCall

	// ShutdownCalled 标记是否已调用关闭。
	ShutdownCalled bool
}

// SyncedTurn 记录一条同步的对话轮次。
type SyncedTurn struct {
	UserContent      string
	AssistantContent string
}

// RecordedMemoryToolCall 记录一条工具调用。
type RecordedMemoryToolCall struct {
	ToolName string
	Args     map[string]any
}

// ───────────────────────────── Provider 接口实现 ─────────────────────────────

// Name 返回提供者的唯一名称。
func (f *FakeMemoryProvider) Name() string {
	if f.ProviderName != "" {
		return f.ProviderName
	}
	return "fake_memory"
}

// Initialize 初始化提供者。
func (f *FakeMemoryProvider) Initialize(ctx context.Context, sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.InitError != nil {
		return f.InitError
	}

	f.Initialized = true
	f.SessionID = sessionID
	return nil
}

// SystemPromptBlock 返回注入到系统提示词的静态文本块。
func (f *FakeMemoryProvider) SystemPromptBlock() string {
	if f.SystemPrompt != "" {
		return f.SystemPrompt
	}
	return "这是 FakeMemoryProvider 的默认系统提示词。"
}

// Prefetch 为即将到来的对话回合召回相关记忆。
func (f *FakeMemoryProvider) Prefetch(ctx context.Context, query string) (string, error) {
	f.mu.Lock()
	f.PrefetchedQueries = append(f.PrefetchedQueries, query)
	f.mu.Unlock()

	if f.PrefetchError != nil {
		return "", f.PrefetchError
	}

	return f.PrefetchResult, nil
}

// SyncTurn 将已完成的对话回合持久化到记忆。
func (f *FakeMemoryProvider) SyncTurn(ctx context.Context, userContent, assistantContent string) error {
	f.mu.Lock()
	f.SyncedTurns = append(f.SyncedTurns, SyncedTurn{
		UserContent:      userContent,
		AssistantContent: assistantContent,
	})
	f.mu.Unlock()

	return f.SyncTurnError
}

// GetToolSchemas 返回此提供者暴露给模型的工具 Schema 列表。
func (f *FakeMemoryProvider) GetToolSchemas() []llm.ToolSchema {
	if f.ToolSchemas != nil {
		return f.ToolSchemas
	}
	return []llm.ToolSchema{}
}

// HandleToolCall 处理模型发起的记忆工具调用。
func (f *FakeMemoryProvider) HandleToolCall(ctx context.Context, toolName string, args map[string]any) (string, error) {
	f.mu.Lock()
	f.ToolCalls = append(f.ToolCalls, RecordedMemoryToolCall{
		ToolName: toolName,
		Args:     args,
	})
	f.mu.Unlock()

	if f.HandleToolCallFunc != nil {
		return f.HandleToolCallFunc(ctx, toolName, args)
	}

	if f.HandleToolCallError != nil {
		return "", f.HandleToolCallError
	}

	if f.HandleToolCallResult != "" {
		return f.HandleToolCallResult, nil
	}

	return fmt.Sprintf(`{"status":"ok","tool":"%s"}`, toolName), nil
}

// Shutdown 优雅关闭提供者。
func (f *FakeMemoryProvider) Shutdown(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ShutdownCalled = true
	return f.ShutdownError
}

// ───────────────────────────── 辅助方法 ─────────────────────────────

// Reset 清空所有记录。
func (f *FakeMemoryProvider) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Initialized = false
	f.SessionID = ""
	f.PrefetchedQueries = nil
	f.SyncedTurns = nil
	f.ToolCalls = nil
	f.ShutdownCalled = false
}

// LastPrefetchQuery 返回最后一次预取查询。
func (f *FakeMemoryProvider) LastPrefetchQuery() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.PrefetchedQueries) == 0 {
		return "", fmt.Errorf("没有预取记录")
	}
	return f.PrefetchedQueries[len(f.PrefetchedQueries)-1], nil
}

// LastToolCall 返回最后一次工具调用。
func (f *FakeMemoryProvider) LastToolCall() (*RecordedMemoryToolCall, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.ToolCalls) == 0 {
		return nil, fmt.Errorf("没有工具调用记录")
	}
	tc := f.ToolCalls[len(f.ToolCalls)-1]
	return &tc, nil
}
