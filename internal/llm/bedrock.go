// Package llm 提供 LLM 提供者抽象层。
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	pkgerrors "nexus-agent/internal/errors"
	"os"
	"strings"
)

// ── 常量 ───────────────────────────────────────────────────────────────────

const (
	// DefaultBedrockRegion 为 AWS Bedrock 默认区域。
	DefaultBedrockRegion = "us-east-1"

	// DefaultBedrockModel 为 Bedrock 默认模型。
	DefaultBedrockModel = "anthropic.claude-sonnet-4-20250514-v1:0"
)

// ── Bedrock 传输层 ─────────────────────────────────────────────────────────

// BedrockTransport 实现 AWS Bedrock Converse API 的请求构建和响应解析。
// 使用 AWS SigV4 签名（简化实现，不依赖 aws-sdk-go-v2）。
type BedrockTransport struct {
	httpClient *http.Client
	region     string
}

// NewBedrockTransport 创建新的 Bedrock 传输层。
func NewBedrockTransport(httpClient *http.Client, region string) *BedrockTransport {
	if region == "" {
		region = DefaultBedrockRegion
	}
	return &BedrockTransport{
		httpClient: httpClient,
		region:     region,
	}
}

// APIMode 返回 API 模式标识。
func (t *BedrockTransport) APIMode() string {
	return "bedrock_converse"
}

// BuildRequest 构建 Bedrock Converse HTTP 请求。
// 使用环境变量中的 AWS credentials 进行 SigV4 签名。
func (t *BedrockTransport) BuildRequest(ctx context.Context, req *ChatRequest, apiKey string) (*http.Request, error) {
	// 确定模型 ID
	modelID := req.Model
	if modelID == "" {
		modelID = DefaultBedrockModel
	}

	// 构建请求体
	body := buildBedrockRequestBody(req)

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ProviderAPI, "序列化 Bedrock 请求体失败", err)
	}

	// 确定是否流式
	stream := false
	if req.Metadata != nil {
		if s, ok := req.Metadata["stream"].(bool); ok {
			stream = s
		}
	}

	// 构建 URL
	var url string
	if stream {
		url = fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com/model/%s/converse-stream",
			t.region, neturl.PathEscape(modelID))
	} else {
		url = fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com/model/%s/converse",
			t.region, neturl.PathEscape(modelID))
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ProviderAPI, "创建 HTTP 请求失败", err)
	}

	// 设置请求头
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	// AWS SigV4 签名
	accessKey := os.Getenv("AWS_ACCESS_KEY_ID")
	secretKey := os.Getenv("AWS_SECRET_ACCESS_KEY")
	sessionToken := os.Getenv("AWS_SESSION_TOKEN")

	// 如果 apiKey 不为空且没有设置环境变量，尝试使用 apiKey 作为 access key
	if accessKey == "" && apiKey != "" {
		accessKey = apiKey
	}

	if accessKey != "" && secretKey != "" {
		if err := signAWSRequest(httpReq, bodyBytes, accessKey, secretKey, sessionToken, t.region); err != nil {
			return nil, pkgerrors.Wrap(pkgerrors.ProviderAuth, "AWS SigV4 签名失败", err)
		}
	} else {
		return nil, pkgerrors.New(pkgerrors.ProviderAuth, "AWS 凭证缺失: 请设置 AWS_ACCESS_KEY_ID 和 AWS_SECRET_ACCESS_KEY 环境变量，或配置 IAM 角色")
	}

	// 支持 GetBody 以便重试
	httpReq.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(bodyBytes)), nil
	}

	return httpReq, nil
}

// ParseResponse 解析 Bedrock Converse 响应体为统一的 ChatResponse。
func (t *BedrockTransport) ParseResponse(body []byte) (*ChatResponse, error) {
	var bedrockResp bedrockConverseResponse
	if err := json.Unmarshal(body, &bedrockResp); err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ProviderAPI, "解析 Bedrock 响应失败", err)
	}

	response := &ChatResponse{
		Model: bedrockResp.Output.Message.Role,
	}

	// 解析 output.message.content
	if bedrockResp.Output.Message.Content != nil {
		var textParts []string
		var toolCalls []ToolCall
		var reasoningParts []string

		for _, block := range bedrockResp.Output.Message.Content {
			switch block.Type {
			case "text":
				if block.Text != "" {
					textParts = append(textParts, block.Text)
				}
			case "tool_use":
				argsJSON, _ := json.Marshal(block.Input)
				toolCalls = append(toolCalls, ToolCall{
					ID:        block.ToolUseID,
					Name:      block.Name,
					Arguments: string(argsJSON),
				})
			}
		}

		response.Content = strings.Join(textParts, "")
		response.ToolCalls = toolCalls
		response.Reasoning = strings.Join(reasoningParts, "")
	}

	// 映射停止原因
	response.StopReason = mapBedrockStopReason(bedrockResp.StopReason, len(response.ToolCalls) > 0)

	// token 用量
	response.Usage = convertBedrockUsage(&bedrockResp.Usage)

	// 缓存命中检测
	if response.Usage != nil && (response.Usage.CacheReadTokens > 0 || response.Usage.CacheWriteTokens > 0) {
		response.CachedPrompt = true
	}

	return response, nil
}

// ── Bedrock Provider 实现 ─────────────────────────────────────────────────

