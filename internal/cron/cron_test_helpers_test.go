package cron

import (
	"context"

	"nexus-agent/internal/agent"
	"nexus-agent/internal/gateway/platforms"
)

// mockAdapter 用于测试投递逻辑的 mock 平台适配器。
type mockAdapter struct {
	name       string
	platform   platforms.Platform
	sendErr    error
	sendResult *platforms.SendResult
	sendCalled int
}

func (m *mockAdapter) Name() string                     { return m.name }
func (m *mockAdapter) PlatformType() platforms.Platform { return m.platform }
func (m *mockAdapter) Connect(_ context.Context) (<-chan *platforms.MessageEvent, error) {
	ch := make(chan *platforms.MessageEvent)
	close(ch)
	return ch, nil
}
func (m *mockAdapter) Disconnect(_ context.Context) error { return nil }
func (m *mockAdapter) Send(_ context.Context, _ string, _ string, _ *platforms.SendOptions) (*platforms.SendResult, error) {
	m.sendCalled++
	if m.sendErr != nil {
		return nil, m.sendErr
	}
	if m.sendResult != nil {
		return m.sendResult, nil
	}
	return &platforms.SendResult{Success: true, MessageID: "msg-123"}, nil
}
func (m *mockAdapter) EditMessage(_ context.Context, _, _, _ string) (*platforms.SendResult, error) {
	return &platforms.SendResult{Success: true}, nil
}
func (m *mockAdapter) DeleteMessage(_ context.Context, _, _ string) error { return nil }
func (m *mockAdapter) SendTyping(_ context.Context, _ string) error       { return nil }
func (m *mockAdapter) SendImage(_ context.Context, _, _, _ string, _ *platforms.SendOptions) (*platforms.SendResult, error) {
	return &platforms.SendResult{Success: true}, nil
}
func (m *mockAdapter) SendVoice(_ context.Context, _, _ string, _ *platforms.SendOptions) (*platforms.SendResult, error) {
	return &platforms.SendResult{Success: true}, nil
}
func (m *mockAdapter) SendVideo(_ context.Context, _, _, _ string, _ *platforms.SendOptions) (*platforms.SendResult, error) {
	return &platforms.SendResult{Success: true}, nil
}
func (m *mockAdapter) SendDocument(_ context.Context, _, _, _ string, _ *platforms.SendOptions) (*platforms.SendResult, error) {
	return &platforms.SendResult{Success: true}, nil
}
func (m *mockAdapter) MaxMessageLength() int   { return 4096 }
func (m *mockAdapter) SupportsStreaming() bool { return false }

// mockRunner 是 conversationRunner 的测试替身。
type mockRunner struct {
	result *agent.TurnResult
	err    error
	called bool
}

func (m *mockRunner) runConversation(_ context.Context, _ string, _ []any, _ string) (*agent.TurnResult, error) {
	m.called = true
	return m.result, m.err
}
