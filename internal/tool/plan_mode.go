// Package tool 提供计划模式切换工具。
// 允许代理进入只读模式进行规划，完成后退出恢复完整工具访问。
package tool

import (
	"context"
	"sync"
)

// ───────────────────────────── 计划模式回调机制 ─────────────────────────────

// PlanModeCallback 是计划模式切换回调函数类型。
// readonly 为 true 表示进入只读模式，false 表示退出恢复正常模式。
type PlanModeCallback func(readonly bool)

var (
	globalPlanModeCallback PlanModeCallback
	planModeCallbackMu     sync.RWMutex
)

// SetPlanModeCallback 设置全局计划模式切换回调。
// 通常在 Agent 初始化时调用，将回调连接到权限检查器的策略切换。
func SetPlanModeCallback(cb PlanModeCallback) {
	planModeCallbackMu.Lock()
	defer planModeCallbackMu.Unlock()
	globalPlanModeCallback = cb
}

// GetPlanModeCallback 获取当前计划模式回调。
func GetPlanModeCallback() PlanModeCallback {
	planModeCallbackMu.RLock()
	defer planModeCallbackMu.RUnlock()
	return globalPlanModeCallback
}

// ───────────────────────────── 进入计划模式工具 ─────────────────────────────

// EnterPlanModeTool 实现进入计划模式的功能。
// 切换到只读模式，仅保留 file_read、glob、grep 等只读工具可用。
type EnterPlanModeTool struct{}

// Name 返回工具名称。
func (t *EnterPlanModeTool) Name() string { return "enter_plan_mode" }

// Description 返回工具描述。
func (t *EnterPlanModeTool) Description() string {
	return "Enter plan mode — switches to read-only access. Only file_read, glob, grep tools are available. Use this when you need to explore the codebase and design an approach before making changes."
}

// Toolset 返回工具所属工具集。
func (t *EnterPlanModeTool) Toolset() string { return "plan" }

// Emoji 返回工具图标。
func (t *EnterPlanModeTool) Emoji() string { return "📋" }

// IsAvailable 检查计划模式工具是否可用。
// 需要有回调函数注入。
func (t *EnterPlanModeTool) IsAvailable() bool {
	return GetPlanModeCallback() != nil
}

// MaxResultChars 返回结果最大字符数。
func (t *EnterPlanModeTool) MaxResultChars() int { return 2000 }

// Schema 返回工具的 JSON Schema。
func (t *EnterPlanModeTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "enter_plan_mode",
		Description: t.Description(),
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}
}

// Execute 执行进入计划模式。
// 调用回调将权限检查器切换为只读策略。
func (t *EnterPlanModeTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	callback := GetPlanModeCallback()
	if callback == nil {
		return ToolError("Plan mode is not available in the current execution context."), nil
	}

	callback(true)

	return ToolResult(map[string]any{
		"status":  "plan_mode_enabled",
		"message": "Entered plan mode. Only read-only tools (file_read, glob, grep) are available. Use exit_plan_mode to return to normal mode.",
	}), nil
}

// ───────────────────────────── 退出计划模式工具 ─────────────────────────────

// ExitPlanModeTool 实现退出计划模式的功能。
// 恢复正常的完整工具访问权限。
type ExitPlanModeTool struct{}

// Name 返回工具名称。
func (t *ExitPlanModeTool) Name() string { return "exit_plan_mode" }

// Description 返回工具描述。
func (t *ExitPlanModeTool) Description() string {
	return "Exit plan mode — restores normal tool access. Use this when you're ready to implement the planned changes."
}

// Toolset 返回工具所属工具集。
func (t *ExitPlanModeTool) Toolset() string { return "plan" }

// Emoji 返回工具图标。
func (t *ExitPlanModeTool) Emoji() string { return "📋" }

// IsAvailable 检查计划模式工具是否可用。
// 需要有回调函数注入。
func (t *ExitPlanModeTool) IsAvailable() bool {
	return GetPlanModeCallback() != nil
}

// MaxResultChars 返回结果最大字符数。
func (t *ExitPlanModeTool) MaxResultChars() int { return 2000 }

// Schema 返回工具的 JSON Schema。
func (t *ExitPlanModeTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "exit_plan_mode",
		Description: t.Description(),
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}
}

// Execute 执行退出计划模式。
// 调用回调将权限检查器恢复正常策略。
func (t *ExitPlanModeTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	callback := GetPlanModeCallback()
	if callback == nil {
		return ToolError("Plan mode is not available in the current execution context."), nil
	}

	callback(false)

	return ToolResult(map[string]any{
		"status":  "plan_mode_disabled",
		"message": "Exited plan mode. Full tool access has been restored.",
	}), nil
}

