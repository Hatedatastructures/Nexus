// Package llm 提供 OpenAI Codex Responses API 传输。
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// CodexTransport 实现 OpenAI Codex Responses API。
type CodexTransport struct {
	httpClient *http.Client
	baseURL    string
}

// NewCodexTransport 创建 Codex 传输层。
func NewCodexTransport(httpClient *http.Client, baseURL string) *CodexTransport {
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	return &CodexTransport{
		httpClient: httpClient,
		baseURL:    strings.TrimRight(baseURL, "/"),
	}
}

func (t *CodexTransport) APIMode() string { return "codex_responses" }

func (t *CodexTransport) BuildRequest(ctx context.Context, req *ChatRequest, apiKey string) (any, error) {
	type codexInput struct {
		Type    string `json:"type"`
		Content string `json:"content"`
		Role    string `json:"role,omitempty"`
	}

	type codexMsg struct {
		Type    string     `json:"type"`
		Role    string     `json:"role"`
		Content []codexInput `json:"content,omitempty"`
	}

	inputs := make([]codexMsg, 0, len(req.Messages))
	for _, msg := range req.Messages {
		inputs = append(inputs, codexMsg{
			Type: "message",
			Role: string(msg.Role),
			Content: []codexInput{{Type: "input_text", Content: msg.Content}},
		})
	}

	body := map[string]any{
		"model":       req.Model,
		"input":       inputs,
		"tools":       req.Tools,
		"max_output_tokens": req.MaxTokens,
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化 Codex 请求体失败: %w", err)
	}

	url := t.baseURL + "/v1/responses"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("创建 Codex 请求失败: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}
	return httpReq, nil
}

func (t *CodexTransport) ParseResponse(body []byte) (*ChatResponse, error) {
	var resp struct {
		Output []struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage  *TokenUsage `json:"usage"`
		Status string      `json:"status"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("解析 Codex 响应失败: %w", err)
	}

	var content strings.Builder
	for _, item := range resp.Output {
		if item.Role == "assistant" {
			for _, c := range item.Content {
				content.WriteString(c.Text)
			}
		}
	}

	// 空 output: 返回错误而非静默成功
	if len(resp.Output) == 0 {
		if resp.Status == "failed" {
			return nil, fmt.Errorf("Codex 请求失败 (status=failed)")
		}
		if resp.Status == "cancelled" {
			return nil, fmt.Errorf("Codex 请求被取消 (status=cancelled)")
		}
		return &ChatResponse{
			Content:    "",
			StopReason: StopEndTurn,
			Usage:      resp.Usage,
		}, nil
	}

	result := content.String()

	// 根据 status 字段判断 stop reason
	stopReason := StopEndTurn
	switch resp.Status {
	case "incomplete":
		stopReason = StopLength
	case "failed", "cancelled":
		stopReason = StopEndTurn
	}

	return &ChatResponse{
		Content:    result,
		StopReason: stopReason,
		Usage:      resp.Usage,
	}, nil
}

func (t *CodexTransport) ParseStream(ctx context.Context, body io.ReadCloser) <-chan *StreamDelta {
	ch := make(chan *StreamDelta, 1)
	ch <- &StreamDelta{Done: true, Error: fmt.Errorf("Codex Responses API does not support streaming, use non-streaming call")}
	close(ch)
	body.Close()
	return ch
}
