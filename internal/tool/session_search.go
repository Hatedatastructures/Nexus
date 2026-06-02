// Package tool 提供会话搜索工具。
// 基于 SQLite FTS5 全文搜索引擎，搜索历史会话中的相关内容。
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"nexus-agent/internal/state"
)

// ───────────────────────────── 全局状态存储引用 ─────────────────────────────

// globalStateStore 存储全局状态存储引用。
// 在代理初始化时通过 SetStateStore 设置。
var (
	globalStateStore *state.Store
	stateStoreMu     sync.RWMutex
)

// SetStateStore 设置全局状态存储。
// 在代理启动时调用一次。
func SetStateStore(s *state.Store) {
	stateStoreMu.Lock()
	defer stateStoreMu.Unlock()
	globalStateStore = s
}

// getStateStore 获取全局状态存储。
func getStateStore() *state.Store {
	stateStoreMu.RLock()
	defer stateStoreMu.RUnlock()
	return globalStateStore
}

// ───────────────────────────── 会话搜索工具 ─────────────────────────────

// SessionSearchTool 实现历史会话内容搜索功能。
// 基于 SQLite FTS5 全文搜索引擎，支持拉丁语系和 CJK 字符搜索。
type SessionSearchTool struct{}

// Name 返回工具名称。
func (t *SessionSearchTool) Name() string { return "session_search" }

// Description 返回工具描述。
func (t *SessionSearchTool) Description() string {
	return "搜索历史会话中的相关内容。使用 FTS5 全文搜索引擎，跨所有已保存的会话进行全文搜索。支持拉丁语系和中日韩 (CJK) 字符。"
}

// Toolset 返回工具所属工具集。
func (t *SessionSearchTool) Toolset() string { return "research" }

// Emoji 返回工具图标。
func (t *SessionSearchTool) Emoji() string { return "🔍" }

// IsAvailable 检查会话搜索是否可用。
// 取决于状态存储是否已初始化。
func (t *SessionSearchTool) IsAvailable() bool {
	return getStateStore() != nil
}

// MaxResultChars 返回结果最大字符数。
func (t *SessionSearchTool) MaxResultChars() int { return 50000 }

// Schema 返回工具的 JSON Schema。
func (t *SessionSearchTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "session_search",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "搜索关键词 (支持多词搜索、短语搜索用双引号包裹)",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "返回结果的最大数量，默认 10",
				},
				"recent_only": map[string]any{
					"type":        "integer",
					"description": "如果设置，只搜索最近 N 天内的会话 (例如 7 表示最近 7 天)",
				},
			},
			"required": []string{"query"},
		},
	}
}

// Execute 执行会话搜索。
// 流程:
//   1. 验证状态存储已初始化
//   2. 解析参数: query, limit, recent_only
//   3. 调用 Store.SearchMessages 进行 FTS5 搜索
//   4. 格式化搜索结果为可读文本返回
func (t *SessionSearchTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	// 检查状态存储
	store := getStateStore()
	if store == nil {
		slog.Warn("session search failed: state store not initialized")
		return ToolError("状态存储未初始化。请先调用 SetStateStore 配置状态存储。"), nil
	}

	// 解析 query 参数
	query, ok := args["query"].(string)
	if !ok || strings.TrimSpace(query) == "" {
		return ToolError("参数 query 是必填项且必须为非空字符串"), nil
	}

	// 解析 limit 参数 (默认 10)
	limit := 10
	if v, ok := args["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	if limit > 100 {
		limit = 100 // 限制最大搜索数量
	}

	// 解析 recent_only 参数 (可选)
	recentDays := 0
	if v, ok := args["recent_only"].(float64); ok && v > 0 {
		recentDays = int(v)
	}

	slog.Info("executing session search", "query", query, "limit", limit, "recent_days", recentDays)

	// 执行 FTS5 搜索
	results, err := store.SearchMessages(ctx, query, limit)
	if err != nil {
		slog.Error("session search failed", "query", query, "err", err)
		return ToolError(fmt.Sprintf("搜索失败: %v", err)), nil
	}

	// 无结果
	if len(results) == 0 {
		result, err := json.Marshal(map[string]any{
			"output":  fmt.Sprintf("未找到匹配 \"%s\" 的会话内容", query),
			"query":   query,
			"count":   0,
			"results": []any{},
		})
		if err != nil {
			return ToolError(fmt.Sprintf("序列化结果失败: %v", err)), nil
		}
		return string(result), nil
	}

	// 格式化搜索结果
	var formattedResults []map[string]any
	for i, r := range results {
		entry := map[string]any{
			"index":      i + 1,
			"message_id": r.MessageID,
			"session_id": r.SessionID,
			"snippet":    r.Content,
			"rank":       r.Rank,
		}

		// 如果指定了 recent_only，尝试获取会话信息进行时间过滤
		if recentDays > 0 {
			session, sErr := store.GetSession(ctx, r.SessionID)
			if sErr == nil && session != nil {
				entry["session_title"] = session.Title
				entry["session_model"] = session.Model
			}
		}

		formattedResults = append(formattedResults, entry)
	}

	// 构建可读摘要
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("找到 %d 条匹配 \"%s\" 的结果:\n\n", len(results), query))

	for _, entry := range formattedResults {
		sb.WriteString(fmt.Sprintf("[%d] 会话 %s (消息 #%d, 排名: %.4f)\n",
			entry["index"], entry["session_id"], entry["message_id"], entry["rank"]))
		if title, ok := entry["session_title"].(string); ok && title != "" {
			sb.WriteString(fmt.Sprintf("    标题: %s\n", title))
		}
		sb.WriteString(fmt.Sprintf("    内容: %s\n\n", entry["snippet"]))
	}

	// JSON 输出
	output, err := json.Marshal(map[string]any{
		"output":  sb.String(),
		"query":   query,
		"count":   len(results),
		"results": formattedResults,
	})
	if err != nil {
		return ToolError(fmt.Sprintf("序列化结果失败: %v", err)), nil
	}
	return string(output), nil
}

// ───────────────────────────── init 注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&SessionSearchTool{})
}
