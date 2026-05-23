// Package llm 提供 LLM 提供者抽象层。
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// ── 常量 ───────────────────────────────────────────────────────────────────

const (
	// AnthropicVersion 为 Anthropic API 版本头。
	AnthropicVersion = "2023-06-01"

	// DefaultAnthropicBaseURL 为 Anthropic API 的默认基础 URL。
	DefaultAnthropicBaseURL = "https://api.anthropic.com"

	// DefaultAnthropicMaxTokens 为 Anthropic 请求的默认最大输出 token 数。
	DefaultAnthropicMaxTokens = 16384

	// AnthropicCacheCount 为 Anthropic 最大缓存断点数。
	AnthropicCacheCount = 4

	// prompt caching 文本标记 — 由 caching.go 和 prompt_cache.go 写入 Content
	cacheMarkerHTML    = "<!-- cache_control: ephemeral -->"
	cacheMarkerBracket = "[cache_control:ephemeral]"
)

// ── Anthropic 传输层 ──────────────────────────────────────────────────────

// AnthropicTransport 实现 Anthropic Messages API 的请求构建和响应解析。
// 处理系统提示词分离、缓存控制、思维链签名管理、以及消息格式转换。
type AnthropicTransport struct {
	httpClient *http.Client
	baseURL    string
}

// NewAnthropicTransport 创建新的 Anthropic 传输层。
func NewAnthropicTransport(httpClient *http.Client, baseURL string) *AnthropicTransport {
	if baseURL == "" {
		baseURL = DefaultAnthropicBaseURL
	}
	return &AnthropicTransport{
		httpClient: httpClient,
		baseURL:    strings.TrimRight(baseURL, "/"),
	}
}

// APIMode 返回 API 模式标识。
func (t *AnthropicTransport) APIMode() string {
	return "anthropic_messages"
}

// BuildRequest 构建 Anthropic Messages API HTTP 请求。
// 将统一消息格式转换为 Anthropic 原生格式。
func (t *AnthropicTransport) BuildRequest(ctx context.Context, req *ChatRequest, apiKey string) (any, error) {
	body := buildAnthropicRequestBody(req)

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化 Anthropic 请求体失败: %w", err)
	}

	url := t.baseURL + "/v1/messages"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("创建 HTTP 请求失败: %w", err)
	}

	// 设置请求头
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("anthropic-version", AnthropicVersion)

	// 认证头：OAuth token 用 Bearer，API key 用 x-api-key
	if strings.HasPrefix(apiKey, "sk-ant-api") {
		httpReq.Header.Set("x-api-key", apiKey)
	} else if apiKey != "" {
		// OAuth token 或其他 Bearer 认证
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}

	// 支持 GetBody 以便重试
	httpReq.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(bodyBytes)), nil
	}

	return httpReq, nil
}

// ParseResponse 解析 Anthropic Messages API 响应体为统一的 ChatResponse。
func (t *AnthropicTransport) ParseResponse(body []byte) (*ChatResponse, error) {
	var anthropicResp anthropicMessagesResponse
	if err := json.Unmarshal(body, &anthropicResp); err != nil {
		return nil, fmt.Errorf("解析 Anthropic 响应失败: %w", err)
	}

	response := &ChatResponse{
		ID:    anthropicResp.ID,
		Model: anthropicResp.Model,
	}

	// 解析 content 块
	var textParts []string
	var toolCalls []ToolCall
	var reasoningParts []string

	for _, block := range anthropicResp.Content {
		switch block.Type {
		case "text":
			if block.Text != "" {
				textParts = append(textParts, block.Text)
			}
		case "tool_use":
			argsJSON, _ := json.Marshal(block.Input)
			toolCalls = append(toolCalls, ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: string(argsJSON),
			})
		case "thinking":
			if block.Thinking != "" {
				reasoningParts = append(reasoningParts, block.Thinking)
			}
		case "redacted_thinking":
			// 被编辑的思维内容，保留占位符
			reasoningParts = append(reasoningParts, "[redacted]")
		}
	}

	response.Content = strings.Join(textParts, "\n")
	response.ToolCalls = toolCalls
	response.Reasoning = strings.Join(reasoningParts, "\n\n")

	// 映射停止原因
	response.StopReason = mapAnthropicStopReason(anthropicResp.StopReason)

	// token 用量
	response.Usage = convertAnthropicUsage(&anthropicResp.Usage)

	// 缓存命中检测
	if response.Usage != nil && (response.Usage.CacheReadTokens > 0 || response.Usage.CacheWriteTokens > 0) {
		response.CachedPrompt = true
	}

	return response, nil
}

