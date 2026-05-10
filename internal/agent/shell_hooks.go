// Package agent 提供 Shell Hook 系统的薄包装层。
// 实际实现已迁移到 internal/hooks 包，本文件仅保留向后兼容的类型别名和委托方法。
package agent

import (
	"context"

	"nexus-agent/internal/hooks"
)

// ───────────────────────────── 类型别名 (向后兼容) ─────────────────────────────

// ShellHookSpec 定义一个 Shell Hook 的规格。
// 委托到 hooks.HookSpec。
type ShellHookSpec = hooks.HookSpec

// HookEvent 表示发送给 hook 脚本的事件。
// 委托到 hooks.HookEvent。
type HookEvent = hooks.HookEvent

// HookResponse 表示 hook 脚本的响应。
// 委托到 hooks.HookResponse。
type HookResponse = hooks.HookResponse

// ShellHookManager 管理所有 Shell Hook。
// 委托到 hooks.HookManager。
type ShellHookManager struct {
	inner *hooks.HookManager
}

// NewShellHookManager 创建 Shell Hook 管理器。
func NewShellHookManager(hookDir string, acceptAll bool) *ShellHookManager {
	return &ShellHookManager{
		inner: hooks.NewHookManager(hookDir, acceptAll),
	}
}

// RegisterHooks 注册多个 hook 规格。
func (m *ShellHookManager) RegisterHooks(specs []ShellHookSpec) error {
	return m.inner.RegisterFromSpecs(specs)
}

// LoadFromDir 从目录加载 hook 配置。
func (m *ShellHookManager) LoadFromDir(dir string) error {
	return m.inner.LoadFromDir(dir)
}

// ExecuteHook 执行匹配的 hook。
// 返回 HookResponse 和是否应该阻止工具调用。
func (m *ShellHookManager) ExecuteHook(ctx context.Context, toolName string, toolInput map[string]any, sessionID string) (*HookResponse, bool, error) {
	return m.inner.ExecutePreHooks(ctx, toolName, toolInput)
}

// ExecutePostHook 执行 post_tool_call hook 链。
func (m *ShellHookManager) ExecutePostHook(ctx context.Context, toolName string, toolInput map[string]any, output string) error {
	return m.inner.ExecutePostHooks(ctx, toolName, toolInput, output)
}
