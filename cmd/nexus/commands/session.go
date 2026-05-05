package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"nexus-agent/internal/state"
)

// SessionCommand 实现 nexus session 命令。
type SessionCommand struct{}

func (c *SessionCommand) Name() string    { return "session" }
func (c *SessionCommand) Synopsis() string { return "会话管理 (list/search/export/stats)" }

func (c *SessionCommand) Run(args []string) {
	if len(args) == 0 {
		c.listSessions()
		return
	}

	switch args[0] {
	case "list", "ls":
		c.listSessions()
	case "search":
		if len(args) < 2 {
			PrintError("用法: nexus session search <query>")
		}
		c.searchSessions(strings.Join(args[1:], " "))
	case "export":
		if len(args) < 2 {
			PrintError("用法: nexus session export <session_id>")
		}
		c.exportSession(args[1])
	case "stats":
		c.sessionStats()
	default:
		PrintError("未知子命令: %s", args[0])
	}
}

func (c *SessionCommand) getStore() (*state.Store, error) {
	return state.NewStore(GetDBPath())
}

func (c *SessionCommand) listSessions() {
	store, err := c.getStore()
	if err != nil {
		PrintError("打开数据库失败: %v", err)
	}
	defer store.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sessions, err := store.ListRecentSessions(ctx, 20)
	if err != nil {
		PrintError("查询会话失败: %v", err)
	}

	PrintTitle("最近会话 (Top 20)")

	if len(sessions) == 0 {
		fmt.Println(DimStyle.Render("  无会话记录"))
		fmt.Println()
		fmt.Println(DimStyle.Render("  提示: 使用 nexus chat 开始对话以创建会话"))
		return
	}

	for i, sess := range sessions {
		status := DimStyle.Render("○")
		if sess.EndedAt == 0 {
			status = GreenBold.Render("●")
		}

		title := sess.Title
		if title == "" {
			title = "(无标题)"
		}

		fmt.Printf("  %2d. %s %s\n", i+1, status, title)
		fmt.Printf("      ID:     %s\n", sess.ID[:min(12, len(sess.ID))])
		fmt.Printf("      模型:   %s  来源: %s\n", sess.Model, sess.Source)
		fmt.Printf("      消息:   %d  工具调用: %d  Token: %d\n",
			sess.MessageCount, sess.ToolCallCount, sess.InputTokens+sess.OutputTokens)
		if sess.EstimatedCostUSD > 0 {
			fmt.Printf("      费用:   $%.4f\n", sess.EstimatedCostUSD)
		}
		fmt.Println()
	}

	fmt.Printf("  共 %d 个会话\n", len(sessions))
}

func (c *SessionCommand) searchSessions(query string) {
	store, err := c.getStore()
	if err != nil {
		PrintError("打开数据库失败: %v", err)
	}
	defer store.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	fmt.Printf("搜索会话: %q\n\n", query)

	results, err := store.SearchMessages(ctx, query, 20)
	if err != nil {
		PrintError("搜索失败: %v", err)
	}

	if len(results) == 0 {
		fmt.Println(DimStyle.Render("  未找到匹配的会话消息"))
		return
	}

	fmt.Printf("找到 %d 条匹配消息:\n", len(results))
	fmt.Println(strings.Repeat("━", 70))

	for i, r := range results {
		fmt.Printf("  %2d. [会话 %s]  排名: %.4f\n", i+1, r.SessionID[:min(8, len(r.SessionID))], r.Rank)
		snippet := r.Content
		if len(snippet) > 100 {
			snippet = snippet[:100] + "..."
		}
		fmt.Printf("      %s\n", snippet)
		fmt.Println()
	}
}

func (c *SessionCommand) exportSession(sessionID string) {
	store, err := c.getStore()
	if err != nil {
		PrintError("打开数据库失败: %v", err)
	}
	defer store.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 获取会话信息
	session, err := store.GetSession(ctx, sessionID)
	if err != nil {
		PrintError("获取会话失败: %v", err)
	}

	// 获取消息
	messages, err := store.GetMessages(ctx, sessionID, 10000, 0)
	if err != nil {
		PrintError("获取消息失败: %v", err)
	}

	// 导出为 JSON
	export := map[string]any{
		"session":  session,
		"messages": messages,
	}

	jsonData, err := json.MarshalIndent(export, "", "  ")
	if err != nil {
		PrintError("序列化失败: %v", err)
	}

	// 写入文件
	outputFile := fmt.Sprintf("session_%s.json", sessionID[:min(8, len(sessionID))])
	if err := os.WriteFile(outputFile, jsonData, 0644); err != nil {
		PrintError("写入文件失败: %v", err)
	}

	PrintSuccess(fmt.Sprintf("会话已导出到: %s", outputFile))
}

func (c *SessionCommand) sessionStats() {
	store, err := c.getStore()
	if err != nil {
		PrintError("打开数据库失败: %v", err)
	}
	defer store.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sessions, err := store.ListRecentSessions(ctx, 1000)
	if err != nil {
		PrintError("查询会话失败: %v", err)
	}

	PrintTitle("会话统计")

	totalMessages := 0
	totalToolCalls := 0
	totalInputTokens := 0
	totalOutputTokens := 0
	totalCost := 0.0

	for _, s := range sessions {
		totalMessages += s.MessageCount
		totalToolCalls += s.ToolCallCount
		totalInputTokens += s.InputTokens
		totalOutputTokens += s.OutputTokens
		totalCost += s.EstimatedCostUSD
	}

	fmt.Printf("  会话总数:     %d\n", len(sessions))
	fmt.Printf("  消息总数:     %d\n", totalMessages)
	fmt.Printf("  工具调用总数: %d\n", totalToolCalls)
	fmt.Printf("  输入 Token:   %d\n", totalInputTokens)
	fmt.Printf("  输出 Token:   %d\n", totalOutputTokens)
	fmt.Printf("  总费用:       $%.4f\n", totalCost)
	fmt.Println()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func init() {
	Register(&SessionCommand{})
}
