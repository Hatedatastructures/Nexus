// Package llm 提供 LLM 提供者抽象层。
// openai.go 实现 OpenAI Chat Completions API 的传输层和 Provider。
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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
func (t *OpenAITransport) BuildRequest(ctx context.Context, req *ChatRequest, apiKey string) (*http.Request, error) {
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

	// 转换工具调用（必须在映射停止原因之前，以便推断逻辑生效）
	if len(msg.ToolCalls) > 0 {
		response.ToolCalls = make([]ToolCall, 0, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			response.ToolCalls = append(response.ToolCalls, ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			})
		}
	}

	// 映射停止原因
	response.StopReason = mapOpenAIStopReason(choice.FinishReason)
	// 如果停止原因为默认值且存在工具调用，修正为 tool_use
	if len(msg.ToolCalls) > 0 && response.StopReason == StopEndTurn && choice.FinishReason == "" {
		response.StopReason = StopToolUse
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

	resp, err := p.transport.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLLMResponseSize))
	if err != nil {
		return nil, fmt.Errorf("读取响应体失败: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyStr := string(body)
		classified := ClassifyError(resp.StatusCode, bodyStr)
		return nil, fmt.Errorf("OpenAI API 错误 (HTTP %d, %s): %s", resp.StatusCode, classified.Reason, RedactErrorBody(bodyStr))
	}

	return p.transport.ParseResponse(body)
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
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLLMResponseSize))
	if err != nil {
		return nil, fmt.Errorf("读取模型列表响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		bodyStr := string(body)
		return nil, fmt.Errorf("获取模型列表返回 HTTP %d: %s", resp.StatusCode, RedactErrorBody(bodyStr))
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

