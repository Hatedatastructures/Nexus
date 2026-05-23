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

// ── OpenAI 传输层 ─────────────────────────────────────────────────────────

// OpenAITransport 实现 OpenAI Chat Completions API 的请求构建和响应解析。
// 兼容所有使用 Chat Completions 格式的提供者（OpenRouter、DeepSeek、Qwen 等）。
type OpenAITransport struct {
	httpClient *http.Client
	baseURL    string
}

// NewOpenAITransport 创建新的 OpenAI 传输层。
// baseURL 默认为 "https://api.openai.com" 但可配置为其他兼容端点。
func NewOpenAITransport(httpClient *http.Client, baseURL string) *OpenAITransport {
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	return &OpenAITransport{
		httpClient: httpClient,
		baseURL:    strings.TrimRight(baseURL, "/"),
	}
}

// APIMode 返回 API 模式标识。
func (t *OpenAITransport) APIMode() string {
	return "chat_completions"
}

// BuildRequest 构建 OpenAI Chat Completions HTTP 请求。
// 返回 *http.Request 实例。
func (t *OpenAITransport) BuildRequest(ctx context.Context, req *ChatRequest, apiKey string) (any, error) {
	// 转换为 OpenAI 格式的请求体
	body := buildOpenAIRequestBody(req)

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化 OpenAI 请求体失败: %w", err)
	}

	url := buildAPIURL(t.baseURL, "/chat/completions")
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("创建 HTTP 请求失败: %w", err)
	}

	// 设置请求头
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	if apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}

	// 支持 GetBody 以便重试
	httpReq.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(bodyBytes)), nil
	}

	return httpReq, nil
}

// ParseResponse 解析 OpenAI Chat Completions 响应体为统一的 ChatResponse。
func (t *OpenAITransport) ParseResponse(body []byte) (*ChatResponse, error) {
	var oaiResp openAIChatResponse
	if err := json.Unmarshal(body, &oaiResp); err != nil {
		return nil, fmt.Errorf("解析 OpenAI 响应失败: %w", err)
	}

	if len(oaiResp.Choices) == 0 {
		return nil, fmt.Errorf("OpenAI 响应中没有 choices")
	}

	choice := oaiResp.Choices[0]
	msg := choice.Message

	response := &ChatResponse{
		ID:      oaiResp.ID,
		Model:   oaiResp.Model,
		Content: strings.TrimSpace(msg.Content),
		Usage:   convertOpenAIUsage(&oaiResp.Usage),
	}

	// 映射停止原因
	response.StopReason = mapOpenAIStopReason(choice.FinishReason)

	// 转换工具调用
	if len(msg.ToolCalls) > 0 {
		response.ToolCalls = make([]ToolCall, 0, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			response.ToolCalls = append(response.ToolCalls, ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			})
		}
		// 如果停止原因为空且存在工具调用，推断为 tool_calls
		if response.StopReason == "" {
			response.StopReason = StopToolCalls
		}
	}

	// 提取推理内容（DeepSeek / Moonshot 等使用 reasoning_content 字段）
	if msg.ReasoningContent != "" {
		response.Reasoning = msg.ReasoningContent
	}

	// 检测缓存命中
	if oaiResp.Usage.PromptTokensDetails != nil {
		if oaiResp.Usage.PromptTokensDetails.CachedTokens > 0 {
			response.CachedPrompt = true
		}
	}

	return response, nil
}

// ParseStream 解析 OpenAI 流式 Chat Completions 响应体，返回 StreamDelta 通道。
// body 由调用方传入的 HTTP 响应体 ReadCloser，本方法负责在 goroutine 结束时关闭。
func (t *OpenAITransport) ParseStream(ctx context.Context, body io.ReadCloser) <-chan *StreamDelta {
	ch := make(chan *StreamDelta, 256)

	go func() {
		defer close(ch)
		defer body.Close() // 确保响应体被关闭，防止资源泄漏

		var contentBuilder strings.Builder
		var toolCalls []ToolCall
		toolCallBuilders := make(map[int]*toolCallBuilder)
		var finalUsage *TokenUsage // 流式响应的累积 token 用量

		for event := range ParseSSEStream(ctx, body) {
			// 检查 context 取消
			select {
			case <-ctx.Done():
				return
			default:
			}

			if event.Data == "[DONE]" {
				// 流结束，发送最终增量
				ch <- &StreamDelta{
					Content:   contentBuilder.String(),
					ToolCalls: toolCalls,
					Usage:     finalUsage,
					Done:      true,
				}
				return
			}

			var chunk openAIStreamChunk
			if err := json.Unmarshal([]byte(event.Data), &chunk); err != nil {
				slog.Debug("failed to parse SSE data", "data", event.Data[:min(len(event.Data), 200)], "error", err)
				continue
			}

			// 收集流式 usage（最后一个 chunk 可能包含 usage 字段）
			if chunk.Usage != nil {
				finalUsage = convertOpenAIUsage(chunk.Usage)
			}

			if len(chunk.Choices) == 0 {
				continue
			}

			delta := chunk.Choices[0].Delta

			// 文本增量
			if delta.Content != "" {
				contentBuilder.WriteString(delta.Content)
				ch <- &StreamDelta{
					Content: delta.Content,
				}
			}

			// 工具调用增量
			for _, tcDelta := range delta.ToolCalls {
				idx := tcDelta.Index
				if _, ok := toolCallBuilders[idx]; !ok {
					toolCallBuilders[idx] = &toolCallBuilder{
						ID:   tcDelta.ID,
						Name: tcDelta.Function.Name,
					}
				}
				if tcDelta.ID != "" {
					toolCallBuilders[idx].ID = tcDelta.ID
				}
				if tcDelta.Function.Name != "" {
					toolCallBuilders[idx].Name = tcDelta.Function.Name
				}
				if tcDelta.Function.Arguments != "" {
					toolCallBuilders[idx].Arguments.WriteString(tcDelta.Function.Arguments)
				}
			}

			// 推理增量
			if delta.ReasoningContent != "" {
				ch <- &StreamDelta{
					Reasoning: delta.ReasoningContent,
				}
			}

			// 停止原因
			finishReason := chunk.Choices[0].FinishReason
			if finishReason != "" {
				// 完成所有工具调用
				toolCalls = make([]ToolCall, 0, len(toolCallBuilders))
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
					Done:      true,
				}
				return
			}
		}
	}()

	return ch
}

