// Package llm 提供 LLM 提供者抽象层。
package llm

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	neturl "net/url"
	"os"
	"strings"
	"time"
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
func (t *BedrockTransport) BuildRequest(ctx context.Context, req *ChatRequest, apiKey string) (any, error) {
	// 确定模型 ID
	modelID := req.Model
	if modelID == "" {
		modelID = DefaultBedrockModel
	}

	// 构建请求体
	body := buildBedrockRequestBody(req)

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化 Bedrock 请求体失败: %w", err)
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
		return nil, fmt.Errorf("创建 HTTP 请求失败: %w", err)
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
			slog.Warn("AWS SigV4 签名失败，将发送未签名请求", "error", err)
		}
	} else {
		slog.Debug("未找到 AWS credentials，将发送未签名请求（假设使用代理或 IAM role）")
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
		return nil, fmt.Errorf("解析 Bedrock 响应失败: %w", err)
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

// ParseStream 解析 Bedrock Converse Stream 响应，返回 StreamDelta 通道。
func (t *BedrockTransport) ParseStream(ctx context.Context, body io.ReadCloser) <-chan *StreamDelta {
	ch := make(chan *StreamDelta, 256)

	go func() {
		defer close(ch)
		defer body.Close()

		var contentBuilder strings.Builder
		var reasoningBuilder strings.Builder
		toolCallBuilders := make(map[int]*toolCallBuilder)
		var finalUsage *TokenUsage // 流式响应的累积 token 用量

		for event := range ParseSSEStream(ctx, body) {
			select {
			case <-ctx.Done():
				return
			default:
			}

			if event.Data == "" || event.Data == "[DONE]" {
				// 流结束
				if event.Data == "[DONE]" {
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
						Usage:     finalUsage,
						Done:      true,
					}
				}
				return
			}

			// Bedrock 流式响应也是 SSE 格式，使用 message_delta 事件
			var streamEvent bedrockStreamEvent
			if err := json.Unmarshal([]byte(event.Data), &streamEvent); err != nil {
				slog.Debug("解析 Bedrock SSE 数据失败", "error", err)
				continue
			}

			switch streamEvent.Type {
			case "content_block_start":
				if streamEvent.ContentBlock != nil && streamEvent.ContentBlock.Type == "tool_use" {
					toolCallBuilders[streamEvent.Index] = &toolCallBuilder{
						ID:   streamEvent.ContentBlock.ToolUseID,
						Name: streamEvent.ContentBlock.Name,
					}
				}

			case "content_block_delta":
				if streamEvent.Delta != nil {
					switch streamEvent.Delta.Type {
					case "text_delta":
						if streamEvent.Delta.Text != "" {
							contentBuilder.WriteString(streamEvent.Delta.Text)
							ch <- &StreamDelta{
								Content: streamEvent.Delta.Text,
							}
						}
					case "input_json_delta":
						if builder, ok := toolCallBuilders[streamEvent.Index]; ok && streamEvent.Delta.PartialJSON != "" {
							builder.Arguments.WriteString(streamEvent.Delta.PartialJSON)
						}
					}
				}

			case "metadata":
				// Bedrock 流式响应的 metadata 事件包含 token 用量
				if streamEvent.Usage != nil {
					finalUsage = convertBedrockUsage(streamEvent.Usage)
				}

			case "message_delta":
				// 最终事件，收集所有工具调用并发送 Done
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
					Done:      true,
				}
				return
			}
		}

		// 如果循环正常结束而没有 Done，发送最终增量
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
			Done:      true,
		}
	}()

	return ch
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
		req.Model = p.model
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
		return nil, fmt.Errorf("Bedrock API 错误 (HTTP %d, %s): %s", resp.StatusCode, classified.Reason, bodyStr)
	}

	return p.transport.ParseResponse(body)
}

