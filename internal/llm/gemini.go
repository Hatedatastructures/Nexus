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
func (t *GeminiTransport) BuildRequest(ctx context.Context, req *ChatRequest, apiKey string) (any, error) {
	body := buildGeminiRequestBody(req)

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化 Gemini 请求体失败: %w", err)
	}

	// Gemini URL: {baseURL}/models/{model}:generateContent
	// API key 通过 x-goog-api-key header 传递，避免密钥暴露在 URL 中
	url := fmt.Sprintf("%s/models/%s:generateContent",
		t.baseURL, req.Model)

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("创建 HTTP 请求失败: %w", err)
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
		return nil, fmt.Errorf("解析 Gemini 响应失败: %w", err)
	}

	if len(geminiResp.Candidates) == 0 {
		return nil, fmt.Errorf("Gemini 响应中没有 candidates")
	}

	candidate := geminiResp.Candidates[0]
	content := candidate.Content

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

// ParseStream 解析 Gemini 流式 SSE 响应，返回 StreamDelta 通道。
func (t *GeminiTransport) ParseStream(ctx context.Context, body io.ReadCloser) <-chan *StreamDelta {
	ch := make(chan *StreamDelta, 256)

	go func() {
		defer close(ch)
		defer body.Close()

		var contentBuilder strings.Builder
		var reasoningBuilder strings.Builder
		toolCallBuilders := make(map[string]*toolCallBuilder) // key = part_index + name
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
						Done:      true,
					}
					return
				}
				continue
			}

			var streamResp geminiStreamResponse
			if err := json.Unmarshal([]byte(event.Data), &streamResp); err != nil {
				slog.Debug("failed to parse Gemini SSE data", "error", err)
				continue
			}

			// 收集流式 usage（最后一个 chunk 包含完整的 usageMetadata）
			if streamResp.UsageMetadata.TotalTokenCount > 0 {
				finalUsage = convertGeminiUsage(&streamResp.UsageMetadata)
			}

			if len(streamResp.Candidates) == 0 {
				continue
			}

			candidate := streamResp.Candidates[0]

			// 处理结束原因
			if candidate.FinishReason != "" {
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
				return
			}

			if candidate.Content == nil {
				continue
			}

			// 处理 parts
			for partIdx, part := range candidate.Content.Parts {
				// 思维文本
				if part.Text != "" && part.Thought {
					reasoningBuilder.WriteString(part.Text)
					ch <- &StreamDelta{
						Reasoning: part.Text,
					}
					continue
				}

				// 普通文本
				if part.Text != "" && !part.Thought {
					contentBuilder.WriteString(part.Text)
					ch <- &StreamDelta{
						Content: part.Text,
					}
				}

				// 函数调用
				if part.FunctionCall != nil && part.FunctionCall.Name != "" {
					fc := part.FunctionCall
					argsJSON, _ := json.Marshal(fc.Args)
					callKey := fmt.Sprintf("%d_%s", partIdx, fc.Name)
					if existing, ok := toolCallBuilders[callKey]; ok {
						// 增量更新参数
						existing.Arguments = strings.Builder{}
						existing.Arguments.WriteString(string(argsJSON))
					} else {
						id := fmt.Sprintf("call_%s", generateShortID())
						b := &toolCallBuilder{
							ID:   id,
							Name: fc.Name,
						}
						b.Arguments.WriteString(string(argsJSON))
						toolCallBuilders[callKey] = b

						ch <- &StreamDelta{
							ToolCalls: []ToolCall{{
								ID:        id,
								Name:      fc.Name,
								Arguments: string(argsJSON),
								Extra:     buildGeminiToolExtra(part),
							}},
						}
					}
				}
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
		return nil, fmt.Errorf("Gemini API 错误 (HTTP %d, %s): %s", resp.StatusCode, classified.Reason, bodyStr)
	}

	return p.transport.ParseResponse(body)
}

// CreateChatCompletionStream 发送流式聊天补全请求。
func (p *GeminiProvider) CreateChatCompletionStream(ctx context.Context, req *ChatRequest) (<-chan *StreamDelta, error) {
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

	// Gemini 流式端点：将 generateContent 替换为 streamGenerateContent?alt=sse
	streamURL := strings.Replace(httpReqTyped.URL.String(), ":generateContent", ":streamGenerateContent?alt=sse", 1)
	if parsed, err := httpReqTyped.URL.Parse(streamURL); err == nil {
		httpReqTyped.URL = parsed
	}
	httpReqTyped.RequestURI = "" // 清空 RequestURI 以避免与 URL 冲突
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
		return nil, fmt.Errorf("Gemini 流式 API 错误 (HTTP %d, %s): %s", resp.StatusCode, classified.Reason, bodyStr)
	}

	// 直接将 HTTP 响应体传递给 ParseStream 进行真正的流式解析。
	// 不再使用 io.ReadAll 将整个响应读入内存，避免大响应导致 OOM。
	// resp.Body 的生命周期由 ParseStream 内部的 goroutine 通过 defer body.Close() 管理。
	return p.transport.ParseStream(ctx, resp.Body), nil
}

