// Package llm 提供 LLM 提供者抽象层。
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// openAIStreamChunk OpenAI 流式响应块。
type openAIStreamChunk struct {
	ID      string               `json:"id"`
	Object  string               `json:"object"`
	Created int64                `json:"created"`
	Model   string               `json:"model"`
	Choices []openAIStreamChoice `json:"choices"`
	Usage   *openAIUsage         `json:"usage,omitempty"` // 流式最后一个 chunk 可能包含 usage
}

// openAIStreamChoice 流式响应选项。
type openAIStreamChoice struct {
	Index        int               `json:"index"`
	Delta        openAIStreamDelta `json:"delta"`
	FinishReason string            `json:"finish_reason"`
}

// openAIStreamDelta 流式增量。
type openAIStreamDelta struct {
	Role             string                `json:"role,omitempty"`
	Content          string                `json:"content,omitempty"`
	ToolCalls        []openAIToolCallDelta `json:"tool_calls,omitempty"`
	ReasoningContent string                `json:"reasoning_content,omitempty"`
}

// openAIToolCallDelta 流式工具调用增量。
type openAIToolCallDelta struct {
	Index    int                     `json:"index"`
	ID       string                  `json:"id,omitempty"`
	Type     string                  `json:"type,omitempty"`
	Function openAIFunctionCallDelta `json:"function,omitempty"`
}

// openAIFunctionCallDelta 流式函数调用增量。
type openAIFunctionCallDelta struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// toolCallBuilder 用于在流式模式下构建工具调用。
type toolCallBuilder struct {
	ID        string
	Name      string
	Arguments strings.Builder
}

// ParseStream 解析 OpenAI 流式 Chat Completions 响应体，返回 StreamDelta 通道。
// body 由调用方传入的 HTTP 响应体 ReadCloser，本方法负责在 goroutine 结束时关闭。
func (t *OpenAITransport) ParseStream(ctx context.Context, body io.ReadCloser) <-chan *StreamDelta {
	ch := make(chan *StreamDelta, 256)

	go func() {
		defer close(ch)
		defer func() { _ = body.Close() }() // 确保响应体被关闭，防止资源泄漏

		var contentBuilder strings.Builder
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
				ch <- &StreamDelta{
					Content:   contentBuilder.String(),
					ToolCalls: toolCalls,
					Usage:     finalUsage,
					Done:      true,
				}
				return
			}

			var chunk openAIStreamChunk
			if err := json.Unmarshal([]byte(event.Data), &chunk); err != nil {
				slog.Debug("failed to parse SSE data", "data", event.Data[:min(len(event.Data), 200)], "error", err)
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

			// 推理增量
			if delta.ReasoningContent != "" {
				ch <- &StreamDelta{
					Reasoning: delta.ReasoningContent,
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
					ToolCalls: toolCalls,
					Usage:     finalUsage,
					Done:      true,
				}
				return
			}
		}
	}()

	return ch
}

// CreateChatCompletionStream 发送流式聊天补全请求。
func (p *OpenAIProvider) CreateChatCompletionStream(ctx context.Context, req *ChatRequest) (<-chan *StreamDelta, error) {
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
		return nil, fmt.Errorf("HTTP 流式请求失败: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxLLMResponseSize))
		_ = resp.Body.Close()
		bodyStr := string(body)
		classified := ClassifyError(resp.StatusCode, bodyStr)
		return nil, fmt.Errorf("OpenAI 流式 API 错误 (HTTP %d, %s): %s", resp.StatusCode, classified.Reason, RedactErrorBody(bodyStr))
	}

	// 直接将 HTTP 响应体传递给 ParseStream 进行真正的流式解析。
	// 不再使用 io.ReadAll 将整个响应读入内存，避免大响应导致 OOM。
	// resp.Body 的生命周期由 ParseStream 内部的 goroutine 通过 defer body.Close() 管理。
	return p.transport.ParseStream(ctx, resp.Body), nil
}