// CreateChatCompletionStream 发送流式聊天补全请求。
func (p *BedrockProvider) CreateChatCompletionStream(ctx context.Context, req *ChatRequest) (<-chan *StreamDelta, error) {
	if req.Model == "" {
		req.Model = p.model
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

	httpReqTyped.Header.Set("Accept", "application/vnd.amazon.eventstream")

	resp, err := p.transport.httpClient.Do(httpReqTyped)
	if err != nil {
		return nil, fmt.Errorf("HTTP 流式请求失败: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		bodyStr := string(body)
		classified := ClassifyError(resp.StatusCode, bodyStr)
		return nil, fmt.Errorf("Bedrock 流式 API 错误 (HTTP %d, %s): %s", resp.StatusCode, classified.Reason, bodyStr)
	}

	// 直接将 HTTP 响应体传递给 ParseStream 进行真正的流式解析。
	// 不再使用 io.ReadAll 将整个响应读入内存，避免大响应导致 OOM。
	// resp.Body 的生命周期由 ParseStream 内部的 goroutine 通过 defer body.Close() 管理。
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

// ── AWS SigV4 简化签名 ─────────────────────────────────────────────────────

// signAWSRequest 对 HTTP 请求进行简化的 AWS SigV4 签名。
// 参考: https://docs.aws.amazon.com/general/latest/gr/signature-version-4.html
func signAWSRequest(req *http.Request, body []byte, accessKey, secretKey, sessionToken, region string) error {
	now := time.Now().UTC()
	dateStamp := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")
	serviceName := "bedrock"

	// 计算请求体的 SHA256
	bodyHash := sha256Hex(body)

	// 设置必需的 AWS 头
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("Host", req.URL.Host)
	if sessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", sessionToken)
	}

	// 构建规范请求
	canonicalHeaders := "content-type:" + req.Header.Get("Content-Type") + "\n" +
		"host:" + req.URL.Host + "\n" +
		"x-amz-date:" + amzDate + "\n"
	if sessionToken != "" {
		canonicalHeaders += "x-amz-security-token:" + sessionToken + "\n"
	}
	signedHeaders := "content-type;host;x-amz-date"
	if sessionToken != "" {
		signedHeaders += ";x-amz-security-token"
	}

	canonicalRequest := strings.Join([]string{
		req.Method,
		req.URL.Path,
		req.URL.RawQuery,
		canonicalHeaders,
		signedHeaders,
		bodyHash,
	}, "\n")

	// 构建待签名字符串
	credentialScope := fmt.Sprintf("%s/%s/%s/aws4_request", dateStamp, region, serviceName)
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		credentialScope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	// 计算签名
	signingKey := deriveSigningKey(secretKey, dateStamp, region, serviceName)
	signature := hmacSHA256Hex(signingKey, []byte(stringToSign))

	// 设置 Authorization 头
	authorization := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKey, credentialScope, signedHeaders, signature)
	req.Header.Set("Authorization", authorization)

	return nil
}

// sha256Hex 计算数据的 SHA256 十六进制编码。
func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// hmacSHA256Hex 使用 HMAC-SHA256 计算签名。
func hmacSHA256Hex(key, data []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}

// deriveSigningKey 派生 SigV4 签名密钥。
func deriveSigningKey(secretKey, dateStamp, region, service string) []byte {
	kSecret := []byte("AWS4" + secretKey)
	kDate := hmacSHA256(kSecret, []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))
	return kSigning
}

// hmacSHA256 计算 HMAC-SHA256 摘要。
func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

// ── Bedrock API 类型 ──────────────────────────────────────────────────────

// bedrockConverseResponse Bedrock Converse API 响应。
type bedrockConverseResponse struct {
	Output     bedrockOutput    `json:"output"`
	StopReason string           `json:"stopReason,omitempty"`
	Usage      bedrockUsage     `json:"usage,omitempty"`
	Metrics    *bedrockMetrics  `json:"metrics,omitempty"`
}

// bedrockOutput Bedrock 输出。
type bedrockOutput struct {
	Message bedrockMessage `json:"message"`
}

// bedrockMessage Bedrock 消息。
type bedrockMessage struct {
	Role    string                  `json:"role"`
	Content []bedrockContentBlock   `json:"content"`
}

// bedrockContentBlock Bedrock 内容块。
// 注意：Bedrock Converse 中 tool_result 类型使用嵌套的 toolResult 对象。
// 此结构体支持 text、tool_use、tool_result 三种类型。
type bedrockContentBlock struct {
	Type      string                 `json:"type"`
	Text      string                 `json:"text,omitempty"`
	ToolUseID string                 `json:"toolUseId,omitempty"`
	Name      string                 `json:"name,omitempty"`
	Input     map[string]any         `json:"input,omitempty"`
	ToolResult *bedrockToolResultContent `json:"toolResult,omitempty"`
}

// bedrockToolResultContent Bedrock tool_result 类型的嵌套内容结构。
type bedrockToolResultContent struct {
	ToolUseID string                `json:"toolUseId"`
	Content   []bedrockToolResultPart `json:"content"`
	Status    string                `json:"status,omitempty"`
}

// bedrockToolResultPart tool_result 中的单个内容部分。
type bedrockToolResultPart struct {
	Text string `json:"text,omitempty"`
	JSON any    `json:"json,omitempty"`
}

