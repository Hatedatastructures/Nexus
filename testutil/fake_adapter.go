// Package testutil 提供测试用的模拟对象和辅助工具。
// 本文件实现 platforms.PlatformAdapter 的假实现，记录发送的消息并返回可配置的结果。
package testutil

import (
	"context"
	"fmt"
	"sync"

	"nexus-agent/internal/gateway/platforms"
)

// ───────────────────────────── SentMessage ─────────────────────────────

// SentMessage 记录一条通过 FakeAdapter 发送的消息。
type SentMessage struct {
	Method  string         // 方法名: "Send", "EditMessage", "SendImage" 等
	ChatID  string         // 聊天 ID
	Content string         // 消息内容/文件路径
	Extra   map[string]any // 额外参数
}

// ───────────────────────────── FakeAdapter ─────────────────────────────

// FakeAdapter 是 platforms.PlatformAdapter 的假实现。
// 记录所有发送的消息并返回可配置的结果。
type FakeAdapter struct {
	mu sync.Mutex

	// ── 配置字段 ──

	// AdapterName 返回适配器名称。
	AdapterName string

	// Platform 返回平台类型。
	Platform platforms.Platform

	// SendResult 预设的 Send 结果。
	SendResult *platforms.SendResult

	// SendError 预设的 Send 错误。
	SendError error

	// ConnectChannel 预设的 Connect 消息通道。
	ConnectChannel chan *platforms.MessageEvent

	// ConnectError 预设的 Connect 错误。
	ConnectError error

	// DisconnectError 预设的 Disconnect 错误。
	DisconnectError error

	// MaxMsgLength 消息最大长度。
	MaxMsgLength int

	// StreamingEnabled 是否支持流式输出。
	StreamingEnabled bool

	// ── 记录字段 (用于断言) ──

	// Messages 记录所有发送的消息。
	Messages []SentMessage

	// Connected 标记是否已连接。
	Connected bool

	// Disconnected 标记是否已断开。
	Disconnected bool
}

// ───────────────────────────── PlatformAdapter 接口实现 ─────────────────────────────

// Name 返回适配器名称。
func (f *FakeAdapter) Name() string {
	if f.AdapterName != "" {
		return f.AdapterName
	}
	return "FakeAdapter"
}

// PlatformType 返回平台类型。
func (f *FakeAdapter) PlatformType() platforms.Platform {
	if f.Platform != "" {
		return f.Platform
	}
	return platforms.PlatformLocal
}

// Connect 建立连接。
func (f *FakeAdapter) Connect(ctx context.Context) (<-chan *platforms.MessageEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.ConnectError != nil {
		return nil, f.ConnectError
	}

	f.Connected = true

	if f.ConnectChannel != nil {
		return f.ConnectChannel, nil
	}

	ch := make(chan *platforms.MessageEvent, 10)
	return ch, nil
}

// Disconnect 断开连接。
func (f *FakeAdapter) Disconnect(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Disconnected = true
	return f.DisconnectError
}

// Send 发送文本消息。
func (f *FakeAdapter) Send(ctx context.Context, chatID string, content string, opts *platforms.SendOptions) (*platforms.SendResult, error) {
	f.mu.Lock()
	f.Messages = append(f.Messages, SentMessage{
		Method:  "Send",
		ChatID:  chatID,
		Content: content,
		Extra:   optsToMap(opts),
	})
	f.mu.Unlock()

	return f.defaultSendResult(), f.SendError
}

// EditMessage 编辑已发送的消息。
func (f *FakeAdapter) EditMessage(ctx context.Context, chatID string, messageID string, content string) (*platforms.SendResult, error) {
	f.mu.Lock()
	f.Messages = append(f.Messages, SentMessage{
		Method:  "EditMessage",
		ChatID:  chatID,
		Content: content,
		Extra:   map[string]any{"message_id": messageID},
	})
	f.mu.Unlock()

	return f.defaultSendResult(), f.SendError
}

// DeleteMessage 删除已发送的消息。
func (f *FakeAdapter) DeleteMessage(ctx context.Context, chatID string, messageID string) error {
	f.mu.Lock()
	f.Messages = append(f.Messages, SentMessage{
		Method: "DeleteMessage",
		ChatID: chatID,
		Extra:  map[string]any{"message_id": messageID},
	})
	f.mu.Unlock()

	return f.SendError
}

