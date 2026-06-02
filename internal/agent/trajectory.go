// Package agent 提供轨迹记录功能。
// 将对话历史保存为 ShareGPT 格式的 JSONL 文件，用于训练数据生成。
package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nexus-agent/internal/llm"
)

// ───────────────────────────── 数据结构 ─────────────────────────────

// TrajectoryEntry 表示 ShareGPT 格式的一条轨迹记录。
type TrajectoryEntry struct {
	Conversations []TrajectoryTurn `json:"conversations"`
	Timestamp     string           `json:"timestamp"`
	Model         string           `json:"model"`
	Completed     bool             `json:"completed"`
}

// TrajectoryTurn 表示轨迹中的一轮对话。
type TrajectoryTurn struct {
	From    string `json:"from"`    // "system" / "human" / "gpt" / "tool"
	Value   string `json:"value"`   // 内容
	ToolUse string `json:"tool_use,omitempty"` // 工具名称 (仅 tool 角色)
}

func validateTrajectoryPath(path string) error {
	if path == "" {
		return fmt.Errorf("路径不能为空")
	}
	if strings.Contains(path, "..") {
		return fmt.Errorf("路径不允许包含 \"..\": %s", path)
	}
	if filepath.IsAbs(path) {
		return fmt.Errorf("不允许使用绝对路径: %s", path)
	}
	return nil
}

// ───────────────────────────── 保存函数 ─────────────────────────────

// SaveTrajectory 将对话消息保存为 ShareGPT 格式的 JSONL。
func SaveTrajectory(path string, msgs []llm.Message, model string, completed bool) error {
	if err := validateTrajectoryPath(path); err != nil {
		return fmt.Errorf("轨迹路径验证失败: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("创建轨迹文件: %w", err)
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	defer w.Flush()

	entry := convertToTrajectory(msgs, model, completed)
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("序列化轨迹: %w", err)
	}

	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("写入轨迹数据: %w", err)
	}
	w.WriteByte('\n')
	return nil
}

// SaveTrajectoryBatch 将多条轨迹追加到 JSONL 文件。
func SaveTrajectoryBatch(path string, entries []TrajectoryEntry) error {
	if err := validateTrajectoryPath(path); err != nil {
		return fmt.Errorf("轨迹路径验证失败: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("打开轨迹文件: %w", err)
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	defer w.Flush()

	for _, entry := range entries {
		data, err := json.Marshal(entry)
		if err != nil {
			continue
		}
		w.Write(data)
		w.WriteByte('\n')
	}
	return nil
}

// convertToTrajectory 将 llm.Message 列表转换为 ShareGPT 格式。
func convertToTrajectory(msgs []llm.Message, model string, completed bool) TrajectoryEntry {
	turns := make([]TrajectoryTurn, 0, len(msgs))

	for _, msg := range msgs {
		role := convertRole(msg.Role)
		content := msg.Content

		// 包装推理内容
		if msg.ReasoningContent != "" {
			content = ConvertScratchpadToThink(msg.ReasoningContent) + "\n\n" + content
		}

		turn := TrajectoryTurn{
			From:  role,
			Value: content,
		}

		// 工具调用: 添加为单独的 turn，同时保留文本内容
		if len(msg.ToolCalls) > 0 {
			// 先添加文本内容 (如果有)
			if content != "" {
				turns = append(turns, turn)
			}
			for _, tc := range msg.ToolCalls {
				turns = append(turns, TrajectoryTurn{
					From:    "gpt",
					Value:   fmt.Sprintf("<tool_call>%s\n%s", tc.Name, tc.Arguments),
					ToolUse: tc.Name,
				})
			}
		} else {
			turns = append(turns, turn)
		}
	}

	return TrajectoryEntry{
		Conversations: turns,
		Timestamp:     time.Now().Format(time.RFC3339),
		Model:         model,
		Completed:     completed,
	}
}

// convertRole 将 llm 角色转换为 ShareGPT 角色。
func convertRole(role llm.MessageRole) string {
	switch role {
	case llm.RoleSystem:
		return "system"
	case llm.RoleUser:
		return "human"
	case llm.RoleAssistant:
		return "gpt"
	case llm.RoleTool:
		return "tool"
	default:
		return string(role)
	}
}

// ───────────────────────────── Scratchpad 处理 ─────────────────────────────

// ConvertScratchpadToThink 将推理内容包装为 <think> 标签。
func ConvertScratchpadToThink(content string) string {
	if content == "" {
		return ""
	}
	// 已经包含标签则不重复包装
	if strings.HasPrefix(content, "<think>") {
		return content
	}
	return "<think>\n" + content + "\n</think>"
}

// HasIncompleteScratchpad 检测是否有未闭合的 think 块。
func HasIncompleteScratchpad(content string) bool {
	opens := strings.Count(content, "<think>")
	closes := strings.Count(content, "</think>")
	return opens > closes
}
