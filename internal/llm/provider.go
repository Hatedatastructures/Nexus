// Package llm 提供 LLM 提供者接口定义。
package llm

import (
	"context"
	"io"
	"net/http"
)

// ───────────────────────────── 提供者接口 ─────────────────────────────

// Provider 是所有 LLM 后端的统一接口。
// 实现者包括 OpenAI、Anthropic、Gemini、Bedrock 等。
type Provider interface {
	// CreateChatCompletion 发送非流式聊天补全请求。
	// 返回完整的响应对象。
	CreateChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error)

	// CreateChatCompletionStream 发送流式聊天补全请求。
	// 返回文本增量通道，调用者通过 range 遍历。
	// 通道关闭表示流结束。
	CreateChatCompletionStream(ctx context.Context, req *ChatRequest) (<-chan *StreamDelta, error)

	// ListModels 返回可用模型列表。
	ListModels(ctx context.Context) ([]ModelInfo, error)

	// Name 返回提供者标识名称。
	Name() string
}

// ───────────────────────────── 传输层接口 ─────────────────────────────

// Transport 负责将内部 ChatRequest 转换为提供者原生 HTTP 请求。
// 每种 API 模式 (chat_completions / anthropic_messages / bedrock_converse) 对应一个 Transport 实现。
type Transport interface {
	// APIMode 返回 API 模式标识。
	APIMode() string

	// BuildRequest 构建提供者特定的 HTTP 请求。
	// 包括认证头、Content-Type 和请求体转换。
	BuildRequest(ctx context.Context, req *ChatRequest, apiKey string) (*http.Request, error) // 返回 *http.Request 构建器

	// ParseResponse 将提供者的 HTTP 响应解析为统一的 ChatResponse。
	ParseResponse(body []byte) (*ChatResponse, error)

	// ParseStream 解析流式 HTTP 响应体。
	// body 为 HTTP 响应的 ReadCloser，由实现方在 goroutine 中通过 defer 关闭。
	// 从 body 逐行读取 SSE 事件，通过 channel 实时发送 StreamDelta，
	// 当流结束时关闭 channel。
	ParseStream(ctx context.Context, body io.ReadCloser) <-chan *StreamDelta
}
