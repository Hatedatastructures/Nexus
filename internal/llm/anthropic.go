// Package llm 提供 LLM 提供者抽象层。
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	pkgerrors "nexus-agent/internal/errors"
)// ── 常量 ───────────────────────────────────────────────────────────────────

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
func (t *AnthropicTransport) BuildRequest(ctx context.Context, req *ChatRequest, apiKey string) (*http.Request, error) {
	body := buildAnthropicRequestBody(req)

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ProviderAPI, "序列化 Anthropic 请求体失败", err)
	}

	url := t.baseURL + "/v1/messages"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ProviderAPI, "创建 HTTP 请求失败", err)
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
		return nil, pkgerrors.Wrap(pkgerrors.ProviderAPI, "解析 Anthropic 响应失败", err)
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

	resp, err := p.transport.httpClient.Do(httpReq)
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.NetworkHTTP, "HTTP 请求失败", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLLMResponseSize))
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.NetworkHTTP, "读取响应体失败", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyStr := string(body)
		classified := ClassifyError(resp.StatusCode, bodyStr)
		return nil, pkgerrors.New(pkgerrors.ProviderAPI, fmt.Sprintf("Anthropic API 错误 (HTTP %d, %s): %s", resp.StatusCode, classified.Reason, RedactErrorBody(bodyStr)))
	}

	return p.transport.ParseResponse(body)
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
