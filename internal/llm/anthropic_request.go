package llm

import (
	"encoding/json"
	"strings"
)

// ── Anthropic API 类型 ─────────────────────────────────────────────────────

// anthropicMessagesResponse Anthropic Messages API 响应。
type anthropicMessagesResponse struct {
	ID         string                  `json:"id"`
	Type       string                  `json:"type"`
	Role       string                  `json:"role"`
	Model      string                  `json:"model"`
	Content    []anthropicContentBlock `json:"content"`
	StopReason string                  `json:"stop_reason"`
	Usage      anthropicUsage          `json:"usage"`
}

// anthropicContentBlock Anthropic 内容块。
// 不同 type 使用不同字段：text/thinking/tool_use/tool_result。
type anthropicContentBlock struct {
	Type         string         `json:"type"`
	Text         string         `json:"text,omitempty"`
	ID           string         `json:"id,omitempty"`
	Name         string         `json:"name,omitempty"`
	Input        map[string]any `json:"input,omitempty"`
	Thinking     string         `json:"thinking,omitempty"`
	Signature    string         `json:"signature,omitempty"`
	ToolUseID    string         `json:"tool_use_id,omitempty"`
	Content      any            `json:"content,omitempty"` // tool_result 的内容（string 或 []block）
	CacheControl *cacheControl  `json:"cache_control,omitempty"`
}

// anthropicUsage Anthropic token 用量。
type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
}

// ── Anthropic 请求体构建 ───────────────────────────────────────────────────

// anthropicRequestBody Anthropic Messages API 请求体。
type anthropicRequestBody struct {
	Model       string             `json:"model"`
	Messages    []anthropicMessage `json:"messages"`
	System      any                `json:"system,omitempty"` // string 或 []anthropicSystemBlock
	Tools       []anthropicToolDef `json:"tools,omitempty"`
	MaxTokens   int                `json:"max_tokens"`
	Stream      bool               `json:"stream,omitempty"`
	Thinking    map[string]any     `json:"thinking,omitempty"`
	Temperature *float64           `json:"temperature,omitempty"`
	ToolChoice  any                `json:"tool_choice,omitempty"`
}

// anthropicMessage Anthropic 消息格式。
type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string 或 []anthropicContentBlock
}

// anthropicSystemBlock 系统提示词内容块（支持 cache_control）。
type anthropicSystemBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

// cacheControl Anthropic 缓存控制。
type cacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

// anthropicToolDef Anthropic 工具定义。
type anthropicToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

// buildAnthropicRequestBody 构建 Anthropic Messages API 请求体。
func buildAnthropicRequestBody(req *ChatRequest) *anthropicRequestBody {
	body := &anthropicRequestBody{
		Model:     req.Model,
		Messages:  make([]anthropicMessage, 0, len(req.Messages)),
		MaxTokens: DefaultAnthropicMaxTokens,
	}

	// 覆盖 max_tokens
	if req.MaxTokens > 0 {
		body.MaxTokens = req.MaxTokens
	}

	// 分离 system 消息
	systemContent := extractAnthropicSystemMessages(req.Messages)
	if systemContent != "" {
		cleanText, shouldCache := extractCacheControl(systemContent)
		block := anthropicSystemBlock{
			Type: "text",
			Text: cleanText,
		}
		if shouldCache {
			block.CacheControl = &cacheControl{Type: "ephemeral"}
		}
		body.System = []anthropicSystemBlock{block}
	}

	// 转换非 system 消息
	nonSystem := make([]Message, 0)
	for _, msg := range req.Messages {
		if msg.Role == RoleSystem {
			continue
		}
		nonSystem = append(nonSystem, msg)
	}

	// 清理孤儿 tool block
	nonSystem = stripOrphanedToolBlocks(nonSystem)

	// 合并连续相同 role
	nonSystem = mergeConsecutiveRoles(nonSystem)

	// 淘汰旧截图
	nonSystem = evictOldScreenshots(nonSystem, 3)

	for i := range nonSystem {
		anthropicMsg := convertMessageToAnthropic(&nonSystem[i])
		body.Messages = append(body.Messages, anthropicMsg)
	}

	// 转换工具定义
	if len(req.Tools) > 0 {
		body.Tools = make([]anthropicToolDef, 0, len(req.Tools))
		for _, t := range req.Tools {
			toolDef := anthropicToolDef{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				},
			}
			// 如果 Parameters 已经是 map，直接使用
			if paramsMap, ok := t.Parameters.(map[string]any); ok {
				toolDef.InputSchema = paramsMap
				// 确保有 type 字段
				if _, hasType := toolDef.InputSchema["type"]; !hasType {
					toolDef.InputSchema["type"] = "object"
				}
			}
			body.Tools = append(body.Tools, toolDef)
		}
	}

	// 温度
	if req.Temperature > 0 {
		temp := req.Temperature
		body.Temperature = &temp
	}

	// 流式标记
	if stream, ok := req.Metadata["stream"].(bool); ok {
		body.Stream = stream
	}

	// 思维链配置（从 Metadata 读取）
	if thinkingCfg, ok := req.Metadata["thinking"].(map[string]any); ok {
		body.Thinking = thinkingCfg
	}

	return body
}