// ListModels 通过 Gemini API 获取可用模型列表。
// 调用 GET {baseURL}/models 获取真实模型信息。
// API key 通过 x-goog-api-key header 传递。
func (p *GeminiProvider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	url := p.transport.baseURL + "/models"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("创建模型列表请求失败: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	if p.apiKey != "" {
		httpReq.Header.Set("x-goog-api-key", p.apiKey)
	}

	resp, err := p.transport.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("获取 Gemini 模型列表失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取模型列表响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		bodyStr := string(body)
		classified := ClassifyError(resp.StatusCode, bodyStr)
		return nil, fmt.Errorf("Gemini 模型列表 API 错误 (HTTP %d, %s): %s", resp.StatusCode, classified.Reason, bodyStr)
	}

	var listResp geminiModelListResponse
	if err := json.Unmarshal(body, &listResp); err != nil {
		return nil, fmt.Errorf("解析 Gemini 模型列表失败: %w", err)
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

// ── Gemini API 类型 ────────────────────────────────────────────────────────

// geminiGenerateResponse Gemini generateContent 响应。
type geminiGenerateResponse struct {
	Candidates    []geminiCandidate  `json:"candidates"`
	UsageMetadata geminiUsageMetadata `json:"usageMetadata,omitempty"`
	ModelVersion  string             `json:"modelVersion,omitempty"`
}

// geminiCandidate 候选响应。
type geminiCandidate struct {
	Content      *geminiContent `json:"content"`
	FinishReason string         `json:"finishReason,omitempty"`
	SafetyRatings []any         `json:"safetyRatings,omitempty"`
}

// geminiContent Gemini 内容。
type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts,omitempty"`
}

// geminiPart Gemini 内容部分。
type geminiPart struct {
	Text             string            `json:"text,omitempty"`
	Thought          bool              `json:"thought,omitempty"`
	FunctionCall     *geminiFunctionCall `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
	ThoughtSignature  string            `json:"thoughtSignature,omitempty"`
	InlineData       *geminiInlineData  `json:"inlineData,omitempty"`
}

// geminiFunctionCall Gemini 函数调用。
type geminiFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

// geminiFunctionResponse Gemini 函数响应。
type geminiFunctionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

// geminiInlineData Gemini 内联数据（图片等）。
type geminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

// geminiUsageMetadata Gemini token 用量元数据。
type geminiUsageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount,omitempty"`
	CandidatesTokenCount    int `json:"candidatesTokenCount,omitempty"`
	TotalTokenCount         int `json:"totalTokenCount,omitempty"`
	CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
}

// geminiStreamResponse Gemini 流式响应。
type geminiStreamResponse struct {
	Candidates    []geminiCandidate  `json:"candidates"`
	UsageMetadata geminiUsageMetadata `json:"usageMetadata,omitempty"`
}

// geminiModelListResponse Gemini 模型列表 API 响应。
type geminiModelListResponse struct {
	Models []geminiModelEntry `json:"models"`
}

// geminiModelEntry Gemini 模型条目。
type geminiModelEntry struct {
	Name             string `json:"name"`
	DisplayName      string `json:"displayName,omitempty"`
	Description      string `json:"description,omitempty"`
	InputTokenLimit  int    `json:"inputTokenLimit,omitempty"`
	OutputTokenLimit int    `json:"outputTokenLimit,omitempty"`
	SupportedGenerationMethods []string `json:"supportedGenerationMethods,omitempty"`
	Deprecated       bool   `json:"deprecated,omitempty"`
}

// ── Gemini 请求体构建 ─────────────────────────────────────────────────────

// geminiRequestBody Gemini generateContent 请求体。
type geminiRequestBody struct {
	Contents          []geminiRequestContent   `json:"contents"`
	SystemInstruction *geminiSystemInstruction `json:"systemInstruction,omitempty"`
	Tools             []geminiToolDeclaration  `json:"tools,omitempty"`
	ToolConfig        *geminiToolConfig        `json:"toolConfig,omitempty"`
	GenerationConfig  *geminiGenerationConfig  `json:"generationConfig,omitempty"`
	CachedContent     string                   `json:"cachedContent,omitempty"` // 缓存内容资源名称
}

// geminiRequestContent Gemini 请求内容。
type geminiRequestContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

// geminiSystemInstruction Gemini 系统指令。
type geminiSystemInstruction struct {
	Parts []geminiPart `json:"parts"`
}

// geminiToolDeclaration Gemini 工具声明。
type geminiToolDeclaration struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations,omitempty"`
}