// ParseStream 解析 Anthropic 流式 SSE 响应，返回 StreamDelta 通道。
func (t *AnthropicTransport) ParseStream(ctx context.Context, body io.ReadCloser) <-chan *StreamDelta {
	ch := make(chan *StreamDelta, 256)

	go func() {
		defer close(ch)
		defer body.Close() // 确保响应体被关闭，防止资源泄漏

		var contentBuilder strings.Builder
		var reasoningBuilder strings.Builder
		toolCallBuilders := make(map[int]*toolCallBuilder)
		var inputTokens, outputTokens int // 累积的 input/output token 计数

		for event := range ParseSSEStream(ctx, body) {
			select {
			case <-ctx.Done():
				return
			default:
			}

			if event.Data == "" {
				continue
			}

			// 检查是否是 message_stop 事件（message_delta 不再提前拦截，需要解析 usage）
			if event.Event == "message_stop" {
				// 流结束事件，发送最终增量
				var toolCalls []ToolCall
				for _, builder := range toolCallBuilders {
					toolCalls = append(toolCalls, ToolCall{
						ID:        builder.ID,
						Name:      builder.Name,
						Arguments: builder.Arguments.String(),
					})
				}
				ch <- &StreamDelta{
					Content:   contentBuilder.String(),
					ToolCalls: toolCalls,
					Reasoning: reasoningBuilder.String(),
					Usage: &TokenUsage{
						PromptTokens:     inputTokens,
						CompletionTokens: outputTokens,
						TotalTokens:      inputTokens + outputTokens,
					},
					Done: true,
				}
				return
			}

			var sseEvent anthropicSSEEvent
			if err := json.Unmarshal([]byte(event.Data), &sseEvent); err != nil {
				slog.Debug("failed to parse Anthropic SSE event", "error", err)
				continue
			}

			switch sseEvent.Type {
			case "message_start":
				// message_start 事件包含 input token 用量
				if sseEvent.Message != nil && sseEvent.Message.Usage != nil {
					inputTokens = sseEvent.Message.Usage.InputTokens
				}

			case "content_block_start":
				handleContentBlockStart(&sseEvent, toolCallBuilders)

			case "content_block_delta":
				handleContentBlockDelta(&sseEvent, &contentBuilder, &reasoningBuilder, toolCallBuilders, ch)

			case "content_block_stop":
				// 内容块结束，不发送事件

			case "message_delta":
				// message_delta 事件包含 output token 用量
				if sseEvent.Usage != nil {
					outputTokens = sseEvent.Usage.OutputTokens
				}
				if sseEvent.Delta != nil {
					if sseEvent.Delta.StopReason != "" {
						var toolCalls []ToolCall
						for _, b := range toolCallBuilders {
							toolCalls = append(toolCalls, ToolCall{
								ID:        b.ID,
								Name:      b.Name,
								Arguments: b.Arguments.String(),
							})
						}
						ch <- &StreamDelta{
							Content:   contentBuilder.String(),
							ToolCalls: toolCalls,
							Reasoning: reasoningBuilder.String(),
							Usage: &TokenUsage{
								PromptTokens:     inputTokens,
								CompletionTokens: outputTokens,
								TotalTokens:      inputTokens + outputTokens,
								CacheReadTokens:  sseEvent.Usage.CacheReadInputTokens,
							},
							Done: true,
						}
						return
					}
				}

			case "ping":
				// 保持连接活跃的心跳事件，忽略
			}
		}
	}()

	return ch
}

