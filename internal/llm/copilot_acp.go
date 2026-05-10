// Package llm 提供 GitHub Copilot ACP (API Completions Protocol) 提供者。
// Copilot 使用 OpenAI Chat Completions 兼容的 HTTP REST API。
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
	"sync"
	"time"
)

// ───────────────────────────── 常量 ─────────────────────────────

const (
	// copilotDefaultEndpoint Copilot API 默认端点。
	copilotDefaultEndpoint = "https://api.githubcopilot.com"

	// copilotDefaultModel Copilot 默认模型。
	copilotDefaultModel = "gpt-4o"

	// copilotAPIMode Copilot 使用的 API 模式。
	copilotAPIMode = "chat_completions"
)

// ───────────────────────────── CopilotProvider 结构 ─────────────────────────────

// CopilotProvider 实现 GitHub Copilot ACP 提供者。
//
// Copilot API 兼容 OpenAI Chat Completions 格式，
// 但使用独立的认证头和端点。
//
// 使用流程:
//  1. 调用 NewCopilotProvider 创建提供者。
//  2. 通过 llm.Provider 接口调用 CreateChatCompletion 或 CreateChatCompletionStream。
type CopilotProvider struct {
	transport  *OpenAITransport // 复用 OpenAI 传输层
	httpClient *http.Client
	endpoint   string // Copilot API 端点
	token      string // Copilot 访问令牌
	model      string // 默认模型
	mu         sync.Mutex
}

// NewCopilotProvider 创建 GitHub Copilot ACP 提供者。
//
// token 为 Copilot 访问令牌，通常通过 GitHub OAuth 获取。
// 使用默认端点 https://api.githubcopilot.com 和默认模型 gpt-4o。
func NewCopilotProvider(token string) *CopilotProvider {
	return NewCopilotProviderWithOptions(token, "", "", nil)
}

// NewCopilotProviderWithOptions 使用自定义选项创建 Copilot 提供者。
//
// endpoint 为空时使用默认端点；model 为空时使用默认模型；
// httpClient 为 nil 时使用默认 HTTP 客户端。
func NewCopilotProviderWithOptions(token, endpoint, model string, httpClient *http.Client) *CopilotProvider {
	if endpoint == "" {
		endpoint = copilotDefaultEndpoint
	}
	if model == "" {
		model = copilotDefaultModel
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 300 * time.Second}
	}

	return &CopilotProvider{
		transport:  NewOpenAITransport(httpClient, endpoint),
		httpClient: httpClient,
		endpoint:   strings.TrimRight(endpoint, "/"),
		token:      token,
		model:      model,
	}
}

// ───────────────────────────── Provider 接口实现 ─────────────────────────────

// Name 返回提供者标识。
func (p *CopilotProvider) Name() string {
	return "copilot"
}

// CreateChatCompletion 发送非流式聊天补全请求到 Copilot API。
func (p *CopilotProvider) CreateChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	// 确保模型已设置
	if req.Model == "" {
		req.Model = p.model
	}

	httpReq, err := p.buildCopilotRequest(ctx, req, false)
	if err != nil {
		return nil, err
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("Copilot HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 Copilot 响应体失败: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyStr := string(body)
		classified := ClassifyError(resp.StatusCode, bodyStr)
		return nil, fmt.Errorf("Copilot API 错误 (HTTP %d, %s): %s", resp.StatusCode, classified.Reason, bodyStr)
	}

	return p.transport.ParseResponse(body)
}

// CreateChatCompletionStream 发送流式聊天补全请求到 Copilot API。
// 返回 StreamDelta 通道，调用者通过 range 遍历。
func (p *CopilotProvider) CreateChatCompletionStream(ctx context.Context, req *ChatRequest) (<-chan *StreamDelta, error) {
	// 确保模型已设置
	if req.Model == "" {
		req.Model = p.model
	}

	httpReq, err := p.buildCopilotRequest(ctx, req, true)
	if err != nil {
		return nil, err
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("Copilot 流式 HTTP 请求失败: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		bodyStr := string(body)
		classified := ClassifyError(resp.StatusCode, bodyStr)
		return nil, fmt.Errorf("Copilot 流式 API 错误 (HTTP %d, %s): %s", resp.StatusCode, classified.Reason, bodyStr)
	}

	// 读取响应体并通过 OpenAI 传输层解析 SSE 流
	bodyBytes, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("读取 Copilot 流式响应体失败: %w", err)
	}

	return p.transport.ParseStream(ctx, bodyBytes), nil
}