// geminiFunctionDeclaration Gemini 函数声明。
type geminiFunctionDeclaration struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// geminiToolConfig Gemini 工具配置。
type geminiToolConfig struct {
	FunctionCallingConfig *geminiFunctionCallingConfig `json:"functionCallingConfig,omitempty"`
}

// geminiFunctionCallingConfig Gemini 函数调用配置。
type geminiFunctionCallingConfig struct {
	Mode                 string   `json:"mode,omitempty"`
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

// geminiGenerationConfig Gemini 生成配置。
type geminiGenerationConfig struct {
	Temperature     *float64           `json:"temperature,omitempty"`
	MaxOutputTokens int                `json:"maxOutputTokens,omitempty"`
	TopP            *float64           `json:"topP,omitempty"`
	StopSequences   []string           `json:"stopSequences,omitempty"`
	ThinkingConfig  *geminiThinkingConfig `json:"thinkingConfig,omitempty"`
}

// geminiThinkingConfig Gemini 思维配置。
type geminiThinkingConfig struct {
	ThinkingBudget  int    `json:"thinkingBudget,omitempty"`
	IncludeThoughts bool   `json:"includeThoughts,omitempty"`
	ThinkingLevel   string `json:"thinkingLevel,omitempty"`
}

// buildGeminiRequestBody 构建 Gemini API 请求体。
func buildGeminiRequestBody(req *ChatRequest) *geminiRequestBody {
	body := &geminiRequestBody{}

	// 分离 system 消息构建系统指令
	var systemTextParts []string
	var contentList []geminiRequestContent

	// 构建 callID → toolName 索引，用于 RoleTool 消息查找函数名
	callIDToName := make(map[string]string)
	for i := range req.Messages {
		if req.Messages[i].Role == RoleAssistant && len(req.Messages[i].ToolCalls) > 0 {
			for _, tc := range req.Messages[i].ToolCalls {
				callIDToName[tc.ID] = tc.Name
			}
		}
	}

	for _, msg := range req.Messages {
		if msg.Role == RoleSystem {
			systemTextParts = append(systemTextParts, msg.Content)
			continue
		}

		gemMsg := convertMessageToGemini(&msg, callIDToName)
		if gemMsg != nil {
			contentList = append(contentList, *gemMsg)
		}
	}

	body.Contents = contentList

	// 系统指令
	joinedSystem := strings.Join(systemTextParts, "\n")
	if strings.TrimSpace(joinedSystem) != "" {
		body.SystemInstruction = &geminiSystemInstruction{
			Parts: []geminiPart{{Text: joinedSystem}},
		}
	}

	// 工具定义
	if len(req.Tools) > 0 {
		declarations := make([]geminiFunctionDeclaration, 0, len(req.Tools))
		for _, t := range req.Tools {
			params := t.Parameters
			paramsMap, ok := params.(map[string]any)
			if !ok {
				paramsMap = map[string]any{
					"type": "object",
				}
			}
			declarations = append(declarations, geminiFunctionDeclaration{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  paramsMap,
			})
		}
		body.Tools = []geminiToolDeclaration{{FunctionDeclarations: declarations}}
	}

	// 生成配置
	genCfg := &geminiGenerationConfig{}
	hasGenCfg := false

	if req.MaxTokens > 0 {
		genCfg.MaxOutputTokens = req.MaxTokens
		hasGenCfg = true
	}

	if req.Temperature > 0 {
		temp := req.Temperature
		genCfg.Temperature = &temp
		hasGenCfg = true
	}

	if hasGenCfg {
		body.GenerationConfig = genCfg
	}

	// 缓存控制：通过 Metadata 中的 "cached_content" 字段引用预创建的缓存内容
	if req.Metadata != nil {
		if cc, ok := req.Metadata["cached_content"].(string); ok && cc != "" {
			body.CachedContent = cc
		}
		// 思维链配置（通过 Metadata 传递）
		if thinkingBudget, ok := req.Metadata["thinking_budget"].(int); ok && thinkingBudget > 0 {
			if body.GenerationConfig == nil {
				body.GenerationConfig = &geminiGenerationConfig{}
			}
			body.GenerationConfig.ThinkingConfig = &geminiThinkingConfig{
				ThinkingBudget:  thinkingBudget,
				IncludeThoughts: true,
			}
		}
		// 推理级别（通过 Metadata 传递）
		if thinkingLevel, ok := req.Metadata["thinking_level"].(string); ok && thinkingLevel != "" {
			if body.GenerationConfig == nil {
				body.GenerationConfig = &geminiGenerationConfig{}
			}
			if body.GenerationConfig.ThinkingConfig == nil {
				body.GenerationConfig.ThinkingConfig = &geminiThinkingConfig{
					IncludeThoughts: true,
				}
			}
			body.GenerationConfig.ThinkingConfig.ThinkingLevel = thinkingLevel
		}
	}

	return body
}

// convertMessageToGemini 将统一消息转换为 Gemini 格式。
func convertMessageToGemini(msg *Message, callIDToName map[string]string) *geminiRequestContent {
	switch msg.Role {
	case RoleUser:
		parts := []geminiPart{}
		if msg.Content != "" {
			parts = append(parts, geminiPart{Text: msg.Content})
		}
		return &geminiRequestContent{
			Role:  "user",
			Parts: parts,
		}

	case RoleAssistant:
		parts := []geminiPart{}
		// 文本内容
		if msg.Content != "" {
			parts = append(parts, geminiPart{Text: msg.Content})
		}
		// 工具调用
		for _, tc := range msg.ToolCalls {
			var args map[string]any
			if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
				args = map[string]any{"_raw": tc.Arguments}
			}
			part := geminiPart{
				FunctionCall: &geminiFunctionCall{
					Name: tc.Name,
					Args: args,
				},
			}
			// 保留 thought_signature
			if tc.Extra != nil {
				if sig, ok := tc.Extra["thought_signature"].(string); ok && sig != "" {
					part.ThoughtSignature = sig
				}
			}
			parts = append(parts, part)
		}
		return &geminiRequestContent{
			Role:  "model",
			Parts: parts,
		}

	case RoleTool:
		// 工具结果也作为 user 角色的 functionResponse
		content := msg.Content
		if content == "" {
			content = "{}"
		}
		var responseMap map[string]any
		if err := json.Unmarshal([]byte(content), &responseMap); err != nil {
			responseMap = map[string]any{"output": content}
		}
		// 查找函数名：优先 msg.Name，然后从 callIDToName 查找
		funcName := msg.Name
		if funcName == "" {
			funcName = callIDToName[msg.ToolCallID]
		}
		if funcName == "" {
			funcName = "unknown_function"
		}
		return &geminiRequestContent{
			Role: "user",
			Parts: []geminiPart{{
				FunctionResponse: &geminiFunctionResponse{
					Name:     funcName,
					Response: responseMap,
				},
			}},
		}

	default:
		return &geminiRequestContent{
			Role:  "user",
			Parts: []geminiPart{{Text: msg.Content}},
		}
	}
}

