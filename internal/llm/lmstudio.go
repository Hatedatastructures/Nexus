// Package llm 提供 LM Studio 本地推理传输层。
// LM Studio 运行本地模型服务器，暴露 OpenAI 兼容 API，
// 并扩展了 reasoning_content 字段用于推理模型的思维链输出。
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

// ── 常量 ─────────────────────────────────────────────────────────────────

// lmStudioDefaultBaseURL 是 LM Studio 服务器的默认地址。
const lmStudioDefaultBaseURL = "http://localhost:1234/v1"

// lmStudioTransportID 是 LM Studio 传输层的注册标识。
const lmStudioTransportID = "lmstudio"

// ── LM Studio 传输层 ─────────────────────────────────────────────────────

// LMStudioTransport 实现 LM Studio OpenAI 兼容传输层。
// 基于 OpenAI Chat Completions 协议，额外检测 LM Studio 特有的
// reasoning_content 字段以支持推理模型的思维链输出。
type LMStudioTransport struct {
	httpClient *http.Client
	baseURL    string // 默认 http://localhost:1234/v1
}

// NewLMStudioTransport 创建 LM Studio 传输层。
// baseURL 为空时使用默认地址 http://localhost:1234/v1。
func NewLMStudioTransport(httpClient *http.Client, baseURL string) *LMStudioTransport {
	if baseURL == "" {
		baseURL = lmStudioDefaultBaseURL
	}
	return &LMStudioTransport{
		httpClient: httpClient,
		baseURL:    strings.TrimRight(baseURL, "/"),
	}
}

// APIMode 返回 API 模式标识。
func (t *LMStudioTransport) APIMode() string {
	return lmStudioTransportID
}

// BuildRequest 构建 LM Studio Chat Completions HTTP 请求。
// 使用与 OpenAI 兼容的请求格式，LM Studio 本地服务通常不需要 API Key。
func (t *LMStudioTransport) BuildRequest(ctx context.Context, req *ChatRequest, apiKey string) (any, error) {
	body := buildOpenAIRequestBody(req)

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化 LM Studio 请求体失败: %w", err)
	}

	url := t.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("创建 LM Studio HTTP 请求失败: %w", err)
	}

	// 设置请求头
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	// LM Studio 本地服务通常不需要认证，但保留 Bearer 头以兼容需要的场景
	if apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}

	// 支持 GetBody 以便重试
	httpReq.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(bodyBytes)), nil
	}

	return httpReq, nil
}

// ParseResponse 解析 LM Studio Chat Completions 响应体为统一的 ChatResponse。
// 额外检测 reasoning_content 字段（LM Studio 推理模型特有）。
func (t *LMStudioTransport) ParseResponse(body []byte) (*ChatResponse, error) {
	var oaiResp openAIChatResponse
	if err := json.Unmarshal(body, &oaiResp); err != nil {
		return nil, fmt.Errorf("解析 LM Studio 响应失败: %w", err)
	}

	if len(oaiResp.Choices) == 0 {
		return nil, fmt.Errorf("LM Studio 响应中没有 choices")
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

	// 检测推理内容（LM Studio 推理模型使用 reasoning_content 字段）
	if msg.ReasoningContent != "" {
		response.Reasoning = msg.ReasoningContent
		slog.Debug("LM Studio reasoning content extracted",
			"model", oaiResp.Model,
			"reasoningLength", len(msg.ReasoningContent),
		)
	}

	// 检测缓存命中
	if oaiResp.Usage.PromptTokensDetails != nil {
		if oaiResp.Usage.PromptTokensDetails.CachedTokens > 0 {
			response.CachedPrompt = true
		}
	}

	return response, nil
}

// ParseStream 解析 LM Studio 流式 Chat Completions 响应体。
// 复用 OpenAI SSE 流解析逻辑，额外处理 reasoning_content 增量。
func (t *LMStudioTransport) ParseStream(ctx context.Context, body io.ReadCloser) <-chan *StreamDelta {
	ch := make(chan *StreamDelta, 256)

	go func() {
		defer close(ch)
		defer body.Close()

		var contentBuilder strings.Builder
		var reasoningBuilder strings.Builder
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
					Reasoning: reasoningBuilder.String(),
					ToolCalls: toolCalls,
					Usage:     finalUsage,
					Done:      true,
				}
				return
			}

			var chunk openAIStreamChunk
			if err := json.Unmarshal([]byte(event.Data), &chunk); err != nil {
				slog.Debug("failed to parse LM Studio SSE data",
					"data", event.Data[:min(len(event.Data), 200)],
					"error", err,
				)
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

			// 推理增量（LM Studio 推理模型特有）
			if delta.ReasoningContent != "" {
				reasoningBuilder.WriteString(delta.ReasoningContent)
				ch <- &StreamDelta{
					Reasoning: delta.ReasoningContent,
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
					Reasoning: reasoningBuilder.String(),
					ToolCalls: toolCalls,
					Done:      true,
				}
				return
			}
		}
	}()

	return ch
}

