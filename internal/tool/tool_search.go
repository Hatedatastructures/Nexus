package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ───────────────────────────── 工具搜索工具 ─────────────────────────────

// ToolSearchTool 让模型动态搜索可用工具，减少上下文开销。
type ToolSearchTool struct{}

// Name 返回工具名称。
func (t *ToolSearchTool) Name() string { return "tool_search" }

// Description 返回工具描述。
func (t *ToolSearchTool) Description() string {
	return "搜索可用工具。根据关键词匹配工具名称和描述，返回匹配的工具列表。用于在不确定有哪些工具可用时动态发现工具。"
}

// Toolset 返回工具所属工具集。
func (t *ToolSearchTool) Toolset() string { return "meta" }

// Emoji 返回工具图标。
func (t *ToolSearchTool) Emoji() string { return "🔍" }

// IsAvailable 始终可用。
func (t *ToolSearchTool) IsAvailable() bool { return true }

// MaxResultChars 返回结果最大字符数。
func (t *ToolSearchTool) MaxResultChars() int { return 10000 }

// Schema 返回工具的 JSON Schema。
func (t *ToolSearchTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "tool_search",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "搜索关键词，匹配工具名称和描述",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "返回结果的最大数量（默认 10，最大 20）",
				},
			},
			"required": []string{"query"},
		},
	}
}

// matchInfo 保存匹配结果及匹配类型。
type matchInfo struct {
	name        string
	description string
	toolset     string
	nameMatch   bool // true = 名称匹配，false = 描述匹配
}

// Execute 执行工具搜索。
// 流程:
//   1. 解析参数: query, limit
//   2. 遍历所有已注册工具，按名称和描述进行关键词匹配
//   3. 名称匹配优先于描述匹配排序
//   4. 返回匹配的工具列表
func (t *ToolSearchTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	// 解析 query 参数
	query, ok := args["query"].(string)
	if !ok || strings.TrimSpace(query) == "" {
		return ToolError("参数 query 是必填项且必须为非空字符串"), nil
	}
	query = strings.ToLower(strings.TrimSpace(query))

	// 解析 limit 参数 (默认 10，上限 20)
	limit := 10
	if v, ok := args["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	if limit > 20 {
		limit = 20
	}

	// 获取所有工具名称并逐一匹配
	allTools := GetRegistry().ListTools()
	var nameMatches []matchInfo
	var descMatches []matchInfo

	for _, name := range allTools {
		entry := GetRegistry().GetEntry(name)
		if entry == nil {
			continue
		}

		lowerName := strings.ToLower(name)
		desc := entry.Tool.Description()
		lowerDesc := strings.ToLower(desc)

		info := matchInfo{
			name:        name,
			description: desc,
			toolset:     entry.Tool.Toolset(),
		}

		if strings.Contains(lowerName, query) {
			info.nameMatch = true
			nameMatches = append(nameMatches, info)
		} else if strings.Contains(lowerDesc, query) {
			info.nameMatch = false
			descMatches = append(descMatches, info)
		}
	}

	// 名称匹配优先，按名称排序保持稳定顺序
	sort.Slice(nameMatches, func(i, j int) bool {
		return nameMatches[i].name < nameMatches[j].name
	})
	sort.Slice(descMatches, func(i, j int) bool {
		return descMatches[i].name < descMatches[j].name
	})

	// 合并并截断
	all := append(nameMatches, descMatches...)
	if len(all) > limit {
		all = all[:limit]
	}

	// 构建结果
	matches := make([]map[string]string, 0, len(all))
	for _, m := range all {
		matches = append(matches, map[string]string{
			"name":        m.name,
			"description": m.description,
			"toolset":     m.toolset,
		})
	}

	output, err := json.Marshal(map[string]any{
		"output":  fmt.Sprintf("Found %d matching tools", len(matches)),
		"matches": matches,
	})
	if err != nil {
		return ToolError(fmt.Sprintf("序列化结果失败: %v", err)), nil
	}
	return string(output), nil
}

// ───────────────────────────── init 注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&ToolSearchTool{})
}
