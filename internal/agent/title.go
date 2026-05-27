// Package agent 提供自动对话标题生成功能。
// 在首次对话轮次后异步生成简洁标题，截取对话片段调用 LLM 生成。
package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"nexus-agent/internal/llm"
	"nexus-agent/internal/state"
)

// ───────────────────────────── 常量 ─────────────────────────────

const (
	titleSnippetLen  = 500 // 截取的对话片段最大字符数
	titleMaxLen      = 80  // 标题最大字符数
	titleTriggerTurn = 2   // 触发标题生成的对话轮次数
)

// ───────────────────────────── 生成函数 ─────────────────────────────

// GenerateTitle 调用 LLM 生成对话标题。
// 截取前几轮对话的 user/assistant 片段作为上下文。
func GenerateTitle(ctx context.Context, provider llm.Provider, messages []llm.Message) (string, error) {
	if provider == nil {
		return "", fmt.Errorf("LLM 提供者未设置")
	}

	// 构建标题生成提示
	snippets := extractSnippets(messages, 3)
	prompt := fmt.Sprintf(`为以下对话生成一个简洁的标题（不超过 %d 个字符）。
只输出标题文本，不要引号、前缀或解释。

%s`, titleMaxLen, snippets)

	req := &llm.ChatRequest{
		Model: provider.Name(),
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: prompt},
		},
		MaxTokens: 100,
	}

	resp, err := provider.CreateChatCompletion(ctx, req)
	if err != nil {
		return "", fmt.Errorf("生成标题失败: %w", err)
	}

	title := cleanTitle(resp.Content)
	if title == "" {
		return "", nil
	}

	slog.Debug("title generated", "title", title)
	return title, nil
}

// MaybeAutoTitle 在首次对话后异步生成标题。
// 当用户消息数达到触发阈值且会话尚无标题时执行。
func MaybeAutoTitle(ctx context.Context, provider llm.Provider, store *state.Store, sessionID string, messages []llm.Message) {
	if provider == nil || store == nil {
		return
	}

	// 统计用户消息数
	userCount := 0
	for _, msg := range messages {
		if msg.Role == llm.RoleUser {
			userCount++
		}
	}

	if userCount > titleTriggerTurn {
		return // 已过触发点
	}
	if userCount < titleTriggerTurn {
		return // 还未到触发点
	}

	// 检查是否已有标题
	sess, err := store.GetSession(ctx, sessionID)
	if err != nil || sess == nil || sess.Title != "" {
		return
	}

	// 异步生成（不传递 session 指针，避免跨 goroutine 数据竞争）
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Warn("auto title generation panic", "session_id", sessionID, "panic", r)
			}
		}()
		titleCtx, titleCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer titleCancel()

		title, err := GenerateTitle(titleCtx, provider, messages)
		if err != nil {
			slog.Warn("auto title generation failed", "session_id", sessionID, "err", err)
			return
		}
		if title != "" {
			// 重新获取 session 避免跨 goroutine 指针竞争
			sess, err := store.GetSession(titleCtx, sessionID)
			if err == nil && sess != nil {
				sess.Title = title
				if err := store.UpdateSession(titleCtx, sess); err != nil {
					slog.Warn("save title failed", "session_id", sessionID, "err", err)
				}
			}
		}
	}()
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

// extractSnippets 提取对话片段用于标题生成。
func extractSnippets(messages []llm.Message, maxTurns int) string {
	var b strings.Builder
	count := 0

	for _, msg := range messages {
		if msg.Role == llm.RoleSystem {
			continue
		}
		if count >= maxTurns*2 {
			break
		}

		role := string(msg.Role)
		if msg.Role == llm.RoleUser {
			role = "用户"
		} else if msg.Role == llm.RoleAssistant {
			role = "助手"
		} else {
			continue
		}

		content := msg.Content
		if len([]rune(content)) > titleSnippetLen {
			content = string([]rune(content)[:titleSnippetLen]) + "..."
		}

		b.WriteString(fmt.Sprintf("%s: %s\n", role, content))
		count++
	}

	return b.String()
}

// cleanTitle 清理 LLM 生成的标题。
func cleanTitle(title string) string {
	title = strings.TrimSpace(title)

	// 去除引号
	if len(title) >= 2 {
		if (title[0] == '"' && title[len(title)-1] == '"') ||
			(title[0] == '\'' && title[len(title)-1] == '\'') {
			title = title[1 : len(title)-1]
		}
	}
	// 去除中文引号 (多字节)
	title = strings.TrimPrefix(title, "「")
	title = strings.TrimSuffix(title, "」")

	// 去除 "Title: " 前缀
	title = strings.TrimPrefix(title, "Title: ")
	title = strings.TrimPrefix(title, "标题: ")
	title = strings.TrimPrefix(title, "标题：")

	// 截断
	runes := []rune(title)
	if len(runes) > titleMaxLen {
		title = string(runes[:titleMaxLen])
	}

	return strings.TrimSpace(title)
}