// ── Anthropic Provider 实现 ────────────────────────────────────────────────

// AnthropicProvider 实现 Anthropic Messages API 的 Provider 接口。
type AnthropicProvider struct {
	transport *AnthropicTransport
	apiKey    string
	model     string
}

// NewAnthropicProvider 创建一个新的 Anthropic 提供者。
func NewAnthropicProvider(httpClient *http.Client, apiKey, model, baseURL string) *AnthropicProvider {
	if baseURL == "" {
		baseURL = DefaultAnthropicBaseURL
	}
	return &AnthropicProvider{
		transport: NewAnthropicTransport(httpClient, baseURL),
		apiKey:    apiKey,
		model:     model,
	}
}

// Name 返回提供者标识。
func (p *AnthropicProvider) Name() string {
	return "anthropic"
}

// CreateChatCompletion 发送非流式聊天补全请求。
func (p *AnthropicProvider) CreateChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	if req.Model == "" {
		reqCopy := *req
		reqCopy.Model = p.model
		req = &reqCopy
	}

	httpReq, err := p.transport.BuildRequest(ctx, req, p.apiKey)
	if err != nil {
		return nil, err
	}

	httpReqTyped, ok := httpReq.(*http.Request)
	if !ok {
		return nil, fmt.Errorf("BuildRequest 返回类型不是 *http.Request")
	}

	resp, err := p.transport.httpClient.Do(httpReqTyped)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应体失败: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyStr := string(body)
		classified := ClassifyError(resp.StatusCode, bodyStr)
		return nil, fmt.Errorf("Anthropic API 错误 (HTTP %d, %s): %s", resp.StatusCode, classified.Reason, bodyStr)
	}

	return p.transport.ParseResponse(body)
}

