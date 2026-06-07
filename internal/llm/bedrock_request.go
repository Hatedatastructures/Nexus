package llm

import "encoding/json"

// ── Bedrock API 类型 ──────────────────────────────────────────────────────

// bedrockConverseResponse Bedrock Converse API 响应。
type bedrockConverseResponse struct {
	Output     bedrockOutput   `json:"output"`
	StopReason string          `json:"stopReason,omitempty"`
	Usage      bedrockUsage    `json:"usage"`
	Metrics    *bedrockMetrics `json:"metrics,omitempty"`
}

// bedrockOutput Bedrock 输出。
type bedrockOutput struct {
	Message bedrockMessage `json:"message"`
}

// bedrockMessage Bedrock 消息。
type bedrockMessage struct {
	Role    string                `json:"role"`
	Content []bedrockContentBlock `json:"content"`
}

// bedrockContentBlock Bedrock 内容块。
// 注意：Bedrock Converse 中 tool_result 类型使用嵌套的 toolResult 对象。
// 此结构体支持 text、tool_use、tool_result 三种类型。
type bedrockContentBlock struct {
	Type       string                    `json:"type"`
	Text       string                    `json:"text,omitempty"`
	ToolUseID  string                    `json:"toolUseId,omitempty"`
	Name       string                    `json:"name,omitempty"`
	Input      map[string]any            `json:"input,omitempty"`
	ToolResult *bedrockToolResultContent `json:"toolResult,omitempty"`
}

// bedrockToolResultContent Bedrock tool_result 类型的嵌套内容结构。
type bedrockToolResultContent struct {
	ToolUseID string                  `json:"toolUseId"`
	Content   []bedrockToolResultPart `json:"content"`
	Status    string                  `json:"status,omitempty"`
}

// bedrockToolResultPart tool_result 中的单个内容部分。
type bedrockToolResultPart struct {
	Text string `json:"text,omitempty"`
	JSON any    `json:"json,omitempty"`
}

// bedrockUsage Bedrock token 用量。
type bedrockUsage struct {
	InputTokens      int `json:"inputTokens"`
	OutputTokens     int `json:"outputTokens"`
	TotalTokens      int `json:"totalTokens"`
	CacheReadTokens  int `json:"cacheReadInputTokens,omitempty"`
	CacheWriteTokens int `json:"cacheWriteInputTokens,omitempty"`
}

// bedrockMetrics Bedrock 性能指标。
type bedrockMetrics struct {
	LatencyMs int `json:"latencyMs"`
}

// bedrockStreamEvent Bedrock 流式 SSE 事件。
type bedrockStreamEvent struct {
	Type         string                     `json:"type"`
	Index        int                        `json:"index,omitempty"`
	ContentBlock *bedrockStreamContentBlock `json:"contentBlock,omitempty"`
	Delta        *bedrockStreamDelta        `json:"delta,omitempty"`
	Usage        *bedrockUsage              `json:"usage,omitempty"` // metadata 事件中的 token 用量
}

// bedrockStreamContentBlock Bedrock 流式内容块。
type bedrockStreamContentBlock struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	ToolUseID string `json:"toolUseId,omitempty"`
	Name      string `json:"name,omitempty"`
}

// bedrockStreamDelta Bedrock 流式增量。
type bedrockStreamDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	PartialJSON string `json:"partialJson,omitempty"`
	StopReason  string `json:"stopReason,omitempty"`
}

// ── Bedrock 请求体构建 ───────────────────────────────────────────────────

// bedrockRequestBody Bedrock Converse API 请求体。
type bedrockRequestBody struct {
	InferenceConfig              *bedrockInferenceConfig `json:"inferenceConfig,omitempty"`
	AdditionalModelRequestFields map[string]any          `json:"additionalModelRequestFields,omitempty"`
	Messages                     []bedrockRequestMessage `json:"messages"`
	System                       []bedrockSystemBlock    `json:"system,omitempty"`
	ToolConfig                   *bedrockToolConfig      `json:"toolConfig,omitempty"`
}

// bedrockInferenceConfig Bedrock 推理配置。
type bedrockInferenceConfig struct {
	MaxTokens     int      `json:"maxTokens,omitempty"`
	Temperature   float64  `json:"temperature,omitempty"`
	TopP          float64  `json:"topP,omitempty"`
	StopSequences []string `json:"stopSequences,omitempty"`
}

// bedrockRequestMessage Bedrock 请求消息。
type bedrockRequestMessage struct {
	Role    string                `json:"role"`
	Content []bedrockContentBlock `json:"content"`
}

// bedrockSystemBlock Bedrock 系统提示块。
type bedrockSystemBlock struct {
	Text string `json:"text"`
}

// bedrockToolConfig Bedrock 工具配置。
type bedrockToolConfig struct {
	Tools []bedrockTool `json:"tools,omitempty"`
}

// bedrockTool Bedrock 工具定义。
type bedrockTool struct {
	ToolSpec *bedrockToolSpec `json:"toolSpec,omitempty"`
}

// bedrockToolSpec Bedrock 工具规格。
type bedrockToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema"`
}

