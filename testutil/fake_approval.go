// Package testutil 提供测试用的模拟对象和辅助工具。
// 本文件实现 approval.Checker 的假实现，默认自动批准所有命令。
package testutil

import (
	"context"
	"sync"

	"nexus-agent/internal/approval"
)

// ───────────────────────────── FakeChecker ─────────────────────────────

// FakeChecker 是 approval.Checker 的假实现。
// 默认自动批准所有命令和工具调用。
type FakeChecker struct {
	mu sync.Mutex

	// ── 配置字段 ──

	// Mode_ 返回审批模式。
	Mode_ string

	// DefaultResult 默认审批结果。
	DefaultResult approval.Result

	// DefaultReason 默认审批原因。
	DefaultReason string

	// CheckFunc 可选的自定义 Check 实现。
	CheckFunc func(ctx context.Context, command string) (approval.Result, string)

	// CheckToolFunc 可选的自定义 CheckTool 实现。
	CheckToolFunc func(ctx context.Context, toolName string, args map[string]any) (approval.Result, string)

	// DeniedCommands 预设拒绝的命令集合。
	DeniedCommands map[string]string

	// ── 记录字段 (用于断言) ──

	// CheckedCommands 记录所有检查过的命令。
	CheckedCommands []RecordedCheck

	// CheckedTools 记录所有检查过的工具调用。
	CheckedTools []RecordedToolCheck
}

// RecordedCheck 记录一条命令审批请求。
type RecordedCheck struct {
	Command string
}

// RecordedToolCheck 记录一条工具调用审批请求。
type RecordedToolCheck struct {
	ToolName string
	Args     map[string]any
}

// ───────────────────────────── Checker 公共 API 实现 ─────────────────────────────

// Check 检查命令是否安全。默认自动批准。
func (f *FakeChecker) Check(ctx context.Context, command string) (approval.Result, string) {
	f.mu.Lock()
	f.CheckedCommands = append(f.CheckedCommands, RecordedCheck{Command: command})
	f.mu.Unlock()

	if f.CheckFunc != nil {
		return f.CheckFunc(ctx, command)
	}

	// 检查预设拒绝列表
	if f.DeniedCommands != nil {
		f.mu.Lock()
		reason, ok := f.DeniedCommands[command]
		f.mu.Unlock()
		if ok {
			return approval.Denied, reason
		}
	}

	// 默认批准
	if f.DefaultResult != 0 || f.DefaultReason != "" {
		return f.DefaultResult, f.DefaultReason
	}
	return approval.Approved, "FakeChecker: 自动批准"
}

// CheckTool 检查工具调用是否安全。默认自动批准。
func (f *FakeChecker) CheckTool(ctx context.Context, toolName string, args map[string]any) (approval.Result, string) {
	f.mu.Lock()
	f.CheckedTools = append(f.CheckedTools, RecordedToolCheck{
		ToolName: toolName,
		Args:     args,
	})
	f.mu.Unlock()

	if f.CheckToolFunc != nil {
		return f.CheckToolFunc(ctx, toolName, args)
	}

	if f.DefaultResult != 0 || f.DefaultReason != "" {
		return f.DefaultResult, f.DefaultReason
	}
	return approval.Approved, "FakeChecker: 自动批准"
}

// Mode 返回审批模式。
func (f *FakeChecker) Mode() string {
	if f.Mode_ != "" {
		return f.Mode_
	}
	return "off"
}

// SetMode 设置审批模式。
func (f *FakeChecker) SetMode(mode string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Mode_ = mode
}

// ───────────────────────────── 辅助方法 ─────────────────────────────

// Reset 清空所有记录。
func (f *FakeChecker) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.CheckedCommands = nil
	f.CheckedTools = nil
}

// AddDeniedCommand 添加一条预设拒绝的命令。
func (f *FakeChecker) AddDeniedCommand(command, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.DeniedCommands == nil {
		f.DeniedCommands = make(map[string]string)
	}
	f.DeniedCommands[command] = reason
}

// SetDefaultResult 设置默认审批结果。
func (f *FakeChecker) SetDefaultResult(result approval.Result, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.DefaultResult = result
	f.DefaultReason = reason
}

// ───────────────────────────── 便捷构造函数 ─────────────────────────────

// NewFakeChecker 创建一个自动批准的 FakeChecker。
func NewFakeChecker() *FakeChecker {
	return &FakeChecker{
		Mode_: "off",
	}
}

// NewDenyAllChecker 创建一个拒绝所有命令的 FakeChecker。
func NewDenyAllChecker() *FakeChecker {
	return &FakeChecker{
		Mode_:         "always",
		DefaultResult: approval.Denied,
		DefaultReason: "FakeChecker: 拒绝所有",
	}
}
