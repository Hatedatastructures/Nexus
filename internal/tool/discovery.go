package tool

import (
	"log/slog"
)

// DiscoverBuiltin 确保所有内置工具都已注册。
//
// Deprecated: 使用 NewRegistry() + RegisterAllTools() 替代。
func DiscoverBuiltin() error {
	registry := globalRegistry
	toolNames := registry.ListTools()

	slog.Info("built-in tool discovery complete",
		"count", len(toolNames),
		"tools", toolNames,
	)
	return nil
}