// extractCacheControl 检测 content 中的缓存标记字符串，移除它们并返回清理后的文本和是否需要设置 cache_control。
// 支持两种标记格式: HTML 注释和方括号格式。
func extractCacheControl(content string) (cleaned string, shouldCache bool) {
	if strings.Contains(content, cacheMarkerHTML) {
		content = strings.ReplaceAll(content, cacheMarkerHTML, "")
		shouldCache = true
	}
	if strings.Contains(content, cacheMarkerBracket) {
		content = strings.ReplaceAll(content, cacheMarkerBracket, "")
		shouldCache = true
	}
	return strings.TrimSpace(content), shouldCache
}

// extractAnthropicSystemMessages 从消息列表中提取所有 system 消息，合并为单个字符串。
func extractAnthropicSystemMessages(messages []Message) string {
	var parts []string
	for _, msg := range messages {
		if msg.Role == RoleSystem && msg.Content != "" {
			parts = append(parts, msg.Content)
		}
	}
	return strings.Join(parts, "\n")
}

// convertMessageToAnthropic 将统一消息转换为 Anthropic 格式。
func convertMessageToAnthropic(msg *Message) anthropicMessage {
	switch msg.Role {
	case RoleUser:
		// 检查 Content 是否包含缓存标记
		cleanText, shouldCache := extractCacheControl(msg.Content)
		if shouldCache || msg.Content != cleanText {
			// 含缓存标记或文本被修改，使用 content block 格式
			return anthropicMessage{
				Role: "user",
				Content: []anthropicContentBlock{{
					Type: "text",
					Text: cleanText,
					CacheControl: func() *cacheControl {
						if shouldCache {
							return &cacheControl{Type: "ephemeral"}
						}
						return nil
					}(),
				}},
			}
		}
		return anthropicMessage{
			Role:    "user",
			Content: msg.Content,
		}
	case RoleAssistant:
		blocks := make([]anthropicContentBlock, 0)

		// 文本内容
		if msg.Content != "" {
			cleanText, shouldCache := extractCacheControl(msg.Content)
			block := anthropicContentBlock{
				Type: "text",
				Text: cleanText,
			}
			if shouldCache {
				block.CacheControl = &cacheControl{Type: "ephemeral"}
			}
			blocks = append(blocks, block)
		}

		// 工具调用转换为 tool_use 块
		for i, tc := range msg.ToolCalls {
			var input map[string]any
			if err := json.Unmarshal([]byte(tc.Arguments), &input); err != nil {
				input = map[string]any{"_raw": tc.Arguments}
			}
			block := anthropicContentBlock{
				Type:  "tool_use",
				ID:    sanitizeAnthropicID(tc.ID),
				Name:  tc.Name,
				Input: input,
			}
			// 最后一个 ToolCall 上可能有来自 prompt_cache.go 的 cache_control Extra
			if i == len(msg.ToolCalls)-1 && tc.Extra != nil {
				if _, ok := tc.Extra["cache_control"]; ok {
					block.CacheControl = &cacheControl{Type: "ephemeral"}
				}
			}
			blocks = append(blocks, block)
		}

		// 确保不为空（Anthropic 拒绝空 assistant 内容）
		if len(blocks) == 0 {
			blocks = []anthropicContentBlock{{
				Type: "text",
				Text: "(empty)",
			}}
		}

		return anthropicMessage{
			Role:    "assistant",
			Content: blocks,
		}
	case RoleTool:
		content := msg.Content
		if content == "" {
			content = "(no output)"
		}
		cleanContent, shouldCache := extractCacheControl(content)
		block := anthropicContentBlock{
			Type:      "tool_result",
			ToolUseID: sanitizeAnthropicID(msg.ToolCallID),
			Content:   cleanContent,
		}
		if shouldCache {
			block.CacheControl = &cacheControl{Type: "ephemeral"}
		}
		return anthropicMessage{
			Role:    "user",
			Content: []anthropicContentBlock{block},
		}
	default:
		return anthropicMessage{
			Role:    "user",
			Content: msg.Content,
		}
	}
}

// ── 工具函数 ──────────────────────────────────────────────────────────────

// mapAnthropicStopReason 将 Anthropic stop_reason 映射为统一的停止原因。
func mapAnthropicStopReason(reason string) string {
	switch reason {
	case "end_turn":
		return StopEndTurn
	case "tool_use":
		return StopToolUse
	case "max_tokens":
		return StopMaxTokens
	case "stop_sequence":
		return StopEndTurn
	case "refusal":
		return StopContentFilter
	default:
		return StopEndTurn
	}
}

// convertAnthropicUsage 将 Anthropic usage 转换为统一的 TokenUsage。
func convertAnthropicUsage(usage *anthropicUsage) *TokenUsage {
	if usage == nil {
		return nil
	}
	return &TokenUsage{
		PromptTokens:     usage.InputTokens,
		CompletionTokens: usage.OutputTokens,
		TotalTokens:      usage.InputTokens + usage.OutputTokens,
		CacheReadTokens:  usage.CacheReadInputTokens,
		CacheWriteTokens: usage.CacheCreationInputTokens,
	}
}

