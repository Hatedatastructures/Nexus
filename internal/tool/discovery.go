// Package tool 提供工具自动发现功能。
// 所有工具通过 init() 函数自注册到全局注册中心。
// DiscoverBuiltin 确保所有工具包被导入并触发其 init() 函数。
package tool

import (
	"log/slog"
)

// ───────────────────────────── 工具自动发现 ─────────────────────────────

// DiscoverBuiltin 确保所有内置工具都已注册。
// Go 编译器会在程序启动时自动调用所有包的 init() 函数，
// 因此工具注册是自动完成的。此函数主要用于显式触发和日志记录。
//
// 内置工具列表 (均在 init() 中注册):
//   - terminal: 终端命令执行
//   - file_read / file_write / file_edit / file_search: 文件操作
//   - web_search / web_extract: 网页搜索与提取
//   - browser_navigate / browser_click / browser_type / browser_screenshot: 浏览器自动化
//   - code_execute: 代码执行
//   - delegate_task: 子代理委派
//   - memory: 记忆管理
//   - todo: 待办列表
func DiscoverBuiltin() error {
	registry := GetRegistry()
	toolNames := registry.ListTools()

	slog.Info("built-in tool discovery complete",
		"count", len(toolNames),
		"tools", toolNames,
	)
	return nil
}
