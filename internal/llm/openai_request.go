// Package llm 提供 LLM 提供者抽象层。
// openai_request.go 定义 OpenAI Chat Completions 的 API 类型、请求体构建和工具函数。
package llm

import (
	"strings"
)

// ── OpenAI API 类型 ────────────────────────────────────────────────────────

// openAIChatResponse OpenAI Chat Completions 响应结构。
type openAIChatResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []openAIChoice `json:"choices"`
	Usage   openAIUsage    `json:"usage"`
}

// openAIChoice 单个响应选项。
type openAIChoice struct {
	Index        int           `json:"index"`
	Message      openAIMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
}

// openAIMessage OpenAI 消息格式。
type openAIMessage struct {
	Role             string           `json:"role"`
	Content          string           `json:"content"`
	ToolCalls        []openAIToolCall `json:"tool_calls,omitempty"`
	ReasoningContent string           `json:"reasoning_content,omitempty"`
}

// openAIToolCall OpenAI 工具调用格式。
type openAIToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function openAIFunctionCall `json:"function"`
}

// openAIFunctionCall OpenAI 函数调用格式。
type openAIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// openAIUsage OpenAI token 用量。
type openAIUsage struct {
	PromptTokens        int                       `json:"prompt_tokens"`
	CompletionTokens    int                       `json:"completion_tokens"`
	TotalTokens         int                       `json:"total_tokens"`
	PromptTokensDetails *openAIPromptTokenDetails `json:"prompt_tokens_details,omitempty"`
}

// openAIPromptTokenDetails OpenAI 提示 token 详情（含缓存信息）。
type openAIPromptTokenDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
}

// openAIModelListResponse OpenAI 模型列表响应。
type openAIModelListResponse struct {
	Object string             `json:"object"`
	Data   []openAIModelEntry `json:"data"`
}

// openAIModelEntry 模型条目。
type openAIModelEntry struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// openAIStreamOptions 流式响应选项，用于请求 API 在流中返回 usage 信息。
type openAIStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// openAIRequestBody OpenAI Chat Completions 请求体。
type openAIRequestBody struct {
	Model         string                 `json:"model"`
	Messages      []openAIRequestMessage `json:"messages"`
	Tools         []openAIToolDef        `json:"tools,omitempty"`
	MaxTokens     int                    `json:"max_tokens,omitempty"`
	Temperature   *float64               `json:"temperature,omitempty"`
	Stream        bool                   `json:"stream,omitempty"`
	StreamOptions *openAIStreamOptions   `json:"stream_options,omitempty"`
}

// openAIRequestMessage OpenAI 请求消息。
type openAIRequestMessage struct {
	Role             string           `json:"role"`
	Content          string           `json:"content"`
	ToolCalls        []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string           `json:"tool_call_id,omitempty"`
	Name             string           `json:"name,omitempty"`
	ReasoningContent string           `json:"reasoning_content,omitempty"`
}

// openAIToolDef OpenAI 工具定义。
type openAIToolDef struct {
	Type     string            `json:"type"`
	Function openAIFunctionDef `json:"function"`
}

// openAIFunctionDef OpenAI 函数定义。
type openAIFunctionDef struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

// ── 请求体构建 ──────────────────────────────────────────────────────────────

// buildOpenAIRequestBody 从 ChatRequest 构建 OpenAI API 请求体。
func buildOpenAIRequestBody(req *ChatRequest) *openAIRequestBody {
	body := &openAIRequestBody{
		Model:    req.Model,
		Messages: make([]openAIRequestMessage, 0, len(req.Messages)),
	}

	// 转换消息
	for _, msg := range req.Messages {
		oaiMsg := openAIRequestMessage{
			Role:             string(msg.Role),
			Content:          msg.Content,
			ToolCallID:       msg.ToolCallID,
			Name:             msg.Name,
			ReasoningContent: msg.ReasoningContent,
		}
		// 转换工具调用
		if len(msg.ToolCalls) > 0 {
			oaiMsg.ToolCalls = make([]openAIToolCall, 0, len(msg.ToolCalls))
			for _, tc := range msg.ToolCalls {
				oaiMsg.ToolCalls = append(oaiMsg.ToolCalls, openAIToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: openAIFunctionCall{
						Name:      tc.Name,
						Arguments: tc.Arguments,
					},
				})
			}
		}
		body.Messages = append(body.Messages, oaiMsg)
	}

	// 转换工具定义
	if len(req.Tools) > 0 {
		body.Tools = make([]openAIToolDef, 0, len(req.Tools))
		for _, t := range req.Tools {
			body.Tools = append(body.Tools, openAIToolDef{
				Type:     "function",
				Function: openAIFunctionDef(t),
			})
		}
	}

	// MaxTokens
	if req.MaxTokens > 0 {
		body.MaxTokens = req.MaxTokens
	}

	// Temperature（0 表示使用模型默认值，仅当 > 0 时设置）
	if req.Temperature > 0 {
		temp := req.Temperature
		body.Temperature = &temp
	}

	// 流式标记（从 Metadata 读取）
	if stream, ok := req.Metadata["stream"].(bool); ok {
		body.Stream = stream
		// 启用流式 usage 返回，使 API 在最后一个 chunk 中包含 token 用量
		if stream {
			body.StreamOptions = &openAIStreamOptions{IncludeUsage: true}
		}
	}

	return body
}

// ── 工具函数 ──────────────────────────────────────────────────────────────

// mapOpenAIStopReason 将 OpenAI finish_reason 映射为统一的停止原因。
func mapOpenAIStopReason(reason string) string {
	switch reason {
	case "stop":
		return StopEndTurn
	case "length":
		return StopLength
	case "tool_calls":
		return StopToolUse
	case "content_filter":
		return StopContentFilter
	case "function_call":
		return StopToolUse
	default:
		if reason == "" {
			return StopEndTurn
		}
		return reason
	}
}

// convertOpenAIUsage 将 OpenAI usage 转换为统一的 TokenUsage。
func convertOpenAIUsage(usage *openAIUsage) *TokenUsage {
	if usage == nil {
		return nil
	}
	tu := &TokenUsage{
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		TotalTokens:      usage.TotalTokens,
	}
	if usage.PromptTokensDetails != nil {
		tu.CacheReadTokens = usage.PromptTokensDetails.CachedTokens
	}
	return tu
}

// buildAPIURL 智能构建 API 端点 URL。
// 如果 baseURL 已包含 /v1 等路径前缀，则直接拼接 endpoint；
// 否则插入 /v1 中间件路径。
// 示例:
//
//	baseURL="https://api.openai.com", endpoint="/chat/completions"
//	→ "https://api.openai.com/v1/chat/completions"
//
//	baseURL="https://dashscope.aliyuncs.com/compatible-mode/v1", endpoint="/chat/completions"
//	→ "https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions"
func buildAPIURL(baseURL, endpoint string) string {
	base := strings.TrimRight(baseURL, "/")
	hasV1Suffix := strings.HasSuffix(base, "/v1") || strings.Contains(base, "/v1/")
	hasCompat := strings.Contains(base, "/compatible-mode")
	if hasV1Suffix || hasCompat {
		return base + "/" + strings.TrimLeft(endpoint, "/")
	}
	return base + "/v1/" + strings.TrimLeft(endpoint, "/")
}