// CreateChatCompletionStream 发送流式聊天补全请求。
func (p *AnthropicProvider) CreateChatCompletionStream(ctx context.Context, req *ChatRequest) (<-chan *StreamDelta, error) {
	if req.Model == "" {
		reqCopy := *req
		reqCopy.Model = p.model
		req = &reqCopy
	}

	if req.Metadata == nil {
		req.Metadata = make(map[string]any)
	}
	req.Metadata["stream"] = true

	httpReq, err := p.transport.BuildRequest(ctx, req, p.apiKey)
	if err != nil {
		return nil, err
	}

	httpReqTyped, ok := httpReq.(*http.Request)
	if !ok {
		return nil, fmt.Errorf("BuildRequest 返回类型不是 *http.Request")
	}

	httpReqTyped.Header.Set("Accept", "text/event-stream")

	resp, err := p.transport.httpClient.Do(httpReqTyped)
	if err != nil {
		return nil, fmt.Errorf("HTTP 流式请求失败: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		bodyStr := string(body)
		classified := ClassifyError(resp.StatusCode, bodyStr)
		return nil, fmt.Errorf("Anthropic 流式 API 错误 (HTTP %d, %s): %s", resp.StatusCode, classified.Reason, bodyStr)
	}

	// 直接将 HTTP 响应体传递给 ParseStream 进行真正的流式解析。
	// 不再使用 io.ReadAll 将整个响应读入内存，避免大响应导致 OOM。
	// resp.Body 的生命周期由 ParseStream 内部的 goroutine 通过 defer body.Close() 管理。
	return p.transport.ParseStream(ctx, resp.Body), nil
}

// ListModels 返回可用的模型列表。
func (p *AnthropicProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	// Anthropic 没有公开的 list models 端点，返回已知模型列表
	return []ModelInfo{
		{ID: "claude-opus-4-7", Provider: p.Name(), ContextLimit: 200000, MaxOutput: 128000, Vision: true, Reasoning: true},
		{ID: "claude-opus-4-6", Provider: p.Name(), ContextLimit: 200000, MaxOutput: 128000, Vision: true, Reasoning: true},
		{ID: "claude-sonnet-4-6", Provider: p.Name(), ContextLimit: 200000, MaxOutput: 64000, Vision: true, Reasoning: true},
		{ID: "claude-opus-4-5", Provider: p.Name(), ContextLimit: 200000, MaxOutput: 64000, Vision: true, Reasoning: true},
		{ID: "claude-sonnet-4-5", Provider: p.Name(), ContextLimit: 200000, MaxOutput: 64000, Vision: true, Reasoning: true},
		{ID: "claude-haiku-4-5", Provider: p.Name(), ContextLimit: 200000, MaxOutput: 64000, Vision: true, Reasoning: false},
		{ID: "claude-opus-4", Provider: p.Name(), ContextLimit: 200000, MaxOutput: 32000, Vision: true, Reasoning: true},
		{ID: "claude-sonnet-4", Provider: p.Name(), ContextLimit: 200000, MaxOutput: 64000, Vision: true, Reasoning: true},
		{ID: "claude-3-7-sonnet", Provider: p.Name(), ContextLimit: 200000, MaxOutput: 128000, Vision: true, Reasoning: true},
		{ID: "claude-3-5-sonnet", Provider: p.Name(), ContextLimit: 200000, MaxOutput: 8192, Vision: true, Reasoning: false},
		{ID: "claude-3-5-haiku", Provider: p.Name(), ContextLimit: 200000, MaxOutput: 8192, Vision: true, Reasoning: false},
	}, nil
}

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
	Type       string         `json:"type"`
	Text       string         `json:"text,omitempty"`
	ID         string         `json:"id,omitempty"`
	Name       string         `json:"name,omitempty"`
	Input      map[string]any `json:"input,omitempty"`
	Thinking   string         `json:"thinking,omitempty"`
	Signature  string         `json:"signature,omitempty"`
	ToolUseID  string         `json:"tool_use_id,omitempty"`
	Content    any            `json:"content,omitempty"`     // tool_result 的内容（string 或 []block）
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

// anthropicUsage Anthropic token 用量。
type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
}

// anthropicSSEEvent Anthropic SSE 流事件。
type anthropicSSEEvent struct {
	Type         string                    `json:"type"`
	Index        int                       `json:"index,omitempty"`
	Delta        *anthropicStreamDelta     `json:"delta,omitempty"`
	ContentBlock *anthropicContentBlock    `json:"content_block,omitempty"`
	Message      *anthropicStreamMessage   `json:"message,omitempty"`  // message_start 中的 message
	Usage        *anthropicUsage           `json:"usage,omitempty"`    // message_delta 中的 usage
}

// anthropicStreamMessage message_start 事件中的 message 对象（含 input token 用量）。
type anthropicStreamMessage struct {
	ID    string          `json:"id"`
	Model string          `json:"model"`
	Usage *anthropicUsage `json:"usage,omitempty"`
}

// anthropicStreamDelta Anthropic 流式增量。
type anthropicStreamDelta struct {
	Type       string `json:"type,omitempty"`
	Text       string `json:"text,omitempty"`
	Thinking   string `json:"thinking,omitempty"`
	StopReason string `json:"stop_reason,omitempty"`
}

// ── Anthropic 请求体构建 ───────────────────────────────────────────────────

// anthropicRequestBody Anthropic Messages API 请求体。
type anthropicRequestBody struct {
	Model      string                   `json:"model"`
	Messages   []anthropicMessage       `json:"messages"`
	System     any                      `json:"system,omitempty"` // string 或 []anthropicSystemBlock
	Tools      []anthropicToolDef       `json:"tools,omitempty"`
	MaxTokens  int                      `json:"max_tokens"`
	Stream     bool                     `json:"stream,omitempty"`
	Thinking   map[string]any           `json:"thinking,omitempty"`
	Temperature *float64                 `json:"temperature,omitempty"`
	ToolChoice any                      `json:"tool_choice,omitempty"`
}