// ── OpenAI Provider 实现 ───────────────────────────────────────────────────

// OpenAIProvider 实现 OpenAI Chat Completions 协议的 Provider 接口。
type OpenAIProvider struct {
	transport *OpenAITransport
	apiKey    string
	model     string
}

// NewOpenAIProvider 创建一个新的 OpenAI 提供者。
// baseURL 可自定义（如 OpenRouter、DeepSeek、Qwen 等兼容端点）。
func NewOpenAIProvider(httpClient *http.Client, apiKey, model, baseURL string) *OpenAIProvider {
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	return &OpenAIProvider{
		transport: NewOpenAITransport(httpClient, baseURL),
		apiKey:    apiKey,
		model:     model,
	}
}

// Name 返回提供者标识。
func (p *OpenAIProvider) Name() string {
	return "openai"
}

// CreateChatCompletion 发送非流式聊天补全请求。
func (p *OpenAIProvider) CreateChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	// 确保模型已设置
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
		return nil, fmt.Errorf("OpenAI API 错误 (HTTP %d, %s): %s", resp.StatusCode, classified.Reason, bodyStr)
	}

	return p.transport.ParseResponse(body)
}

// CreateChatCompletionStream 发送流式聊天补全请求。
func (p *OpenAIProvider) CreateChatCompletionStream(ctx context.Context, req *ChatRequest) (<-chan *StreamDelta, error) {
	// 确保模型已设置且启用流式
	if req.Model == "" {
		reqCopy := *req
		reqCopy.Model = p.model
		req = &reqCopy
	}

	// 通过 Metadata 传递 stream 参数
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

	// 设置流式 Accept 头
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
		return nil, fmt.Errorf("OpenAI 流式 API 错误 (HTTP %d, %s): %s", resp.StatusCode, classified.Reason, bodyStr)
	}

	// 直接将 HTTP 响应体传递给 ParseStream 进行真正的流式解析。
	// 不再使用 io.ReadAll 将整个响应读入内存，避免大响应导致 OOM。
	// resp.Body 的生命周期由 ParseStream 内部的 goroutine 通过 defer body.Close() 管理。
	return p.transport.ParseStream(ctx, resp.Body), nil
}

// ListModels 返回可用的模型列表。
func (p *OpenAIProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	url := buildAPIURL(p.transport.baseURL, "/models")
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("创建模型列表请求失败: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Accept", "application/json")

	resp, err := p.transport.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("获取模型列表失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取模型列表响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("获取模型列表返回 HTTP %d: %s", resp.StatusCode, string(body))
	}

	var listResp openAIModelListResponse
	if err := json.Unmarshal(body, &listResp); err != nil {
		return nil, fmt.Errorf("解析模型列表失败: %w", err)
	}

	models := make([]ModelInfo, 0, len(listResp.Data))
	for _, m := range listResp.Data {
		models = append(models, ModelInfo{
			ID:       m.ID,
			Provider: p.Name(),
		})
	}

	return models, nil
}

// ── OpenAI API 类型 ────────────────────────────────────────────────────────

// openAIChatResponse OpenAI Chat Completions 响应结构。
type openAIChatResponse struct {
	ID      string           `json:"id"`
	Object  string           `json:"object"`
	Created int64            `json:"created"`
	Model   string           `json:"model"`
	Choices []openAIChoice   `json:"choices"`
	Usage   openAIUsage      `json:"usage"`
}

// openAIChoice 单个响应选项。
type openAIChoice struct {
	Index        int            `json:"index"`
	Message      openAIMessage  `json:"message"`
	FinishReason string         `json:"finish_reason"`
}