// bedrockUsage Bedrock token 用量。
type bedrockUsage struct {
	InputTokens        int `json:"inputTokens"`
	OutputTokens       int `json:"outputTokens"`
	TotalTokens        int `json:"totalTokens"`
	CacheReadTokens    int `json:"cacheReadInputTokens,omitempty"`
	CacheWriteTokens   int `json:"cacheWriteInputTokens,omitempty"`
}

// bedrockMetrics Bedrock 性能指标。
type bedrockMetrics struct {
	LatencyMs int `json:"latencyMs"`
}

// bedrockStreamEvent Bedrock 流式 SSE 事件。
type bedrockStreamEvent struct {
	Type         string                    `json:"type"`
	Index        int                       `json:"index,omitempty"`
	ContentBlock *bedrockStreamContentBlock `json:"contentBlock,omitempty"`
	Delta        *bedrockStreamDelta        `json:"delta,omitempty"`
	Usage        *bedrockUsage             `json:"usage,omitempty"` // metadata 事件中的 token 用量
}

// bedrockStreamContentBlock Bedrock 流式内容块。
type bedrockStreamContentBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	ToolUseID string         `json:"toolUseId,omitempty"`
	Name      string         `json:"name,omitempty"`
}

// bedrockStreamDelta Bedrock 流式增量。
type bedrockStreamDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	PartialJSON string `json:"partialJson,omitempty"`
	StopReason  string `json:"stopReason,omitempty"`
}

// ── Bedrock 请求体构建 ───────────────────────────────────────────────────

// bedrockRequestBody Bedrock Converse API 请求体。
type bedrockRequestBody struct {
	InferenceConfig   *bedrockInferenceConfig   `json:"inferenceConfig,omitempty"`
	AdditionalModelRequestFields map[string]any `json:"additionalModelRequestFields,omitempty"`
	Messages          []bedrockRequestMessage   `json:"messages"`
	System            []bedrockSystemBlock      `json:"system,omitempty"`
	ToolConfig        *bedrockToolConfig        `json:"toolConfig,omitempty"`
}

// bedrockInferenceConfig Bedrock 推理配置。
type bedrockInferenceConfig struct {
	MaxTokens   int     `json:"maxTokens,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
	TopP        float64 `json:"topP,omitempty"`
	StopSequences []string `json:"stopSequences,omitempty"`
}

// bedrockRequestMessage Bedrock 请求消息。
type bedrockRequestMessage struct {
	Role    string                  `json:"role"`
	Content []bedrockContentBlock   `json:"content"`
}

// bedrockSystemBlock Bedrock 系统提示块。
type bedrockSystemBlock struct {
	Text string `json:"text"`
}

// bedrockToolConfig Bedrock 工具配置。
type bedrockToolConfig struct {
	Tools []bedrockTool `json:"tools,omitempty"`
}

// bedrockTool Bedrock 工具定义。
type bedrockTool struct {
	ToolSpec *bedrockToolSpec `json:"toolSpec,omitempty"`
}

// bedrockToolSpec Bedrock 工具规格。
type bedrockToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema"`
}

// buildBedrockRequestBody 构建 Bedrock Converse API 请求体。
func buildBedrockRequestBody(req *ChatRequest) *bedrockRequestBody {
	body := &bedrockRequestBody{
		Messages: make([]bedrockRequestMessage, 0, len(req.Messages)),
	}

	// 分离 system 消息
	var systemTexts []string
	for _, msg := range req.Messages {
		if msg.Role == RoleSystem {
			systemTexts = append(systemTexts, msg.Content)
			continue
		}
	}

	// 系统提示
	if len(systemTexts) > 0 {
		body.System = make([]bedrockSystemBlock, 0, len(systemTexts))
		for _, text := range systemTexts {
			body.System = append(body.System, bedrockSystemBlock{Text: text})
		}
	}

	// 转换非 system 消息
	for _, msg := range req.Messages {
		if msg.Role == RoleSystem {
			continue
		}
		bedMsg := convertMessageToBedrock(&msg)
		if bedMsg != nil {
			body.Messages = append(body.Messages, *bedMsg)
		}
	}

	// 推理配置
	inferCfg := &bedrockInferenceConfig{}
	hasInferCfg := false
	if req.MaxTokens > 0 {
		inferCfg.MaxTokens = req.MaxTokens
		hasInferCfg = true
	}
	if req.Temperature > 0 {
		inferCfg.Temperature = req.Temperature
		hasInferCfg = true
	}
	if hasInferCfg {
		body.InferenceConfig = inferCfg
	}

	// 工具定义
	if len(req.Tools) > 0 {
		tools := make([]bedrockTool, 0, len(req.Tools))
		for _, t := range req.Tools {
			params := t.Parameters
			paramsMap, ok := params.(map[string]any)
			if !ok {
				paramsMap = map[string]any{
					"type": "object",
					"properties": map[string]any{},
				}
			}
			tools = append(tools, bedrockTool{
				ToolSpec: &bedrockToolSpec{
					Name:        t.Name,
					Description: t.Description,
					InputSchema: paramsMap,
				},
			})
		}
		body.ToolConfig = &bedrockToolConfig{Tools: tools}
	}

	return body
}