// anthropicMessage Anthropic 消息格式。
type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string 或 []anthropicContentBlock
}

// anthropicSystemBlock 系统提示词内容块（支持 cache_control）。
type anthropicSystemBlock struct {
	Type         string          `json:"type"`
	Text         string          `json:"text"`
	CacheControl *cacheControl   `json:"cache_control,omitempty"`
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
	for _, msg := range req.Messages {
		if msg.Role == RoleSystem {
			continue
		}

		anthropicMsg := convertMessageToAnthropic(&msg)
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
					Type:         "text",
					Text:         cleanText,
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

// ── 流式处理辅助函数 ──────────────────────────────────────────────────────

// handleContentBlockStart 处理 content_block_start 事件。
func handleContentBlockStart(event *anthropicSSEEvent, builders map[int]*toolCallBuilder) {
	if event.ContentBlock == nil {
		return
	}
	if event.ContentBlock.Type == "tool_use" {
		builders[event.Index] = &toolCallBuilder{
			ID:   event.ContentBlock.ID,
			Name: event.ContentBlock.Name,
		}
	}
}

// handleContentBlockDelta 处理 content_block_delta 事件。
func handleContentBlockDelta(
	event *anthropicSSEEvent,
	contentBuilder *strings.Builder,
	reasoningBuilder *strings.Builder,
	builders map[int]*toolCallBuilder,
	ch chan<- *StreamDelta,
) {
	if event.Delta == nil {
		return
	}

	switch event.Delta.Type {
	case "text_delta":
		if event.Delta.Text != "" {
			contentBuilder.WriteString(event.Delta.Text)
			ch <- &StreamDelta{
				Content: event.Delta.Text,
			}
		}
	case "thinking_delta":
		if event.Delta.Thinking != "" {
			reasoningBuilder.WriteString(event.Delta.Thinking)
			ch <- &StreamDelta{
				Reasoning: event.Delta.Thinking,
			}
		}
	case "input_json_delta":
		// 工具调用参数增量
		if builder, ok := builders[event.Index]; ok && event.Delta.Text != "" {
			builder.Arguments.WriteString(event.Delta.Text)
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

// sanitizeAnthropicID 清理工具调用 ID，确保符合 Anthropic 的 [a-zA-Z0-9_-] 格式。
func sanitizeAnthropicID(id string) string {
	if id == "" {
		return "tool_0"
	}
	var result strings.Builder
	for _, c := range id {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' {
			result.WriteRune(c)
		} else {
			result.WriteRune('_')
		}
	}
	sanitized := result.String()
	if sanitized == "" {
		return "tool_0"
	}
	return sanitized
}

// ── anthropicContentBlock 扩展字段 ────────────────────────────────────────

// 注意：anthropicContentBlock 在流式和非流式场景共享。
// tool_result 类型需要额外字段，通过匿名嵌入方式添加。
// 以下字段在 Marshal 时自动包含。

// MarshalJSON 自定义序列化以确保 tool_result 块有正确字段。
// 注意：此方法已由结构体标签覆盖，仅在需要特殊处理时使用。

// ── init 注册 ─────────────────────────────────────────────────────────────

func init() {
	RegisterTransport("anthropic_messages", &AnthropicTransport{baseURL: DefaultAnthropicBaseURL})
	slog.Debug("Anthropic transport registered", "apiMode", "anthropic_messages", "time", time.Now())
}

// ── tool_result 相关 ──────────────────────────────────────────────────────

// 注意：anthropicContentBlock 已包含 tool_result 所需的 Content 字段。
// 在 JSON 序列化时，Content 字段的类型是 string，但 Anthropic API 也接受
// []anthropicContentBlock 作为 tool_result 的内容。
// 为此，我们在转换时使用 map[string]any 来表示 tool_result。
// 参见 convertMessageToAnthropic 中 RoleTool 的处理。
