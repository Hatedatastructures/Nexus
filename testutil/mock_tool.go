// Package testutil 提供测试用的模拟对象和辅助工具。
// 本文件实现 tool.Tool 的模拟版本，支持预设响应和调用记录。
package testutil

import (
	"context"
	"sync/atomic"

	"nexus-agent/internal/tool"
)

// ───────────────────────────── MockTool ─────────────────────────────

// MockTool 是 tool.Tool 的模拟实现。
// 通过函数字段模式允许按需覆盖任意方法的行为。
// 未设置的字段返回合理的默认值。
type MockTool struct {
	// NameFunc 返回工具名称；默认返回 "mock_tool"。
	NameFunc func() string

	// DescriptionFunc 返回工具描述；默认返回 "mock tool for testing"。
	DescriptionFunc func() string

	// SchemaFunc 返回工具 Schema；默认返回简单的 object schema。
	SchemaFunc func() *tool.ToolSchema

	// ExecuteFunc 执行工具逻辑；默认返回 `{"output": "mock result"}`。
	ExecuteFunc func(ctx context.Context, args map[string]any) (string, error)

	// ToolsetFunc 返回工具集名称；默认返回 "test"。
	ToolsetFunc func() string

	// IsAvailableFunc 报告工具是否可用；默认返回 true。
	IsAvailableFunc func() bool

	// EmojiFunc 返回工具图标；默认返回 "🔧"。
	EmojiFunc func() string

	// MaxResultCharsFunc 返回结果最大字符数；默认返回 0。
	MaxResultCharsFunc func() int

	// ── 记录字段 (用于断言) ──

	// ExecuteCalled 记录 Execute 被调用的次数 (原子递增)。
	ExecuteCalled atomic.Int64

	// LastArgs 记录最近一次传递给 Execute 的参数。
	LastArgs map[string]any
}

// ───────────────────────────── Tool 接口实现 ─────────────────────────────

// Name 返回工具的唯一标识名。
func (m *MockTool) Name() string {
	if m.NameFunc != nil {
		return m.NameFunc()
	}
	return "mock_tool"
}

// Description 返回工具的描述文本。
func (m *MockTool) Description() string {
	if m.DescriptionFunc != nil {
		return m.DescriptionFunc()
	}
	return "mock tool for testing"
}

// Schema 返回工具的 JSON Schema 结构。
func (m *MockTool) Schema() *tool.ToolSchema {
	if m.SchemaFunc != nil {
		return m.SchemaFunc()
	}
	return &tool.ToolSchema{
		Name: "mock_tool",
		Parameters: map[string]any{
			"type": "object",
		},
	}
}

// Execute 执行工具逻辑并记录调用信息。
func (m *MockTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	m.ExecuteCalled.Add(1)
	m.LastArgs = args

	if m.ExecuteFunc != nil {
		return m.ExecuteFunc(ctx, args)
	}

	return `{"output": "mock result"}`, nil
}

// Toolset 返回此工具所属的工具集名称。
func (m *MockTool) Toolset() string {
	if m.ToolsetFunc != nil {
		return m.ToolsetFunc()
	}
	return "test"
}

// IsAvailable 检测工具在当前环境下是否可用。
func (m *MockTool) IsAvailable() bool {
	if m.IsAvailableFunc != nil {
		return m.IsAvailableFunc()
	}
	return true
}

// Emoji 返回工具的展示图标。
func (m *MockTool) Emoji() string {
	if m.EmojiFunc != nil {
		return m.EmojiFunc()
	}
	return "\U0001F527"
}

// MaxResultChars 返回工具结果的最大字符数。
func (m *MockTool) MaxResultChars() int {
	if m.MaxResultCharsFunc != nil {
		return m.MaxResultCharsFunc()
	}
	return 0
}