// BedrockProvider 实现 AWS Bedrock Converse API 的 Provider 接口。
type BedrockProvider struct {
	transport *BedrockTransport
	apiKey    string
	model     string
}

// NewBedrockProvider 创建一个新的 Bedrock 提供者。
// apiKey 可选；优先使用环境变量 AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY。
func NewBedrockProvider(httpClient *http.Client, apiKey, model, region string) *BedrockProvider {
	if region == "" {
		region = os.Getenv("AWS_REGION")
		if region == "" {
			region = os.Getenv("AWS_DEFAULT_REGION")
		}
		if region == "" {
			region = DefaultBedrockRegion
		}
	}
	return &BedrockProvider{
		transport: NewBedrockTransport(httpClient, region),
		apiKey:    apiKey,
		model:     model,
	}
}

// Name 返回提供者标识。
func (p *BedrockProvider) Name() string {
	return "bedrock"
}

// CreateChatCompletion 发送非流式聊天补全请求。
func (p *BedrockProvider) CreateChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
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
		return nil, pkgerrors.New(pkgerrors.ProviderAPI, fmt.Sprintf("Bedrock API 错误 (HTTP %d, %s): %s", resp.StatusCode, classified.Reason, RedactErrorBody(bodyStr)))
	}

	return p.transport.ParseResponse(body)
}

// CreateChatCompletionStream 发送流式聊天补全请求。
func (p *BedrockProvider) CreateChatCompletionStream(ctx context.Context, req *ChatRequest) (<-chan *StreamDelta, error) {
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

	httpReq.Header.Set("Accept", "application/vnd.amazon.eventstream")

	resp, err := p.transport.httpClient.Do(httpReq)
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.NetworkHTTP, "HTTP 流式请求失败", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxLLMResponseSize))
		_ = resp.Body.Close()
		bodyStr := string(body)
		classified := ClassifyError(resp.StatusCode, bodyStr)
		return nil, pkgerrors.New(pkgerrors.ProviderAPI, fmt.Sprintf("Bedrock 流式 API 错误 (HTTP %d, %s): %s", resp.StatusCode, classified.Reason, RedactErrorBody(bodyStr)))
	}

	// 直接将 HTTP 响应体传递给 ParseStream 进行真正的流式解析。
	// 不再使用 io.ReadAll 将整个响应读入内存，避免大响应导致 OOM。
	// resp.Body 的生命周期由 ParseStream 内部的 goroutine 通过 defer func() { _ = body.Close() }() 管理。
	return p.transport.ParseStream(ctx, resp.Body), nil
}

// ListModels 返回 Bedrock 基础模型列表。
// Bedrock 没有简单的 list models 端点，返回已知的 Bedrock 基础模型。
func (p *BedrockProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	return []ModelInfo{
		{ID: "anthropic.claude-opus-4-6-20250806-v1:0", Provider: p.Name(), ContextLimit: 200000, MaxOutput: 128000, Vision: true, Reasoning: true},
		{ID: "anthropic.claude-sonnet-4-20250514-v1:0", Provider: p.Name(), ContextLimit: 200000, MaxOutput: 64000, Vision: true, Reasoning: true},
		{ID: "anthropic.claude-3-7-sonnet-20250219-v1:0", Provider: p.Name(), ContextLimit: 200000, MaxOutput: 128000, Vision: true, Reasoning: true},
		{ID: "anthropic.claude-3-5-sonnet-20241022-v2:0", Provider: p.Name(), ContextLimit: 200000, MaxOutput: 8192, Vision: true, Reasoning: false},
		{ID: "anthropic.claude-3-5-haiku-20241022-v1:0", Provider: p.Name(), ContextLimit: 200000, MaxOutput: 8192, Vision: true, Reasoning: false},
		{ID: "us.anthropic.claude-3-5-haiku-20241022-v1:0", Provider: p.Name(), ContextLimit: 200000, MaxOutput: 8192, Vision: true, Reasoning: false},
		{ID: "amazon.nova-pro-v1:0", Provider: p.Name(), ContextLimit: 300000, MaxOutput: 5120, Vision: true, Reasoning: false},
		{ID: "amazon.nova-lite-v1:0", Provider: p.Name(), ContextLimit: 300000, MaxOutput: 5120, Vision: true, Reasoning: false},
		{ID: "amazon.nova-micro-v1:0", Provider: p.Name(), ContextLimit: 128000, MaxOutput: 5120, Vision: false, Reasoning: false},
		{ID: "meta.llama3-1-405b-instruct-v1:0", Provider: p.Name(), ContextLimit: 128000, MaxOutput: 4096, Vision: false, Reasoning: false},
		{ID: "meta.llama3-1-70b-instruct-v1:0", Provider: p.Name(), ContextLimit: 128000, MaxOutput: 2048, Vision: false, Reasoning: false},
		{ID: "meta.llama3-3-70b-instruct-v1:0", Provider: p.Name(), ContextLimit: 128000, MaxOutput: 2048, Vision: false, Reasoning: false},
		{ID: "mistral.mistral-large-2407-v1:0", Provider: p.Name(), ContextLimit: 128000, MaxOutput: 4096, Vision: false, Reasoning: false},
		{ID: "cohere.command-r-plus-v1:0", Provider: p.Name(), ContextLimit: 128000, MaxOutput: 4096, Vision: false, Reasoning: false},
	}, nil
}
