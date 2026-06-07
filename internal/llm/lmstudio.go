// Package llm 提供 LM Studio 本地推理传输层。
// LM Studio 运行本地模型服务器，暴露 OpenAI 兼容 API，
// 并扩展了 reasoning_content 字段用于推理模型的思维链输出。
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	pkgerrors "nexus-agent/internal/errors"
	"strings"
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
func (t *LMStudioTransport) BuildRequest(ctx context.Context, req *ChatRequest, apiKey string) (*http.Request, error) {
	body := buildOpenAIRequestBody(req)

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ProviderAPI, "序列化 LM Studio 请求体失败", err)
	}

	url := t.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ProviderAPI, "创建 LM Studio HTTP 请求失败", err)
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
		return nil, pkgerrors.Wrap(pkgerrors.ProviderAPI, "解析 LM Studio 响应失败", err)
	}

	if len(oaiResp.Choices) == 0 {
		return nil, pkgerrors.New(pkgerrors.ProviderAPI, "LM Studio 响应中没有 choices")
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
		if choice.FinishReason == "" {
			response.StopReason = StopToolUse
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
		defer func() { _ = body.Close() }()

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
					Content:    contentBuilder.String(),
					Reasoning:  reasoningBuilder.String(),
					ToolCalls:  toolCalls,
					StopReason: "end_turn",
					Done:       true,
				}
				return
			}
		}
		// 检查 context 是否已取消，避免取消后仍发送 Done 增量
		select {
		case <-ctx.Done():
			return
		default:
		}
		// 流通道关闭后（ParseSSEStream 遇到 [DONE] 或 EOF），发送最终 Done 增量
		toolCalls = make([]ToolCall, 0, len(toolCallBuilders))
		for _, builder := range toolCallBuilders {
			toolCalls = append(toolCalls, ToolCall{
				ID:        builder.ID,
				Name:      builder.Name,
				Arguments: builder.Arguments.String(),
			})
		}
		ch <- &StreamDelta{
			Content:    contentBuilder.String(),
			Reasoning:  reasoningBuilder.String(),
			ToolCalls:  toolCalls,
			Usage:      finalUsage,
			StopReason: StopEndTurn,
			Done:       true,
		}
	}()

	return ch
}