// openAIMessage OpenAI 消息格式。
type openAIMessage struct {
	Role             string          `json:"role"`
	Content          string          `json:"content"`
	ToolCalls        []openAIToolCall `json:"tool_calls,omitempty"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
}

// openAIToolCall OpenAI 工具调用格式。
type openAIToolCall struct {
	ID       string                 `json:"id"`
	Type     string                 `json:"type"`
	Function openAIFunctionCall     `json:"function"`
}

// openAIFunctionCall OpenAI 函数调用格式。
type openAIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// openAIUsage OpenAI token 用量。
type openAIUsage struct {
	PromptTokens        int                   `json:"prompt_tokens"`
	CompletionTokens    int                   `json:"completion_tokens"`
	TotalTokens         int                   `json:"total_tokens"`
	PromptTokensDetails *openAIPromptTokenDetails `json:"prompt_tokens_details,omitempty"`
}

// openAIPromptTokenDetails OpenAI 提示 token 详情（含缓存信息）。
type openAIPromptTokenDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
}

// openAIStreamChunk OpenAI 流式响应块。
type openAIStreamChunk struct {
	ID      string               `json:"id"`
	Object  string               `json:"object"`
	Created int64                `json:"created"`
	Model   string               `json:"model"`
	Choices []openAIStreamChoice `json:"choices"`
	Usage   *openAIUsage         `json:"usage,omitempty"` // 流式最后一个 chunk 可能包含 usage
}

// openAIStreamChoice 流式响应选项。
type openAIStreamChoice struct {
	Index        int              `json:"index"`
	Delta        openAIStreamDelta `json:"delta"`
	FinishReason string           `json:"finish_reason"`
}

// openAIStreamDelta 流式增量。
type openAIStreamDelta struct {
	Role             string               `json:"role,omitempty"`
	Content          string               `json:"content,omitempty"`
	ToolCalls        []openAIToolCallDelta `json:"tool_calls,omitempty"`
	ReasoningContent string               `json:"reasoning_content,omitempty"`
}

// openAIToolCallDelta 流式工具调用增量。
type openAIToolCallDelta struct {
	Index    int                  `json:"index"`
	ID       string               `json:"id,omitempty"`
	Type     string               `json:"type,omitempty"`
	Function openAIFunctionCallDelta `json:"function,omitempty"`
}

// openAIFunctionCallDelta 流式函数调用增量。
type openAIFunctionCallDelta struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// openAIModelListResponse OpenAI 模型列表响应。
type openAIModelListResponse struct {
	Object string           `json:"object"`
	Data   []openAIModelEntry `json:"data"`
}

// openAIModelEntry 模型条目。
type openAIModelEntry struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// toolCallBuilder 用于在流式模式下构建工具调用。
type toolCallBuilder struct {
	ID        string
	Name      string
	Arguments strings.Builder
}

// ── OpenAI 请求体构建 ──────────────────────────────────────────────────────

// openAIRequestBody OpenAI Chat Completions 请求体。
type openAIRequestBody struct {
	Model        string          `json:"model"`
	Messages     []openAIRequestMessage `json:"messages"`
	Tools        []openAIToolDef `json:"tools,omitempty"`
	MaxTokens    int             `json:"max_tokens,omitempty"`
	Temperature  *float64        `json:"temperature,omitempty"`
	Stream       bool            `json:"stream,omitempty"`
	StreamOptions *openAIStreamOptions `json:"stream_options,omitempty"`
}

// openAIStreamOptions 流式响应选项，用于请求 API 在流中返回 usage 信息。
type openAIStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// openAIRequestMessage OpenAI 请求消息。
type openAIRequestMessage struct {
	Role             string               `json:"role"`
	Content          string               `json:"content"`
	ToolCalls        []openAIToolCall     `json:"tool_calls,omitempty"`
	ToolCallID       string               `json:"tool_call_id,omitempty"`
	Name             string               `json:"name,omitempty"`
	ReasoningContent string               `json:"reasoning_content,omitempty"`
}

// openAIToolDef OpenAI 工具定义。
type openAIToolDef struct {
	Type     string              `json:"type"`
	Function openAIFunctionDef   `json:"function"`
}

// openAIFunctionDef OpenAI 函数定义。
type openAIFunctionDef struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

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
				Type: "function",
				Function: openAIFunctionDef{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  t.Parameters,
				},
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
		return StopToolCalls
	case "content_filter":
		return StopContentFilter
	case "function_call":
		return StopToolCalls
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

// ── init 注册 ─────────────────────────────────────────────────────────────

func init() {
	// 注册到全局 Transport 注册表
	RegisterTransport("chat_completions", &OpenAITransport{baseURL: "https://api.openai.com"})
	slog.Debug("OpenAI transport registered", "apiMode", "chat_completions", "time", time.Now())
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
	if strings.Contains(base, "/v1") || strings.Contains(base, "/compatible-mode") {
		return base + "/" + strings.TrimLeft(endpoint, "/")
	}
	return base + "/v1/" + strings.TrimLeft(endpoint, "/")
}
