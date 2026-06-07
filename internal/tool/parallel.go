// Package tool 提供并行工具执行控制逻辑。
// 判断一组工具调用是否可以安全地并行执行。
package tool

import (
	"nexus-agent/internal/llm"
)

// ───────────────────────────── 并行执行判断 ─────────────────────────────

// unsafeParallelTools 包含不能并行执行的工具名称。
// 这些工具可能修改共享状态 (文件系统、终端状态、浏览器状态)，
// 或需要严格的执行顺序。
var unsafeParallelTools = map[string]bool{
	"terminal":         true, // 终端命令修改系统状态
	"file_write":       true, // 文件写入修改文件系统
	"file_edit":        true, // 文件编辑修改文件系统
	"browser_navigate": true, // 浏览器导航改变页面状态
	"browser_click":    true, // 浏览器点击改变页面状态
	"browser_type":     true, // 浏览器输入改变页面状态
	"delegate_task":    true, // 子代理委派可能修改系统状态
	"code_execute":     true, // 代码执行修改系统状态
	"memory":           true, // 记忆写入修改记忆状态
}

// safeGuardTools 包含共享状态保护伞下的工具。
// 如果工具调用中存在这些工具之一，则整个工具调用组必须顺序执行。
var safeGuardTools = map[string]bool{
	"browser_navigate": true, // 浏览器系列工具共享页面状态
}

// ───────────────────────────── ShouldParallelize ─────────────────────────────

// ShouldParallelize 判断一组工具调用是否可以并行执行。
// 检查规则:
//  1. 无环依赖: 没有工具的输出作为另一个工具的输入 (在客户端判断)
//  2. 无共享状态工具: 不含 unsafeParallelTools 中的工具
//  3. 无状态保护伞: 不含 safeGuardTools 中的工具
//
// 如果所有检查通过，返回 true 表示可以安全并行。
func ShouldParallelize(toolCalls []llm.ToolCall) bool {
	if len(toolCalls) <= 1 {
		// 单个工具调用不需要并行
		return false
	}

	// 检查是否有不安全并行的工具
	for _, tc := range toolCalls {
		if unsafeParallelTools[tc.Name] {
			return false
		}
		if safeGuardTools[tc.Name] {
			return false
		}
	}

	return true
}

// ToolCallNames 从工具调用列表中提取工具名称。
func ToolCallNames(toolCalls []llm.ToolCall) []string {
	names := make([]string, len(toolCalls))
	for i, tc := range toolCalls {
		names[i] = tc.Name
	}
	return names
}
