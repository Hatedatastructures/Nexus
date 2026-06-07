// Package platforms 提供 Signal 平台适配器 — 消息/事件处理。
package platforms

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// signalDeduplicator 消息去重器。
type signalDeduplicator struct {
	mu      sync.Mutex
	msgIDs  map[string]time.Time
	maxSize int
}

func newSignalDeduplicator(maxSize int) *signalDeduplicator {
	return &signalDeduplicator{
		msgIDs:  make(map[string]time.Time),
		maxSize: maxSize,
	}
}

func (d *signalDeduplicator) isDuplicate(msgID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, exists := d.msgIDs[msgID]; exists {
		return true
	}

	d.msgIDs[msgID] = time.Now()

	if len(d.msgIDs) > d.maxSize {
		for id, t := range d.msgIDs {
			if time.Since(t) > 5*time.Minute {
				delete(d.msgIDs, id)
			}
		}
	}

	return false
}

// ───────────────────────────── SSE 监听与消息处理 ─────────────────────────────

// sseListener SSE 消息监听。
func (a *SignalAdapter) sseListener(ctx context.Context, msgCh chan *MessageEvent) {
	defer a.closeOnce.Do(func() { close(a.msgCh) })

	for {
		a.mu.Lock()
		running := a.running
		a.mu.Unlock()

		if !running {
			return
		}

		// SSE 连接
		url := a.httpURL + "/api/v1/receive/" + a.account

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			slog.Warn("[Signal] failed to create request", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}

		// 检查 ctx 取消，避免在 ctx 已取消时发起请求
		select {
		case <-ctx.Done():
			return
		default:
		}

		req.Header.Set("Accept", "text/event-stream")

		resp, err := a.httpClient.Do(req)
		if err != nil {
			slog.Warn("[Signal] SSE connection failed", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}

		func() {
			defer func() { _ = resp.Body.Close() }()
			a.parseSSEStream(resp.Body, msgCh)
		}()

		// 连接断开，重连
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

// parseSSEStream 解析 SSE 流。
func (a *SignalAdapter) parseSSEStream(body io.ReadCloser, msgCh chan *MessageEvent) {
	decoder := json.NewDecoder(io.LimitReader(body, 100<<20))

	for {
		a.mu.Lock()
		running := a.running
		a.mu.Unlock()

		if !running {
			return
		}

		var envelope map[string]any
		if err := decoder.Decode(&envelope); err != nil {
			if err != io.EOF {
				slog.Debug("[Signal] SSE parse failed", "err", err)
			}
			return
		}

		// 处理信封
		event := a.handleEnvelope(envelope)
		if event != nil {
			select {
			case msgCh <- event:
			default:
				slog.Warn("[Signal] message channel full, dropping message")
			}
		}
	}
}

// handleEnvelope 处理 Signal 信封。
func (a *SignalAdapter) handleEnvelope(envelope map[string]any) *MessageEvent {
	// 检查是否是自己发送的消息（防循环）
	timestamp := getInt(envelope, "timestamp", 0)
	if a.isSentTimestamp(int64(timestamp)) {
		return nil
	}

	// 提取数据消息
	dataMessage := getMap(envelope, "data_message")
	if dataMessage == nil {
		return nil
	}

	// 提取文本
	text := getString(dataMessage, "message", "")
	if text == "" {
		return nil
	}

	// 提取发送者信息
	source := getMap(envelope, "source")
	sourceNumber := getString(source, "number", "")
	if sourceNumber == "" {
		sourceNumber = getString(envelope, "source", "")
	}

	// 确定聊天 ID 和类型
	chatID := sourceNumber
	chatType := "dm"

	groupInfo := getMap(dataMessage, "group_info")
	if groupInfo != nil {
		chatID = getString(groupInfo, "group_id", "")
		chatType = "group"

		// 去除群聊中的 @mention
		text = strings.ReplaceAll(text, "@"+a.account, "")
		text = strings.TrimSpace(text)
	}

	// 去重
	msgID := fmt.Sprintf("%d", timestamp)
	if a.dedup.isDuplicate(msgID) {
		return nil
	}

	// 构建消息事件
	event := &MessageEvent{
		Text:        text,
		MessageType: MsgText,
		MessageID:   msgID,
		Source: &SessionSource{
			Platform: PlatformSignal,
			ChatID:   chatID,
			UserID:   sourceNumber,
			ChatType: chatType,
		},
		RawMessage: envelope,
	}

	// 提取附件
	attachments := getListAny(dataMessage, "attachments")
	if len(attachments) > 0 {
		for _, att := range attachments {
			if attMap, ok := att.(map[string]any); ok {
				contentType := getString(attMap, "content_type", "")
				if strings.HasPrefix(contentType, "image/") {
					event.MessageType = MsgPhoto
				} else if strings.HasPrefix(contentType, "audio/") {
					event.MessageType = MsgVoice
				}
				break
			}
		}
	}

	// 提取引用回复
	quote := getMap(dataMessage, "quote")
	if quote != nil {
		event.ReplyToMsgID = fmt.Sprintf("%d", getInt(quote, "timestamp", 0))
	}

	return event
}

// isSentTimestamp 检查是否是自己发送的时间戳。
func (a *SignalAdapter) isSentTimestamp(ts int64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, exists := a.sentTimestamps[ts]
	return exists
}

// markSentTimestamp 记录发送时间戳。
func (a *SignalAdapter) markSentTimestamp(ts int64) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.sentTimestamps[ts] = time.Now()

	// 清理过期条目
	if len(a.sentTimestamps) > signalDedupMaxSize {
		now := time.Now()
		for key, t := range a.sentTimestamps {
			if now.Sub(t) > 10*time.Minute {
				delete(a.sentTimestamps, key)
			}
		}
	}
}
