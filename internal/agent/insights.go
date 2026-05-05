// Package agent 提供使用洞察引擎功能。
// 从 SQLite 状态存储中聚合会话、工具使用、Token 用量等统计数据，
// 并以 ASCII 表格或 Markdown 格式输出。
package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"nexus-agent/internal/state"
)

// ───────────────────────────── 数据结构 ─────────────────────────────

// InsightsEngine 使用洞察引擎。
type InsightsEngine struct {
	store *state.Store
}

// NewInsightsEngine 创建洞察引擎。
func NewInsightsEngine(store *state.Store) *InsightsEngine {
	return &InsightsEngine{store: store}
}

// OverviewMetrics 总览指标。
type OverviewMetrics struct {
	TotalSessions    int     `json:"total_sessions"`
	TotalMessages    int     `json:"total_messages"`
	TotalToolCalls   int     `json:"total_tool_calls"`
	TotalInputTokens int64   `json:"total_input_tokens"`
	TotalOutputTokens int64  `json:"total_output_tokens"`
	TotalAPICalls    int     `json:"total_api_calls"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`
	AvgMessagesPerSession float64 `json:"avg_messages_per_session"`
}

// ModelBreakdown 按模型的统计。
type ModelBreakdown struct {
	Model       string  `json:"model"`
	Sessions    int     `json:"sessions"`
	Messages    int     `json:"messages"`
	InputTokens int64   `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	CostUSD     float64 `json:"cost_usd"`
}

// ToolBreakdown 按工具的统计。
type ToolBreakdown struct {
	ToolName string `json:"tool_name"`
	CallCount int   `json:"call_count"`
}

// PlatformBreakdown 按平台的统计。
type PlatformBreakdown struct {
	Platform string `json:"platform"`
	Sessions int    `json:"sessions"`
	Messages int    `json:"messages"`
}

// ───────────────────────────── 查询函数 ─────────────────────────────

// GetOverview 获取总览指标。
func (e *InsightsEngine) GetOverview(ctx context.Context) (*OverviewMetrics, error) {
	if e.store == nil {
		return &OverviewMetrics{}, nil
	}

	sessions, err := e.store.ListSessions(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("查询会话失败: %w", err)
	}

	metrics := &OverviewMetrics{}
	metrics.TotalSessions = len(sessions)

	for _, s := range sessions {
		metrics.TotalMessages += s.MessageCount
		metrics.TotalToolCalls += s.ToolCallCount
		metrics.TotalInputTokens += int64(s.InputTokens)
		metrics.TotalOutputTokens += int64(s.OutputTokens)
		metrics.TotalAPICalls += s.APICallCount
		metrics.EstimatedCostUSD += s.EstimatedCostUSD
	}

	if metrics.TotalSessions > 0 {
		metrics.AvgMessagesPerSession = float64(metrics.TotalMessages) / float64(metrics.TotalSessions)
	}

	return metrics, nil
}

// GetModelBreakdown 获取按模型的统计。
func (e *InsightsEngine) GetModelBreakdown(ctx context.Context) ([]ModelBreakdown, error) {
	if e.store == nil {
		return nil, nil
	}

	sessions, err := e.store.ListSessions(ctx, nil)
	if err != nil {
		return nil, err
	}

	modelMap := make(map[string]*ModelBreakdown)
	for _, s := range sessions {
		m := s.Model
		if m == "" {
			m = "unknown"
		}
		if _, ok := modelMap[m]; !ok {
			modelMap[m] = &ModelBreakdown{Model: m}
		}
		b := modelMap[m]
		b.Sessions++
		b.Messages += s.MessageCount
		b.InputTokens += int64(s.InputTokens)
		b.OutputTokens += int64(s.OutputTokens)
		b.CostUSD += s.EstimatedCostUSD
	}

	var results []ModelBreakdown
	for _, b := range modelMap {
		results = append(results, *b)
	}
	return results, nil
}

// GetPlatformBreakdown 获取按来源的统计。
func (e *InsightsEngine) GetPlatformBreakdown(ctx context.Context) ([]PlatformBreakdown, error) {
	if e.store == nil {
		return nil, nil
	}

	sessions, err := e.store.ListSessions(ctx, nil)
	if err != nil {
		return nil, err
	}

	platformMap := make(map[string]*PlatformBreakdown)
	for _, s := range sessions {
		p := s.Source
		if p == "" {
			p = "local"
		}
		if _, ok := platformMap[p]; !ok {
			platformMap[p] = &PlatformBreakdown{Platform: p}
		}
		b := platformMap[p]
		b.Sessions++
		b.Messages += s.MessageCount
	}

	var results []PlatformBreakdown
	for _, b := range platformMap {
		results = append(results, *b)
	}
	return results, nil
}

// ───────────────────────────── 格式化输出 ─────────────────────────────

// FormatTerminal 以 ASCII 表格格式输出洞察。
func (e *InsightsEngine) FormatTerminal(ctx context.Context) (string, error) {
	overview, err := e.GetOverview(ctx)
	if err != nil {
		return "", err
	}

	models, _ := e.GetModelBreakdown(ctx)
	platforms, _ := e.GetPlatformBreakdown(ctx)

	var b strings.Builder

	// 总览
	b.WriteString("╔══════════════════════════════════════════╗\n")
	b.WriteString("║           使用洞察总览                    ║\n")
	b.WriteString("╠══════════════════════════════════════════╣\n")
	b.WriteString(fmt.Sprintf("║  会话总数:     %-26d║\n", overview.TotalSessions))
	b.WriteString(fmt.Sprintf("║  消息总数:     %-26d║\n", overview.TotalMessages))
	b.WriteString(fmt.Sprintf("║  工具调用:     %-26d║\n", overview.TotalToolCalls))
	b.WriteString(fmt.Sprintf("║  API 调用:     %-26d║\n", overview.TotalAPICalls))
	b.WriteString(fmt.Sprintf("║  输入 Token:   %-26d║\n", overview.TotalInputTokens))
	b.WriteString(fmt.Sprintf("║  输出 Token:   %-26d║\n", overview.TotalOutputTokens))
	b.WriteString(fmt.Sprintf("║  估算费用:     $%-25.4f║\n", overview.EstimatedCostUSD))
	b.WriteString(fmt.Sprintf("║  平均消息/会话: %-24.1f║\n", overview.AvgMessagesPerSession))
	b.WriteString("╚══════════════════════════════════════════╝\n\n")

	// 模型分布
	if len(models) > 0 {
		b.WriteString("┌─ 模型使用分布 ─────────────────────────┐\n")
		for _, m := range models {
			b.WriteString(fmt.Sprintf("│  %-20s  会话:%-4d  费用:$%.4f\n", m.Model, m.Sessions, m.CostUSD))
		}
		b.WriteString("└─────────────────────────────────────────┘\n\n")
	}

	// 平台分布
	if len(platforms) > 0 {
		b.WriteString("┌─ 平台使用分布 ─────────────────────────┐\n")
		for _, p := range platforms {
			b.WriteString(fmt.Sprintf("│  %-15s  会话:%-4d  消息:%-6d\n", p.Platform, p.Sessions, p.Messages))
		}
		b.WriteString("└─────────────────────────────────────────┘\n")
	}

	return b.String(), nil
}

// FormatGateway 以 Markdown 格式输出洞察（适合消息平台）。
func (e *InsightsEngine) FormatGateway(ctx context.Context) (string, error) {
	overview, err := e.GetOverview(ctx)
	if err != nil {
		return "", err
	}

	models, _ := e.GetModelBreakdown(ctx)

	var b strings.Builder

	b.WriteString("**使用洞察**\n\n")
	b.WriteString(fmt.Sprintf("- 会话: %d | 消息: %d | 工具调用: %d\n", overview.TotalSessions, overview.TotalMessages, overview.TotalToolCalls))
	b.WriteString(fmt.Sprintf("- Token: %d 输入 / %d 输出\n", overview.TotalInputTokens, overview.TotalOutputTokens))
	b.WriteString(fmt.Sprintf("- 费用: $%.4f\n\n", overview.EstimatedCostUSD))

	if len(models) > 0 {
		b.WriteString("**模型分布:**\n")
		for _, m := range models {
			b.WriteString(fmt.Sprintf("- `%s`: %d 会话, $%.4f\n", m.Model, m.Sessions, m.CostUSD))
		}
	}

	return b.String(), nil
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

// SessionStats 单个会话的统计摘要。
type SessionStats struct {
	ID               string
	Title            string
	Model            string
	Platform         string
	MessageCount     int
	ToolCallCount    int
	Duration         time.Duration
	EstimatedCostUSD float64
}

// GetSessionStats 获取单个会话的统计。
func (e *InsightsEngine) GetSessionStats(ctx context.Context, sessionID string) (*SessionStats, error) {
	if e.store == nil {
		return nil, fmt.Errorf("状态存储未初始化")
	}

	sess, err := e.store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	return &SessionStats{
		ID:               sess.ID,
		Title:            sess.Title,
		Model:            sess.Model,
		Platform:         sess.Source,
		MessageCount:     sess.MessageCount,
		ToolCallCount:    sess.ToolCallCount,
		EstimatedCostUSD: sess.EstimatedCostUSD,
	}, nil
}
