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

// geminiStreamResponse Gemini 流式响应。
type geminiStreamResponse struct {
	Candidates    []geminiCandidate   `json:"candidates"`
	UsageMetadata geminiUsageMetadata `json:"usageMetadata,omitempty"`
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

// ParseStream 解析 Gemini 流式 SSE 响应，返回 StreamDelta 通道。
func (t *GeminiTransport) ParseStream(ctx context.Context, body io.ReadCloser) <-chan *StreamDelta {
	ch := make(chan *StreamDelta, 256)

	go func() {
		defer close(ch)
		defer func() { _ = body.Close() }()

		var contentBuilder strings.Builder
		var reasoningBuilder strings.Builder
		toolCallBuilders := make(map[string]*toolCallBuilder) // key = part_index + name
		var finalUsage *TokenUsage                            // 流式响应的累积 token 用量

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

// CreateChatCompletionStream 发送流式聊天补全请求。
func (p *GeminiProvider) CreateChatCompletionStream(ctx context.Context, req *ChatRequest) (<-chan *StreamDelta, error) {
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

	// Gemini 流式端点：将 generateContent 替换为 streamGenerateContent?alt=sse
	streamURL := strings.Replace(httpReq.URL.String(), ":generateContent", ":streamGenerateContent?alt=sse", 1)
	if parsed, err := httpReq.URL.Parse(streamURL); err == nil {
		httpReq.URL = parsed
	}
	httpReq.RequestURI = "" // 清空 RequestURI 以避免与 URL 冲突
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
		return nil, pkgerrors.New(pkgerrors.ProviderAPI, fmt.Sprintf("Gemini 流式 API 错误 (HTTP %d, %s): %s", resp.StatusCode, classified.Reason, RedactErrorBody(bodyStr)))
	}

	// 直接将 HTTP 响应体传递给 ParseStream 进行真正的流式解析。
	// 不再使用 io.ReadAll 将整个响应读入内存，避免大响应导致 OOM。
	// resp.Body 的生命周期由 ParseStream 内部的 goroutine 通过 defer func() { _ = body.Close() }() 管理。
	return p.transport.ParseStream(ctx, resp.Body), nil
}