// ── LM Studio Provider 实现 ───────────────────────────────────────────────

// LMStudioProvider 实现 LM Studio 本地推理的 Provider 接口。
// 自动检测推理模型的 reasoning_content 输出。
type LMStudioProvider struct {
	transport *LMStudioTransport
	apiKey    string // LM Studio 通常不需要 API Key
	model     string
}

// NewLMStudioProvider 创建一个新的 LM Studio 提供者。
// baseURL 为空时使用默认地址 http://localhost:1234/v1。
func NewLMStudioProvider(httpClient *http.Client, apiKey, model, baseURL string) *LMStudioProvider {
	return &LMStudioProvider{
		transport: NewLMStudioTransport(httpClient, baseURL),
		apiKey:    apiKey,
		model:     model,
	}
}

// Name 返回提供者标识。
func (p *LMStudioProvider) Name() string {
	return lmStudioTransportID
}

// CreateChatCompletion 发送非流式聊天补全请求。
func (p *LMStudioProvider) CreateChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
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
		return nil, fmt.Errorf("LM Studio HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLLMResponseSize))
	if err != nil {
		return nil, fmt.Errorf("读取 LM Studio 响应体失败: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyStr := string(body)
		classified := ClassifyError(resp.StatusCode, bodyStr)
		return nil, fmt.Errorf("LM Studio API 错误 (HTTP %d, %s): %s", resp.StatusCode, classified.Reason, bodyStr)
	}

	return p.transport.ParseResponse(body)
}

// CreateChatCompletionStream 发送流式聊天补全请求。
func (p *LMStudioProvider) CreateChatCompletionStream(ctx context.Context, req *ChatRequest) (<-chan *StreamDelta, error) {
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
		return nil, fmt.Errorf("LM Studio 流式请求失败: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxLLMResponseSize))
		resp.Body.Close()
		bodyStr := string(body)
		classified := ClassifyError(resp.StatusCode, bodyStr)
		return nil, fmt.Errorf("LM Studio 流式 API 错误 (HTTP %d, %s): %s", resp.StatusCode, classified.Reason, bodyStr)
	}

	// 直接将 HTTP 响应体传递给 ParseStream 进行真正的流式解析。
	// 不再使用 io.ReadAll 将整个响应读入内存，避免大响应导致 OOM。
	// resp.Body 的生命周期由 ParseStream 内部的 goroutine 通过 defer body.Close() 管理。
	return p.transport.ParseStream(ctx, resp.Body), nil
}

// ListModels 返回 LM Studio 已加载的模型列表。
// 通过 GET /v1/models 端点获取。
func (p *LMStudioProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	url := p.transport.baseURL + "/models"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("创建 LM Studio 模型列表请求失败: %w", err)
	}

	httpReq.Header.Set("Accept", "application/json")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.transport.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("获取 LM Studio 模型列表失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLLMResponseSize))
	if err != nil {
		return nil, fmt.Errorf("读取 LM Studio 模型列表响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("获取 LM Studio 模型列表返回 HTTP %d: %s", resp.StatusCode, string(body))
	}

	var listResp openAIModelListResponse
	if err := json.Unmarshal(body, &listResp); err != nil {
		return nil, fmt.Errorf("解析 LM Studio 模型列表失败: %w", err)
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

// ── init 注册 ─────────────────────────────────────────────────────────────

func init() {
	// 注册 LM Studio 传输层到全局注册表
	RegisterTransport(lmStudioTransportID, &LMStudioTransport{baseURL: lmStudioDefaultBaseURL})
	slog.Debug("LM Studio transport registered", "apiMode", lmStudioTransportID, "baseURL", lmStudioDefaultBaseURL, "time", time.Now())
}
