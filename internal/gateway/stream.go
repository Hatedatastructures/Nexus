// Package gateway 提供流式消费者。
// StreamConsumer 将同步的文本增量回调转换为异步的平台消息编辑。
// 运行在独立 goroutine，通过缓冲和速率限制控制编辑频率。
package gateway

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"nexus-agent/internal/gateway/platforms"
)

// ───────────────────────────── 流式消费者 ─────────────────────────────

// StreamConsumer 接收 AIAgent 的文本增量，缓冲并渐进编辑平台消息。
// 设计目标: 将同步的 streamCallback 转换为异步的平台消息编辑流。
//
// 工作流程:
//   1. AIAgent 调用 OnDelta(text) 推送文本增量
//   2. deltaCh 缓冲增量
//   3. Run goroutine 消费增量，累积到 buffer
//   4. 缓冲区达到阈值 → adapter.Send/EditMessage
//   5. 编辑间隔未到 → 跳过 (防速率限制)
//
// 第一次发送使用 adapter.Send(), 后续使用 adapter.EditMessage()。
type StreamConsumer struct {
	adapter      platforms.PlatformAdapter // 平台适配器
	chatID       string                    // 目标聊天 ID
	currentMsgID string                    // 正在编辑的消息 ID
	buffer       strings.Builder           // 文本累积缓冲区
	bufferSize   int                       // 触发发送的缓冲区阈值 (字符数)
	editInterval time.Duration             // 最小编辑间隔
	lastEditTime time.Time                 // 上次编辑时间
	deltaCh      chan string               // 增量通道
	done         chan struct{}              // 完成信号
	runDone      chan struct{}              // Run goroutine 退出信号
	finished     atomic.Bool               // 是否已完成
	closeOnce    sync.Once                 // 确保 Finish 只关闭一次
}

// NewStreamConsumer 创建流式消费者。
// bufferSize 为触发编辑的字符阈值 (默认 40)，editInterval 为最小编辑间隔 (默认 1s)。
func NewStreamConsumer(adapter platforms.PlatformAdapter, chatID string, bufferSize int, editInterval time.Duration) *StreamConsumer {
	if bufferSize <= 0 {
		bufferSize = 40
	}
	if editInterval <= 0 {
		editInterval = time.Second
	}
	return &StreamConsumer{
		adapter:      adapter,
		chatID:       chatID,
		bufferSize:   bufferSize,
		editInterval: editInterval,
		deltaCh:      make(chan string, 256),
		done:         make(chan struct{}),
		runDone:      make(chan struct{}),
	}
}

// OnDelta 是 AIAgent 的 streamCallback。
// 同步调用，线程安全，将文本增量推入缓冲通道。
func (s *StreamConsumer) OnDelta(text string) {
	if s.finished.Load() {
		return
	}
	if text == "" {
		return
	}
	// 非阻塞发送: 如果通道已满，丢弃增量 (防止 agent goroutine 阻塞)
	select {
	case s.deltaCh <- text:
	default:
		slog.Warn("stream consumer delta channel full, dropping delta",
			"chat_id", s.chatID,
		)
	}
}

// Run 异步消费循环。
// 在独立 goroutine 中运行，通过 ctx 取消。
// 消费 deltaCh 中的文本增量，缓冲并渐进编辑平台消息。
func (s *StreamConsumer) Run(ctx context.Context) {
	defer close(s.runDone) // 通知 Finish goroutine 已退出

	// 连续失败计数 (用于指数退避)
	consecutiveFailures := 0
	const maxConsecutiveFailures = 3

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.done:
			return
		case delta, ok := <-s.deltaCh:
			if !ok {
				return
			}
			// 累积文本
			s.buffer.WriteString(delta)
			currentText := s.buffer.String()

			// 速率限制检查
			now := time.Now()
			if !s.lastEditTime.IsZero() && now.Sub(s.lastEditTime) < s.editInterval {
				// 如果还没到编辑间隔且有更多增量要处理，先累积
				s.tryDrainPending()
				continue
			}

			// 缓冲区阈值检查 (未达到阈值且通道中还有数据则继续累积)
			if len(currentText) < s.bufferSize {
				if s.tryDrainPending() {
					currentText = s.buffer.String()
				}
				if len(currentText) < s.bufferSize {
					continue
				}
			}

			// 发送/编辑消息
			ok2 := s.sendOrEdit(ctx, currentText)
			if ok2 {
				s.lastEditTime = time.Now()
				consecutiveFailures = 0
			} else {
				consecutiveFailures++
				// 指数退避: 连续失败后跳过更多编辑
				if consecutiveFailures >= maxConsecutiveFailures {
					slog.Warn("stream consumer too many consecutive failures, disabling edits",
						"chat_id", s.chatID,
						"failures", consecutiveFailures,
					)
				}
			}
		}
	}
}

// Finish 发送最终内容。
// 关闭增量通道，发送完整的累积文本，返回最终消息 ID。
func (s *StreamConsumer) Finish(ctx context.Context) string {
	s.closeOnce.Do(func() {
		s.finished.Store(true)
		close(s.done)
	})

	// 等待 Run goroutine 退出后再读取共享状态 (buffer, currentMsgID)
	select {
	case <-s.runDone:
	case <-ctx.Done():
		return ""
	}

	finalText := s.buffer.String()

	// 如果没有累积内容，返回空
	if finalText == "" {
		return ""
	}

	// 如果有当前编辑的消息，发送最终编辑
	if s.currentMsgID != "" && s.adapter.SupportsStreaming() {
		result, err := s.adapter.EditMessage(ctx, s.chatID, s.currentMsgID, finalText)
		if err == nil && result.Success {
			slog.Debug("stream consumer final edit sent",
				"chat_id", s.chatID,
				"msg_id", s.currentMsgID,
				"len", len(finalText),
			)
			return s.currentMsgID
		}

		slog.Warn("stream consumer final edit failed, falling back to send",
			"chat_id", s.chatID,
			"err", err,
		)
	}

	// 回退: 发送新消息
	result, err := s.adapter.Send(ctx, s.chatID, finalText, nil)
	if err != nil {
		slog.Error("stream consumer final send failed",
			"chat_id", s.chatID,
			"err", err,
		)
		return ""
	}

	slog.Debug("stream consumer final send",
		"chat_id", s.chatID,
		"msg_id", result.MessageID,
		"len", len(finalText),
	)
	return result.MessageID
}

// ───────────────────────────── 内部方法 ─────────────────────────────

// sendOrEdit 发送或编辑消息。
// 首次调用使用 adapter.Send，后续使用 adapter.EditMessage。
func (s *StreamConsumer) sendOrEdit(ctx context.Context, text string) bool {
	if s.currentMsgID != "" && s.adapter.SupportsStreaming() {
		result, err := s.adapter.EditMessage(ctx, s.chatID, s.currentMsgID, text)
		if err != nil {
			slog.Warn("stream consumer edit failed", "err", err)
			return false
		}
		return result.Success
	}

	// 首次发送
	result, err := s.adapter.Send(ctx, s.chatID, text, nil)
	if err != nil {
		slog.Warn("stream consumer send failed", "err", err)
		return false
	}
	if result.Success {
		s.currentMsgID = result.MessageID
	}
	return result.Success
}

// tryDrainPending 尝试非阻塞地排空通道中的待处理增量。
// 返回是否有新的增量被添加到缓冲区。
func (s *StreamConsumer) tryDrainPending() bool {
	drained := false
	for {
		select {
		case delta, ok := <-s.deltaCh:
			if !ok {
				return drained
			}
			s.buffer.WriteString(delta)
			drained = true
		default:
			return drained
		}
	}
}