// convertMessageToBedrock 将统一消息转换为 Bedrock 格式。
func convertMessageToBedrock(msg *Message) *bedrockRequestMessage {
	switch msg.Role {
	case RoleUser:
		content := []bedrockContentBlock{}
		if msg.Content != "" {
			content = append(content, bedrockContentBlock{
				Type: "text",
				Text: msg.Content,
			})
		}
		return &bedrockRequestMessage{
			Role:    "user",
			Content: content,
		}

	case RoleAssistant:
		content := []bedrockContentBlock{}
		// 文本内容
		if msg.Content != "" {
			content = append(content, bedrockContentBlock{
				Type: "text",
				Text: msg.Content,
			})
		}
		// 工具调用
		for _, tc := range msg.ToolCalls {
			var input map[string]any
			if err := json.Unmarshal([]byte(tc.Arguments), &input); err != nil {
				input = map[string]any{"_raw": tc.Arguments}
			}
			content = append(content, bedrockContentBlock{
				Type:      "tool_use",
				ToolUseID: tc.ID,
				Name:      tc.Name,
				Input:     input,
			})
		}
		// 确保不为空
		if len(content) == 0 {
			content = []bedrockContentBlock{{Type: "text", Text: "(empty)"}}
		}
		return &bedrockRequestMessage{
			Role:    "assistant",
			Content: content,
		}

	case RoleTool:
		content := msg.Content
		if content == "" {
			content = "(no output)"
		}
		// Bedrock tool_result 格式：{"toolResult": {"toolUseId": "...", "content": [{"text": "..."}]}}
		var resultParts []bedrockToolResultPart
		// 尝试解析为 JSON 对象，如果成功则作为 json 类型
		var jsonObj map[string]any
		if err := json.Unmarshal([]byte(content), &jsonObj); err == nil {
			resultParts = append(resultParts, bedrockToolResultPart{JSON: jsonObj})
		} else {
			resultParts = append(resultParts, bedrockToolResultPart{Text: content})
		}
		return &bedrockRequestMessage{
			Role: "user",
			Content: []bedrockContentBlock{{
				Type: "tool_result",
				ToolResult: &bedrockToolResultContent{
					ToolUseID: msg.ToolCallID,
					Content:   resultParts,
				},
			}},
		}

	default:
		return &bedrockRequestMessage{
			Role: "user",
			Content: []bedrockContentBlock{{Type: "text", Text: msg.Content}},
		}
	}
}

// ── 工具函数 ─────────────────────────────────────────────────────────────

// mapBedrockStopReason 将 Bedrock stopReason 映射为统一的停止原因。
func mapBedrockStopReason(reason string, hasToolCalls bool) string {
	if hasToolCalls {
		return StopToolUse
	}
	switch reason {
	case "end_turn":
		return StopEndTurn
	case "max_tokens":
		return StopMaxTokens
	case "tool_use":
		return StopToolUse
	case "stop_sequence":
		return StopEndTurn
	case "content_filtered":
		return StopContentFilter
	default:
		return StopEndTurn
	}
}

// convertBedrockUsage 将 Bedrock usage 转换为统一的 TokenUsage。
func convertBedrockUsage(usage *bedrockUsage) *TokenUsage {
	if usage == nil {
		return nil
	}
	return &TokenUsage{
		PromptTokens:     usage.InputTokens,
		CompletionTokens: usage.OutputTokens,
		TotalTokens:      usage.TotalTokens,
		CacheReadTokens:  usage.CacheReadTokens,
		CacheWriteTokens: usage.CacheWriteTokens,
	}
}

// ── init 注册 ─────────────────────────────────────────────────────────────

func init() {
	RegisterTransport("bedrock_converse", &BedrockTransport{region: DefaultBedrockRegion})
	slog.Debug("Bedrock 传输层已注册", "apiMode", "bedrock_converse", "region", DefaultBedrockRegion, "time", time.Now())
}
