// Package llm 提供 LLM 提供者抽象层。
package llm

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
)

// ParseStream 解析 Bedrock Converse Stream 响应，返回 StreamDelta 通道。
func (t *BedrockTransport) ParseStream(ctx context.Context, body io.ReadCloser) <-chan *StreamDelta {
	ch := make(chan *StreamDelta, 256)

	go func() {
		defer close(ch)
		defer func() { _ = body.Close() }()

		var contentBuilder strings.Builder
		var reasoningBuilder strings.Builder
		toolCallBuilders := make(map[int]*toolCallBuilder)
		var finalUsage *TokenUsage // 流式响应的累积 token 用量

		for event := range ParseBinaryEventStream(ctx, body) {
			select {
			case <-ctx.Done():
				return
			default:
			}

			if event.Data == "" {
				continue
			}
			if event.Data == "[DONE]" {
				// 流结束
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

			// Bedrock 流式响应也是 SSE 格式，使用 message_delta 事件
			var streamEvent bedrockStreamEvent
			if err := json.Unmarshal([]byte(event.Data), &streamEvent); err != nil {
				slog.Debug("failed to parse Bedrock SSE data", "error", err)
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
					Content:    contentBuilder.String(),
					ToolCalls:  toolCalls,
					Reasoning:  reasoningBuilder.String(),
					Usage:      finalUsage,
					StopReason: "end_turn",
					Done:       true,
				}
				return
			}
		}

		// 如果循环正常结束而没有 Done，发送最终增量
		select {
		case <-ctx.Done():
			return
		default:
		}
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
	}()

	return ch
}