// buildBedrockRequestBody 构建 Bedrock Converse API 请求体。
func buildBedrockRequestBody(req *ChatRequest) *bedrockRequestBody {
	body := &bedrockRequestBody{
		Messages: make([]bedrockRequestMessage, 0, len(req.Messages)),
	}

	// 分离 system 消息
	var systemTexts []string
	for _, msg := range req.Messages {
		if msg.Role == RoleSystem {
			systemTexts = append(systemTexts, msg.Content)
			continue
		}
	}

	// 系统提示
	if len(systemTexts) > 0 {
		body.System = make([]bedrockSystemBlock, 0, len(systemTexts))
		for _, text := range systemTexts {
			body.System = append(body.System, bedrockSystemBlock{Text: text})
		}
	}

	// 转换非 system 消息
	for _, msg := range req.Messages {
		if msg.Role == RoleSystem {
			continue
		}
		bedMsg := convertMessageToBedrock(&msg)
		if bedMsg != nil {
			body.Messages = append(body.Messages, *bedMsg)
		}
	}

	// 推理配置
	inferCfg := &bedrockInferenceConfig{}
	hasInferCfg := false
	if req.MaxTokens > 0 {
		inferCfg.MaxTokens = req.MaxTokens
		hasInferCfg = true
	}
	if req.Temperature > 0 {
		inferCfg.Temperature = req.Temperature
		hasInferCfg = true
	}
	if hasInferCfg {
		body.InferenceConfig = inferCfg
	}

	// 工具定义
	if len(req.Tools) > 0 {
		tools := make([]bedrockTool, 0, len(req.Tools))
		for _, t := range req.Tools {
			params := t.Parameters
			paramsMap, ok := params.(map[string]any)
			if !ok {
				paramsMap = map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				}
			}
			tools = append(tools, bedrockTool{
				ToolSpec: &bedrockToolSpec{
					Name:        t.Name,
					Description: t.Description,
					InputSchema: paramsMap,
				},
			})
		}
		body.ToolConfig = &bedrockToolConfig{Tools: tools}
	}

	return body
}

// convertMessageToBedrock 将统一消息转换为 Bedrock 格式。
func convertMessageToBedrock(msg *Message) *bedrockRequestMessage {
	switch msg.Role {
	case RoleUser:
		content := []bedrockContentBlock{}
		if msg.Content != "" {
			content = append(content, bedrockContentBlock{
				Type: "text",
				Text: msg.Content,
			})
		}
		return &bedrockRequestMessage{
			Role:    "user",
			Content: content,
		}

	case RoleAssistant:
		content := []bedrockContentBlock{}
		// 文本内容
		if msg.Content != "" {
			content = append(content, bedrockContentBlock{
				Type: "text",
				Text: msg.Content,
			})
		}
		// 工具调用
		for _, tc := range msg.ToolCalls {
			var input map[string]any
			if err := json.Unmarshal([]byte(tc.Arguments), &input); err != nil {
				input = map[string]any{"_raw": tc.Arguments}
			}
			content = append(content, bedrockContentBlock{
				Type:      "tool_use",
				ToolUseID: tc.ID,
				Name:      tc.Name,
				Input:     input,
			})
		}
		// 确保不为空
		if len(content) == 0 {
			content = []bedrockContentBlock{{Type: "text", Text: "(empty)"}}
		}
		return &bedrockRequestMessage{
			Role:    "assistant",
			Content: content,
		}

	case RoleTool:
		content := msg.Content
		if content == "" {
			content = "(no output)"
		}
		// Bedrock tool_result 格式：{"toolResult": {"toolUseId": "...", "content": [{"text": "..."}]}}
		var resultParts []bedrockToolResultPart
		// 尝试解析为 JSON 对象，如果成功则作为 json 类型
		var jsonObj map[string]any
		if err := json.Unmarshal([]byte(content), &jsonObj); err == nil {
			resultParts = append(resultParts, bedrockToolResultPart{JSON: jsonObj})
		} else {
			resultParts = append(resultParts, bedrockToolResultPart{Text: content})
		}
		return &bedrockRequestMessage{
			Role: "user",
			Content: []bedrockContentBlock{{
				Type: "tool_result",
				ToolResult: &bedrockToolResultContent{
					ToolUseID: msg.ToolCallID,
					Content:   resultParts,
				},
			}},
		}

	default:
		return &bedrockRequestMessage{
			Role:    "user",
			Content: []bedrockContentBlock{{Type: "text", Text: msg.Content}},
		}
	}
}

// ── 工具函数 ─────────────────────────────────────────────────────────────

// mapBedrockStopReason 将 Bedrock stopReason 映射为统一的停止原因。
func mapBedrockStopReason(reason string, hasToolCalls bool) string {
	if hasToolCalls {
		return StopToolUse
	}
	switch reason {
	case "end_turn":
		return StopEndTurn
	case "max_tokens":
		return StopMaxTokens
	case "tool_use":
		return StopToolUse
	case "stop_sequence":
		return StopEndTurn
	case "content_filtered":
		return StopContentFilter
	default:
		return StopEndTurn
	}
}

// convertBedrockUsage 将 Bedrock usage 转换为统一的 TokenUsage。
func convertBedrockUsage(usage *bedrockUsage) *TokenUsage {
	if usage == nil {
		return nil
	}
	return &TokenUsage{
		PromptTokens:     usage.InputTokens,
		CompletionTokens: usage.OutputTokens,
		TotalTokens:      usage.TotalTokens,
		CacheReadTokens:  usage.CacheReadTokens,
		CacheWriteTokens: usage.CacheWriteTokens,
	}
}
