// Package mcp 提供工具注册中心到 MCP ToolRegistry 接口的适配器。
package mcp

import (
	"context"
	"fmt"

	"nexus-agent/internal/tool"
)

// ToolRegistryAdapter 将 tool.Registry 适配为 MCP 的 ToolRegistry 接口。
type ToolRegistryAdapter struct {
	registry *tool.Registry
}

// NewToolRegistryAdapter 创建适配器实例。
func NewToolRegistryAdapter(registry *tool.Registry) *ToolRegistryAdapter {
	return &ToolRegistryAdapter{registry: registry}
}

// ListTools 返回所有已注册工具的名称列表。
func (a *ToolRegistryAdapter) ListTools() []string {
	return a.registry.ListTools()
}

// GetSchema 返回指定工具的 Schema。
// 通过查询所有工具定义并匹配名称来获取。
func (a *ToolRegistryAdapter) GetSchema(name string) (*ToolSchema, bool) {
	defs := a.registry.GetDefinitions([]string{name})
	if len(defs) == 0 {
		return nil, false
	}
	d := defs[0]

	// 将 Parameters 转换为 map[string]any
	params, _ := d.Parameters.(map[string]any)

	return &ToolSchema{
		Name:        d.Name,
		Description: d.Description,
		Parameters:  params,
	}, true
}

// Dispatch 执行指定工具。
// 忽略 context 参数 (tool.Registry 的 Dispatch 需要 ctx)。
func (a *ToolRegistryAdapter) Dispatch(name string, args map[string]any) (string, error) {
	ctx := context.Background()
	result, err := a.registry.Dispatch(ctx, name, args)
	if err != nil {
		return "", fmt.Errorf("工具 %q 执行失败: %w", name, err)
	}

	return result, nil
}
