// Package llm 提供 LLM 提供者抽象层。
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"

	pkgerrors "nexus-agent/internal/errors"
)

// ── Anthropic 流式 SSE 类型 ────────────────────────────────────────────────

// anthropicSSEEvent Anthropic SSE 流事件。
type anthropicSSEEvent struct {
	Type         string                  `json:"type"`
	Index        int                     `json:"index,omitempty"`
	Delta        *anthropicStreamDelta   `json:"delta,omitempty"`
	ContentBlock *anthropicContentBlock  `json:"content_block,omitempty"`
	Message      *anthropicStreamMessage `json:"message,omitempty"` // message_start 中的 message
	Usage        *anthropicUsage         `json:"usage,omitempty"`   // message_delta 中的 usage
}

// anthropicStreamMessage message_start 事件中的 message 对象（含 input token 用量）。
type anthropicStreamMessage struct {
	ID    string          `json:"id"`
	Model string          `json:"model"`
	Usage *anthropicUsage `json:"usage,omitempty"`
}

// anthropicStreamDelta Anthropic 流式增量。
type anthropicStreamDelta struct {
	Type       string `json:"type,omitempty"`
	Text       string `json:"text,omitempty"`
	Thinking   string `json:"thinking,omitempty"`
	StopReason string `json:"stop_reason,omitempty"`
}

// ── AnthropicTransport 流式方法 ────────────────────────────────────────────

// ParseStream 解析 Anthropic 流式 SSE 响应，返回 StreamDelta 通道。
func (t *AnthropicTransport) ParseStream(ctx context.Context, body io.ReadCloser) <-chan *StreamDelta {
	ch := make(chan *StreamDelta, 256)

	go func() {
		defer close(ch)
		defer func() { _ = body.Close() }() // 确保响应体被关闭，防止资源泄漏

		var contentBuilder strings.Builder
		var reasoningBuilder strings.Builder
		toolCallBuilders := make(map[int]*toolCallBuilder)
		var inputTokens, outputTokens int // 累积的 input/output token 计数

		for event := range ParseSSEStream(ctx, body) {
			select {
			case <-ctx.Done():
				return
			default:
			}

			if event.Data == "" {
				continue
			}

			// 检查是否是 message_stop 事件（message_delta 不再提前拦截，需要解析 usage）
			if event.Event == "message_stop" {
				// 流结束事件，发送最终增量
				var toolCalls []ToolCall
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
					Reasoning: reasoningBuilder.String(),
					Usage: &TokenUsage{
						PromptTokens:     inputTokens,
						CompletionTokens: outputTokens,
						TotalTokens:      inputTokens + outputTokens,
					},
					StopReason: "end_turn",
					Done:       true,
				}
				return
			}

			var sseEvent anthropicSSEEvent
			if err := json.Unmarshal([]byte(event.Data), &sseEvent); err != nil {
				slog.Debug("failed to parse Anthropic SSE event", "error", err)
				continue
			}

			switch sseEvent.Type {
			case "message_start":
				// message_start 事件包含 input token 用量
				if sseEvent.Message != nil && sseEvent.Message.Usage != nil {
					inputTokens = sseEvent.Message.Usage.InputTokens
				}

			case "content_block_start":
				handleContentBlockStart(&sseEvent, toolCallBuilders)

			case "content_block_delta":
				handleContentBlockDelta(&sseEvent, &contentBuilder, &reasoningBuilder, toolCallBuilders, ch)

			case "content_block_stop":
				// 内容块结束，不发送事件

			case "message_delta":
				// message_delta 事件包含 output token 用量
				if sseEvent.Usage != nil {
					outputTokens = sseEvent.Usage.OutputTokens
				}
				if sseEvent.Delta != nil {
					if sseEvent.Delta.StopReason != "" {
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
							Usage: &TokenUsage{
								PromptTokens:     inputTokens,
								CompletionTokens: outputTokens,
								TotalTokens:      inputTokens + outputTokens,
							},
							Done: true,
						}
						return
					}
				}

			case "ping":
				// 保持连接活跃的心跳事件，忽略
			}
		}
	}()

	return ch
}

// ── AnthropicProvider 流式方法 ──────────────────────────────────────────────

// CreateChatCompletionStream 发送流式聊天补全请求。
func (p *AnthropicProvider) CreateChatCompletionStream(ctx context.Context, req *ChatRequest) (<-chan *StreamDelta, error) {
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

	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := p.transport.httpClient.Do(httpReq)
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.NetworkHTTP, "HTTP 流式请求失败", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxLLMResponseSize))
		_ = resp.Body.Close()
		bodyStr := string(body)
		classified := ClassifyError(resp.StatusCode, bodyStr)
		return nil, pkgerrors.New(pkgerrors.ProviderAPI, fmt.Sprintf("Anthropic 流式 API 错误 (HTTP %d, %s): %s", resp.StatusCode, classified.Reason, RedactErrorBody(bodyStr)))
	}

	// 直接将 HTTP 响应体传递给 ParseStream 进行真正的流式解析。
	// 不再使用 io.ReadAll 将整个响应读入内存，避免大响应导致 OOM。
	// resp.Body 的生命周期由 ParseStream 内部的 goroutine 通过 defer func() { _ = body.Close() }() 管理。
	return p.transport.ParseStream(ctx, resp.Body), nil
}

// ── 流式处理辅助函数 ──────────────────────────────────────────────────────

// handleContentBlockStart 处理 content_block_start 事件。
func handleContentBlockStart(event *anthropicSSEEvent, builders map[int]*toolCallBuilder) {
	if event.ContentBlock == nil {
		return
	}
	if event.ContentBlock.Type == "tool_use" {
		builders[event.Index] = &toolCallBuilder{
			ID:   event.ContentBlock.ID,
			Name: event.ContentBlock.Name,
		}
	}
}

// handleContentBlockDelta 处理 content_block_delta 事件。
func handleContentBlockDelta(
	event *anthropicSSEEvent,
	contentBuilder *strings.Builder,
	reasoningBuilder *strings.Builder,
	builders map[int]*toolCallBuilder,
	ch chan<- *StreamDelta,
) {
	if event.Delta == nil {
		return
	}

	switch event.Delta.Type {
	case "text_delta":
		if event.Delta.Text != "" {
			contentBuilder.WriteString(event.Delta.Text)
			ch <- &StreamDelta{
				Content: event.Delta.Text,
			}
		}
	case "thinking_delta":
		if event.Delta.Thinking != "" {
			reasoningBuilder.WriteString(event.Delta.Thinking)
			ch <- &StreamDelta{
				Reasoning: event.Delta.Thinking,
			}
		}
	case "input_json_delta":
		// 工具调用参数增量
		if builder, ok := builders[event.Index]; ok && event.Delta.Text != "" {
			builder.Arguments.WriteString(event.Delta.Text)
		}
	}
}