// ── 工具函数 ──────────────────────────────────────────────────────────────

// mapGeminiStopReason 将 Gemini finishReason 映射为统一的停止原因。
func mapGeminiStopReason(reason string, hasToolCalls bool) string {
	if hasToolCalls {
		return StopToolCalls
	}
	switch strings.ToUpper(reason) {
	case "STOP":
		return StopEndTurn
	case "MAX_TOKENS":
		return StopMaxTokens
	case "SAFETY", "RECITATION":
		return StopContentFilter
	default:
		return StopEndTurn
	}
}

// convertGeminiUsage 将 Gemini usageMetadata 转换为统一的 TokenUsage。
func convertGeminiUsage(meta *geminiUsageMetadata) *TokenUsage {
	if meta == nil {
		return nil
	}
	return &TokenUsage{
		PromptTokens:     meta.PromptTokenCount,
		CompletionTokens: meta.CandidatesTokenCount,
		TotalTokens:      meta.TotalTokenCount,
		CacheReadTokens:  meta.CachedContentTokenCount,
	}
}

// generateShortID 生成简短的唯一 ID。
func generateShortID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano()%10000000000)
}

// buildGeminiToolExtra 为 Gemini 工具调用构建 Extra 字段（含 thought_signature）。
func buildGeminiToolExtra(part geminiPart) map[string]any {
	if part.ThoughtSignature != "" {
		return map[string]any{
			"extra_content": map[string]any{
				"google": map[string]any{
					"thought_signature": part.ThoughtSignature,
				},
			},
		}
	}
	return nil
}

// ── init 注册 ─────────────────────────────────────────────────────────────

func init() {
	RegisterTransport("gemini_api", &GeminiTransport{baseURL: DefaultGeminiBaseURL})
	slog.Debug("Gemini transport registered", "apiMode", "gemini_api", "time", time.Now())
}
