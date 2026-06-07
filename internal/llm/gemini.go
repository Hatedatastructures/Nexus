// Package llm 提供 LLM 提供者抽象层。
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	pkgerrors "nexus-agent/internal/errors"
	"strings"
)

// ── 常量 ───────────────────────────────────────────────────────────────────

const (
	// DefaultGeminiBaseURL 为 Google Gemini API 的默认基础 URL。
	DefaultGeminiBaseURL = "https://generativelanguage.googleapis.com/v1beta"
)

// ── Gemini 传输层 ─────────────────────────────────────────────────────────

// GeminiTransport 实现 Google Gemini API 的请求构建和响应解析。
// 使用原生 generateContent 端点。
type GeminiTransport struct {
	httpClient *http.Client
	baseURL    string
}

// NewGeminiTransport 创建新的 Gemini 传输层。
func NewGeminiTransport(httpClient *http.Client, baseURL string) *GeminiTransport {
	if baseURL == "" {
		baseURL = DefaultGeminiBaseURL
	}
	return &GeminiTransport{
		httpClient: httpClient,
		baseURL:    strings.TrimRight(baseURL, "/"),
	}
}

// APIMode 返回 API 模式标识。
func (t *GeminiTransport) APIMode() string {
	return "gemini_api"
}

// BuildRequest 构建 Gemini generateContent HTTP 请求。
// API key 通过查询参数 ?key= 传递。
func (t *GeminiTransport) BuildRequest(ctx context.Context, req *ChatRequest, apiKey string) (*http.Request, error) {
	body := buildGeminiRequestBody(req)

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ProviderAPI, "序列化 Gemini 请求体失败", err)
	}

	// Gemini URL: {baseURL}/models/{model}:generateContent
	// API key 通过 x-goog-api-key header 传递，避免密钥暴露在 URL 中
	url := fmt.Sprintf("%s/models/%s:generateContent",
		t.baseURL, url.PathEscape(req.Model))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ProviderAPI, "创建 HTTP 请求失败", err)
	}

	// 设置请求头
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	// 使用 header 传递 API key，避免密钥暴露在 URL 中
	if apiKey != "" {
		httpReq.Header.Set("x-goog-api-key", apiKey)
	}

	// 支持 GetBody 以便重试
	httpReq.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(bodyBytes)), nil
	}

	return httpReq, nil
}

// ParseResponse 解析 Gemini generateContent 响应体为统一的 ChatResponse。
func (t *GeminiTransport) ParseResponse(body []byte) (*ChatResponse, error) {
	var geminiResp geminiGenerateResponse
	if err := json.Unmarshal(body, &geminiResp); err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ProviderAPI, "解析 Gemini 响应失败", err)
	}

	if len(geminiResp.Candidates) == 0 {
		return nil, pkgerrors.New(pkgerrors.ProviderAPI, "Gemini 响应中没有 candidates")
	}

	candidate := geminiResp.Candidates[0]
	content := candidate.Content
	if content == nil {
		return nil, pkgerrors.New(pkgerrors.ProviderAPI, "Gemini 响应 candidates[0].Content 为 nil (可能被安全过滤器拦截)")
	}

	response := &ChatResponse{
		Model: geminiResp.ModelVersion,
	}

	// 解析 parts
	var textParts []string
	var reasoningParts []string
	var toolCalls []ToolCall

	for _, part := range content.Parts {
		// 文本内容
		if part.Text != "" && !part.Thought {
			textParts = append(textParts, part.Text)
		}

		// 思维内容（thought: true 的文本）
		if part.Text != "" && part.Thought {
			reasoningParts = append(reasoningParts, part.Text)
		}

		// 函数调用
		if part.FunctionCall != nil && part.FunctionCall.Name != "" {
			argsJSON, _ := json.Marshal(part.FunctionCall.Args)
			toolCall := ToolCall{
				ID:        fmt.Sprintf("call_%s", generateShortID()),
				Name:      part.FunctionCall.Name,
				Arguments: string(argsJSON),
			}
			// 保留 thought_signature 到 Extra 字段
			if part.ThoughtSignature != "" {
				toolCall.Extra = map[string]any{
					"thought_signature": part.ThoughtSignature,
				}
			}
			toolCalls = append(toolCalls, toolCall)
		}
	}

	response.Content = strings.Join(textParts, "")
	response.ToolCalls = toolCalls
	response.Reasoning = strings.Join(reasoningParts, "")

	// 映射停止原因
	response.StopReason = mapGeminiStopReason(candidate.FinishReason, len(toolCalls) > 0)

	// token 用量
	response.Usage = convertGeminiUsage(&geminiResp.UsageMetadata)

	return response, nil
}

