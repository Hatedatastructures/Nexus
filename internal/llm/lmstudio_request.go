// Package llm 提供 LM Studio 本地推理传输层。
// lmstudio_request.go 实现 LM Studio Provider 的聊天补全和模型列表操作。
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	pkgerrors "nexus-agent/internal/errors"
)

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

	resp, err := p.transport.httpClient.Do(httpReq)
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.NetworkHTTP, "LM Studio HTTP 请求失败", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLLMResponseSize))
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.NetworkHTTP, "读取 LM Studio 响应体失败", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyStr := string(body)
		classified := ClassifyError(resp.StatusCode, bodyStr)
		return nil, pkgerrors.New(pkgerrors.ProviderAPI, fmt.Sprintf("LM Studio API 错误 (HTTP %d, %s): %s", resp.StatusCode, classified.Reason, RedactErrorBody(bodyStr)))
	}

	return p.transport.ParseResponse(body)
}

// CreateChatCompletionStream 发送流式聊天补全请求。
func (p *LMStudioProvider) CreateChatCompletionStream(ctx context.Context, req *ChatRequest) (<-chan *StreamDelta, error) {
	// 深拷贝请求，避免修改调用方的原始数据
	reqCopy := *req
	if reqCopy.Model == "" {
		reqCopy.Model = p.model
	}
	if reqCopy.Metadata == nil {
		reqCopy.Metadata = make(map[string]any)
	} else {
		md := make(map[string]any, len(reqCopy.Metadata)+1)
		for k, v := range reqCopy.Metadata {
			md[k] = v
		}
		reqCopy.Metadata = md
	}
	reqCopy.Metadata["stream"] = true
	req = &reqCopy

	httpReq, err := p.transport.BuildRequest(ctx, req, p.apiKey)
	if err != nil {
		return nil, err
	}

	// 设置流式 Accept 头
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := p.transport.httpClient.Do(httpReq)
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.NetworkHTTP, "LM Studio 流式请求失败", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxLLMResponseSize))
		_ = resp.Body.Close()
		bodyStr := string(body)
		classified := ClassifyError(resp.StatusCode, bodyStr)
		return nil, pkgerrors.New(pkgerrors.ProviderAPI, fmt.Sprintf("LM Studio 流式 API 错误 (HTTP %d, %s): %s", resp.StatusCode, classified.Reason, RedactErrorBody(bodyStr)))
	}

	// 直接将 HTTP 响应体传递给 ParseStream 进行真正的流式解析。
	// 不再使用 io.ReadAll 将整个响应读入内存，避免大响应导致 OOM。
	// resp.Body 的生命周期由 ParseStream 内部的 goroutine 通过 defer func() { _ = body.Close() }() 管理。
	return p.transport.ParseStream(ctx, resp.Body), nil
}

// ListModels 返回 LM Studio 已加载的模型列表。
// 通过 GET /v1/models 端点获取。
func (p *LMStudioProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	url := p.transport.baseURL + "/models"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ProviderAPI, "创建 LM Studio 模型列表请求失败", err)
	}

	httpReq.Header.Set("Accept", "application/json")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.transport.httpClient.Do(httpReq)
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.NetworkHTTP, "获取 LM Studio 模型列表失败", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLLMResponseSize))
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.NetworkHTTP, "读取 LM Studio 模型列表响应失败", err)
	}

	if resp.StatusCode != http.StatusOK {
		bodyStr := string(body)
		return nil, pkgerrors.New(pkgerrors.ProviderAPI, fmt.Sprintf("获取 LM Studio 模型列表返回 HTTP %d: %s", resp.StatusCode, RedactErrorBody(bodyStr)))
	}

	var listResp openAIModelListResponse
	if err := json.Unmarshal(body, &listResp); err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ProviderAPI, "解析 LM Studio 模型列表失败", err)
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
