// Package testutil 提供测试用的模拟对象和辅助工具。
// 本文件实现 llm.Provider 的模拟版本，支持预设响应和错误注入。
package testutil

import (
	"context"
	"fmt"
	"sync"

	"nexus-agent/internal/llm"
)

// ───────────────────────────── MockProvider ─────────────────────────────

// MockProvider 是 llm.Provider 的模拟实现。
// 通过预设响应和错误注入来控制测试行为。
type MockProvider struct {
	mu sync.Mutex

	// ── 配置字段 ──

	// Name_ 返回提供者名称。
	Name_ string

	// ChatResponse 预设的非流式响应。
	ChatResponse *llm.ChatResponse

	// ChatError 预设的非流式错误。
	ChatError error

	// StreamDeltas 预设的流式增量列表。
	StreamDeltas []*llm.StreamDelta

	// StreamError 预设的流式错误。
	StreamError error

	// Models 预设的模型列表。
	Models []llm.ModelInfo

	// ListModelsError 预设的 ListModels 错误。
	ListModelsError error

	// ── 记录字段 (用于断言) ──

	// ChatRequests 记录所有收到的非流式请求。
	ChatRequests []*llm.ChatRequest

	// StreamRequests 记录所有收到的流式请求。
	StreamRequests []*llm.ChatRequest

	// CreateChatCompletionFunc 可选的自定义实现，优先于预设字段。
	CreateChatCompletionFunc func(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error)

	// CreateChatCompletionStreamFunc 可选的自定义实现，优先于预设字段。
	CreateChatCompletionStreamFunc func(ctx context.Context, req *llm.ChatRequest) (<-chan *llm.StreamDelta, error)
}

// ───────────────────────────── Provider 接口实现 ─────────────────────────────

// CreateChatCompletion 发送非流式聊天补全请求。
func (m *MockProvider) CreateChatCompletion(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	m.mu.Lock()
	m.ChatRequests = append(m.ChatRequests, req)
	m.mu.Unlock()

	if m.CreateChatCompletionFunc != nil {
		return m.CreateChatCompletionFunc(ctx, req)
	}

	if m.ChatError != nil {
		return nil, m.ChatError
	}

	if m.ChatResponse != nil {
		return m.ChatResponse, nil
	}

	// 默认返回一个简单的成功响应
	return &llm.ChatResponse{
		ID:      fmt.Sprintf("mock-resp-%d", len(m.ChatRequests)),
		Model:   req.Model,
		Content: "mock response",
		Usage: &llm.TokenUsage{
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
		},
	}, nil
}

// CreateChatCompletionStream 发送流式聊天补全请求。
func (m *MockProvider) CreateChatCompletionStream(ctx context.Context, req *llm.ChatRequest) (<-chan *llm.StreamDelta, error) {
	m.mu.Lock()
	m.StreamRequests = append(m.StreamRequests, req)
	m.mu.Unlock()

	if m.CreateChatCompletionStreamFunc != nil {
		return m.CreateChatCompletionStreamFunc(ctx, req)
	}

	if m.StreamError != nil {
		return nil, m.StreamError
	}

	ch := make(chan *llm.StreamDelta, len(m.StreamDeltas)+1)
	go func() {
		defer close(ch)
		for _, delta := range m.StreamDeltas {
			select {
			case ch <- delta:
			case <-ctx.Done():
				return
			}
		}
		// 发送最终完成增量
		ch <- &llm.StreamDelta{Done: true}
	}()

	return ch, nil
}

// ListModels 返回可用模型列表。
func (m *MockProvider) ListModels(ctx context.Context) ([]llm.ModelInfo, error) {
	if m.ListModelsError != nil {
		return nil, m.ListModelsError
	}

	if m.Models != nil {
		return m.Models, nil
	}

	return []llm.ModelInfo{}, nil
}

// Name 返回提供者标识名称。
func (m *MockProvider) Name() string {
	if m.Name_ != "" {
		return m.Name_
	}
	return "mock"
}

// ───────────────────────────── 辅助方法 ─────────────────────────────

// Reset 清空所有记录的请求。
func (m *MockProvider) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ChatRequests = nil
	m.StreamRequests = nil
}

// SetChatResponse 设置预设的非流式响应并清除错误。
func (m *MockProvider) SetChatResponse(resp *llm.ChatResponse) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ChatResponse = resp
	m.ChatError = nil
}

// SetChatError 设置预设的非流式错误。
func (m *MockProvider) SetChatError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ChatError = err
}

// SetStreamDeltas 设置预设的流式增量并清除错误。
func (m *MockProvider) SetStreamDeltas(deltas []*llm.StreamDelta) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.StreamDeltas = deltas
	m.StreamError = nil
}

// SetStreamError 设置预设的流式错误。
func (m *MockProvider) SetStreamError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.StreamError = err
}