// ── Gemini Provider 实现 ──────────────────────────────────────────────────

// GeminiProvider 实现 Google Gemini API 的 Provider 接口。
type GeminiProvider struct {
	transport *GeminiTransport
	apiKey    string
	model     string
}

// NewGeminiProvider 创建一个新的 Gemini 提供者。
func NewGeminiProvider(httpClient *http.Client, apiKey, model, baseURL string) *GeminiProvider {
	if baseURL == "" {
		baseURL = DefaultGeminiBaseURL
	}
	return &GeminiProvider{
		transport: NewGeminiTransport(httpClient, baseURL),
		apiKey:    apiKey,
		model:     model,
	}
}

// Name 返回提供者标识。
func (p *GeminiProvider) Name() string {
	return "gemini"
}

// CreateChatCompletion 发送非流式聊天补全请求。
func (p *GeminiProvider) CreateChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
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
		return nil, pkgerrors.New(pkgerrors.ProviderAPI, fmt.Sprintf("Gemini API 错误 (HTTP %d, %s): %s", resp.StatusCode, classified.Reason, RedactErrorBody(bodyStr)))
	}

	return p.transport.ParseResponse(body)
}

// ListModels 通过 Gemini API 获取可用模型列表。
// 调用 GET {baseURL}/models 获取真实模型信息。
// API key 通过 x-goog-api-key header 传递。
func (p *GeminiProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	url := p.transport.baseURL + "/models"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ProviderAPI, "创建模型列表请求失败", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	if p.apiKey != "" {
		httpReq.Header.Set("x-goog-api-key", p.apiKey)
	}

	resp, err := p.transport.httpClient.Do(httpReq)
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.NetworkHTTP, "获取 Gemini 模型列表失败", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLLMResponseSize))
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.NetworkHTTP, "读取模型列表响应失败", err)
	}

	if resp.StatusCode != http.StatusOK {
		bodyStr := string(body)
		classified := ClassifyError(resp.StatusCode, bodyStr)
		return nil, pkgerrors.New(pkgerrors.ProviderAPI, fmt.Sprintf("Gemini 模型列表 API 错误 (HTTP %d, %s): %s", resp.StatusCode, classified.Reason, RedactErrorBody(bodyStr)))
	}

	var listResp geminiModelListResponse
	if err := json.Unmarshal(body, &listResp); err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ProviderAPI, "解析 Gemini 模型列表失败", err)
	}

	models := make([]ModelInfo, 0, len(listResp.Models))
	for _, m := range listResp.Models {
		// 过滤掉已弃用的模型和嵌入模型
		if strings.Contains(m.Name, "embedding") || strings.Contains(m.Name, "text-embedding") {
			continue
		}
		// 提取模型 ID（去除 "models/" 前缀）
		modelID := strings.TrimPrefix(m.Name, "models/")
		info := ModelInfo{
			ID:       modelID,
			Provider: p.Name(),
		}
		// 从显示名称和描述中推断能力
		lowerDisplay := strings.ToLower(m.DisplayName)
		lowerDesc := strings.ToLower(m.Description)
		if strings.Contains(lowerDisplay, "vision") || strings.Contains(lowerDesc, "image") {
			info.Vision = true
		}
		if strings.Contains(lowerDisplay, "thinking") || strings.Contains(lowerDisplay, "flash") ||
			strings.Contains(lowerDisplay, "pro") {
			info.Reasoning = true
		}
		// 从版本信息中获取上下文限制
		info.ContextLimit = m.InputTokenLimit
		info.MaxOutput = m.OutputTokenLimit
		if m.Deprecated {
			info.Deprecated = true
		}
		models = append(models, info)
	}

	return models, nil
}