// ListModels 返回 Copilot 可用模型列表。
// Copilot API 的 /models 端点可能不可用，此处返回常用模型作为后备。
func (p *CopilotProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	// 尝试从 API 获取模型列表
	url := p.endpoint + "/v1/models"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("创建 Copilot 模型列表请求失败: %w", err)
	}

	p.setAuthHeaders(httpReq)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		// 请求失败时返回已知模型列表
		slog.Debug("获取 Copilot 模型列表失败，返回已知模型", "error", err)
		return p.defaultModels(), nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return p.defaultModels(), nil
	}

	if resp.StatusCode != http.StatusOK {
		slog.Debug("Copilot 模型列表返回非 200，返回已知模型", "status", resp.StatusCode)
		return p.defaultModels(), nil
	}

	var listResp openAIModelListResponse
	if err := json.Unmarshal(body, &listResp); err != nil {
		return p.defaultModels(), nil
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

// ───────────────────────────── 内部辅助 ─────────────────────────────

// buildCopilotRequest 构建 Copilot API 的 HTTP 请求。
// 复用 OpenAI 请求体格式，设置 Copilot 特有的认证头。
func (p *CopilotProvider) buildCopilotRequest(ctx context.Context, req *ChatRequest, stream bool) (*http.Request, error) {
	// 复用 OpenAI 请求体构建
	if stream {
		if req.Metadata == nil {
			req.Metadata = make(map[string]any)
		}
		req.Metadata["stream"] = true
	}

	body := buildOpenAIRequestBody(req)

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化 Copilot 请求体失败: %w", err)
	}

	url := p.endpoint + "/v1/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("创建 Copilot HTTP 请求失败: %w", err)
	}

	// 设置请求头
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	if stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}

	// 设置 Copilot 认证头
	p.setAuthHeaders(httpReq)

	// 支持 GetBody 以便重试
	httpReq.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(bodyBytes)), nil
	}

	return httpReq, nil
}

// setAuthHeaders 设置 Copilot API 的认证和元数据头。
// Copilot 使用 Bearer token 认证，并需要额外的 Editor-Version 等头信息。
func (p *CopilotProvider) setAuthHeaders(req *http.Request) {
	if p.token != "" {
		req.Header.Set("Authorization", "Bearer "+p.token)
	}
	// Copilot 要求的编辑器版本头（标识客户端来源）
	req.Header.Set("Editor-Version", "Nexus/1.0")
	req.Header.Set("Copilot-Integration-Id", "vscode-chat")
}

// defaultModels 返回 Copilot 已知的默认模型列表。
func (p *CopilotProvider) defaultModels() []ModelInfo {
	return []ModelInfo{
		{ID: "gpt-4o", Provider: p.Name(), ContextLimit: 128000, MaxOutput: 16384, Vision: true},
		{ID: "gpt-4o-mini", Provider: p.Name(), ContextLimit: 128000, MaxOutput: 16384, Vision: true},
		{ID: "o1-mini", Provider: p.Name(), ContextLimit: 128000, MaxOutput: 65536, Reasoning: true},
		{ID: "claude-3.5-sonnet", Provider: p.Name(), ContextLimit: 200000, MaxOutput: 8192, Vision: true},
		{ID: "claude-3.5-haiku", Provider: p.Name(), ContextLimit: 200000, MaxOutput: 8192, Vision: true},
	}
}

// ───────────────────────────── init 注册 ─────────────────────────────

func init() {
	slog.Debug("Copilot ACP 提供者已加载", "time", time.Now())
}