// SendTyping 发送"正在输入"指示器。
func (f *FakeAdapter) SendTyping(ctx context.Context, chatID string) error {
	f.mu.Lock()
	f.Messages = append(f.Messages, SentMessage{
		Method: "SendTyping",
		ChatID: chatID,
	})
	f.mu.Unlock()

	return nil
}

// SendImage 发送图片消息。
func (f *FakeAdapter) SendImage(ctx context.Context, chatID string, imageURL string, caption string, opts *platforms.SendOptions) (*platforms.SendResult, error) {
	f.mu.Lock()
	f.Messages = append(f.Messages, SentMessage{
		Method:  "SendImage",
		ChatID:  chatID,
		Content: imageURL,
		Extra: map[string]any{
			"caption": caption,
			"opts":    opts,
		},
	})
	f.mu.Unlock()

	return f.defaultSendResult(), f.SendError
}

// SendVoice 发送语音消息。
func (f *FakeAdapter) SendVoice(ctx context.Context, chatID string, audioPath string, opts *platforms.SendOptions) (*platforms.SendResult, error) {
	f.mu.Lock()
	f.Messages = append(f.Messages, SentMessage{
		Method:  "SendVoice",
		ChatID:  chatID,
		Content: audioPath,
		Extra:   map[string]any{"opts": opts},
	})
	f.mu.Unlock()

	return f.defaultSendResult(), f.SendError
}

// SendVideo 发送视频消息。
func (f *FakeAdapter) SendVideo(ctx context.Context, chatID string, videoPath string, caption string, opts *platforms.SendOptions) (*platforms.SendResult, error) {
	f.mu.Lock()
	f.Messages = append(f.Messages, SentMessage{
		Method:  "SendVideo",
		ChatID:  chatID,
		Content: videoPath,
		Extra: map[string]any{
			"caption": caption,
			"opts":    opts,
		},
	})
	f.mu.Unlock()

	return f.defaultSendResult(), f.SendError
}

// SendDocument 发送文件消息。
func (f *FakeAdapter) SendDocument(ctx context.Context, chatID string, filePath string, caption string, opts *platforms.SendOptions) (*platforms.SendResult, error) {
	f.mu.Lock()
	f.Messages = append(f.Messages, SentMessage{
		Method:  "SendDocument",
		ChatID:  chatID,
		Content: filePath,
		Extra: map[string]any{
			"caption": caption,
			"opts":    opts,
		},
	})
	f.mu.Unlock()

	return f.defaultSendResult(), f.SendError
}

// MaxMessageLength 返回消息最大长度。
func (f *FakeAdapter) MaxMessageLength() int {
	if f.MaxMsgLength > 0 {
		return f.MaxMsgLength
	}
	return 4096
}

// SupportsStreaming 返回是否支持流式输出。
func (f *FakeAdapter) SupportsStreaming() bool {
	return f.StreamingEnabled
}

// ───────────────────────────── 辅助方法 ─────────────────────────────

// defaultSendResult 返回默认的发送结果。
func (f *FakeAdapter) defaultSendResult() *platforms.SendResult {
	if f.SendResult != nil {
		return f.SendResult
	}
	return &platforms.SendResult{
		Success:   true,
		MessageID: fmt.Sprintf("fake-msg-%d", len(f.Messages)),
	}
}

// Reset 清空所有记录。
func (f *FakeAdapter) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Messages = nil
	f.Connected = false
	f.Disconnected = false
}

// MessagesByMethod 返回指定方法名的所有消息。
func (f *FakeAdapter) MessagesByMethod(method string) []SentMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	var msgs []SentMessage
	for _, m := range f.Messages {
		if m.Method == method {
			msgs = append(msgs, m)
		}
	}
	return msgs
}

// LastMessage 返回最后一条发送的消息。
func (f *FakeAdapter) LastMessage() (*SentMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.Messages) == 0 {
		return nil, fmt.Errorf("没有已发送的消息")
	}
	return &f.Messages[len(f.Messages)-1], nil
}

// optsToMap 将 SendOptions 转换为 map。
func optsToMap(opts *platforms.SendOptions) map[string]any {
	if opts == nil {
		return nil
	}
	return map[string]any{
		"reply_to":   opts.ReplyToMsgID,
		"parse_mode": opts.ParseMode,
		"silent":     opts.Silent,
		"metadata":   opts.Metadata,
	}
}
