// Package tool 提供澄清/确认工具。
// 允许代理向用户提出多选或开放性问题以获取澄清。
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// ───────────────────────────── 澄清回调机制 ─────────────────────────────

// ClarifyCallback 是用户澄清回调函数类型。
// question: 要呈现的问题文本
// choices: 可选的多选选项（最多4个）
// 返回用户的回答
type ClarifyCallback func(question string, choices []string) string

// 全局澄清回调（通过 Agent 或网关注入）
var (
	globalClarifyCallback ClarifyCallback
	clarifyCallbackMu     sync.RWMutex
)

// SetClarifyCallback 设置全局澄清回调。
// 通常在 Agent 初始化或网关启动时调用。
func SetClarifyCallback(cb ClarifyCallback) {
	clarifyCallbackMu.Lock()
	defer clarifyCallbackMu.Unlock()
	globalClarifyCallback = cb
}

// GetClarifyCallback 获取当前澄清回调。
func GetClarifyCallback() ClarifyCallback {
	clarifyCallbackMu.RLock()
	defer clarifyCallbackMu.RUnlock()
	return globalClarifyCallback
}

// ───────────────────────────── 最大选项数 ─────────────────────────────

// MaxClarifyChoices 最大预定义选项数（与 Python 版本保持一致）
const MaxClarifyChoices = 4

// ───────────────────────────── 澄清工具 ─────────────────────────────

// ClarifyTool 实现用户澄清/确认功能。
// 支持两种模式：多选问题（最多4个选项）或开放性问题。
// UI 会自动添加第5个"其他（输入你的答案）"选项。
type ClarifyTool struct{}

// Name 返回工具名称。
func (t *ClarifyTool) Name() string { return "clarify" }

// Description 返回工具描述。
func (t *ClarifyTool) Description() string {
	return `向用户提问以获取澄清、反馈或决策。支持两种模式：

1. **多选模式** — 提供最多4个选项。用户选择一个或通过第5个"其他"选项输入自定义答案。
2. **开放模式** — 不提供选项。用户自由输入答案。

适用场景：
- 任务模糊需要用户选择方案
- 需要后任务反馈（"效果如何？"）
- 想要保存技能或更新记忆
- 决策有重要权衡需要用户参与

不适用：危险命令的简单确认（terminal 工具已处理）。低风险决策应自行做合理默认选择。`
}

// Toolset 返回工具所属工具集。
func (t *ClarifyTool) Toolset() string { return "clarify" }

// Emoji 返回工具图标。
func (t *ClarifyTool) Emoji() string { return "❓" }

// IsAvailable 检查澄清工具是否可用。
// 需要有回调函数注入（CLI 或网关提供）。
func (t *ClarifyTool) IsAvailable() bool {
	return GetClarifyCallback() != nil
}

// MaxResultChars 返回结果最大字符数。
func (t *ClarifyTool) MaxResultChars() int { return 2000 }

// Schema 返回工具的 JSON Schema。
func (t *ClarifyTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "clarify",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"question": map[string]any{
					"type":        "string",
					"description": "要呈现给用户的问题文本。",
				},
				"choices": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "string",
					},
					"maxItems":     MaxClarifyChoices,
					"description":  "最多4个答案选项。省略此参数表示开放性问题。UI 自动添加'其他'选项。",
				},
			},
			"required": []string{"question"},
		},
	}
}

// Execute 执行澄清工具。
// 1. 验证问题文本
// 2. 处理选项列表（最多4个）
// 3. 调用回调获取用户输入
// 4. 返回 JSON 结果
func (t *ClarifyTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	// 检查回调可用性
	callback := GetClarifyCallback()
	if callback == nil {
		return ToolError("澄清工具在当前执行上下文中不可用。"), nil
	}

	// 获取问题文本
	question, ok := args["question"].(string)
	if !ok || question == "" {
		return ToolError("参数 question 是必填项且必须为非空字符串。"), nil
	}

	// 检查是否为纯空白
	trimmedQuestion := trimString(question)
	if trimmedQuestion == "" {
		return ToolError("参数 question 是必填项且必须为非空字符串。"), nil
	}
	question = trimmedQuestion

	// 处理选项列表
	var choices []string
	if choicesRaw, ok := args["choices"]; ok {
		choicesSlice, ok := choicesRaw.([]any)
		if !ok {
			return ToolError("参数 choices 必须是字符串数组。"), nil
		}
		// 转换并清理选项
		for i, c := range choicesSlice {
			if i >= MaxClarifyChoices {
				break // 最多保留4个
			}
			if str, ok := c.(string); ok && trimString(str) != "" {
				choices = append(choices, trimString(str))
			}
		}
		// 空列表视为开放问题
		if len(choices) == 0 {
			choices = nil
		}
	}

	// 调用回调获取用户输入
	userResponse := callback(question, choices)

	// 构建结果
	result := map[string]any{
		"question":      question,
		"choices_offered": choices,
		"user_response":  trimString(userResponse),
	}

	return ToolResult(result), nil
}

// trimString 去除字符串两端空白。
func trimString(s string) string {
	if s == "" {
		return s
	}
	// 去除首尾空白
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

// ───────────────────────────── init 注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&ClarifyTool{})
}

// ───────────────────────────── 辅助函数（供外部使用） ─────────────────────────────

// ClarifyResult 解析澄清工具返回结果。
type ClarifyResult struct {
	Question      string   `json:"question"`
	ChoicesOffered []string `json:"choices_offered"`
	UserResponse  string   `json:"user_response"`
}

// ParseClarifyResult 从 JSON 字符串解析澄清结果。
func ParseClarifyResult(jsonStr string) (*ClarifyResult, error) {
	var result ClarifyResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, fmt.Errorf("解析澄清结果失败: %w", err)
	}
	return &result, nil
}