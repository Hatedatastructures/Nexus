package platforms

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// ───────────────────────────── 监听循环与消息处理 ─────────────────────────────

// listenLoop WebSocket 消息监听循环。
func (a *YuanbaoAdapter) listenLoop(msgCh chan *MessageEvent) {
	for {
		a.mu.Lock()
		running := a.running
		conn := a.conn
		a.mu.Unlock()

		if !running {
			a.closeOnce.Do(func() { close(msgCh) })
			return
		}

		if conn == nil {
			// 尝试重连
			time.Sleep(5 * time.Second)
			reconnectCtx, reconnectCancel := context.WithTimeout(context.Background(), yuanbaoConnectTimeout)

			token, _, err := a.getSignToken(reconnectCtx)
			if err != nil {
				reconnectCancel()
				slog.Warn("[Yuanbao] failed to get token", "err", err)
				continue
			}

			newConn, err := a.openConnection(reconnectCtx, token)
			reconnectCancel()
			if err != nil {
				slog.Warn("[Yuanbao] reconnect failed", "err", err)
				continue
			}

			a.mu.Lock()
			a.conn = newConn
			a.connected = true
			a.mu.Unlock()

			slog.Info("[Yuanbao] reconnected")
			continue
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			slog.Warn("[Yuanbao] failed to read message", "err", err)
			a.mu.Lock()
			a.connected = false
			if a.conn != nil {
				_ = a.conn.Close()
				a.conn = nil
			}
			a.mu.Unlock()
			continue
		}

		var payload map[string]any
		if err := json.Unmarshal(msg, &payload); err != nil {
			continue
		}

		a.dispatchPayload(payload, msgCh)
	}
}

// dispatchPayload 处理接收到的 WebSocket 消息。
func (a *YuanbaoAdapter) dispatchPayload(payload map[string]any, msgCh chan *MessageEvent) {
	cmd := getString(payload, "cmd", "")

	switch cmd {
	case yuanbaoCmdT05:
		event := a.parseMessage(payload)
		if event != nil && !a.dedup.isDuplicate(event.MessageID) {
			select {
			case msgCh <- event:
			default:
				slog.Warn("[Yuanbao] message channel full, dropping message")
			}
		}
	case yuanbaoCmdPong:
		return
	default:
		seqNo := getInt(payload, "seq_no", 0)
		if seqNo > 0 {
			seqNoStr := fmt.Sprintf("%d", seqNo)
			a.mu.Lock()
			ch, exists := a.pendingResps[seqNoStr]
			if exists {
				delete(a.pendingResps, seqNoStr)
				a.mu.Unlock()
				ch <- payload
				return
			}
			a.mu.Unlock()
		}
	}
}

// parseMessage 解析消息。
func (a *YuanbaoAdapter) parseMessage(payload map[string]any) *MessageEvent {
	body := getMap(payload, "body")
	if body == nil {
		return nil
	}

	msgID := getString(body, "msg_id", "")
	if msgID == "" {
		b := make([]byte, 8)
		if _, err := rand.Read(b); err == nil {
			msgID = fmt.Sprintf("%x", b)
		} else {
			msgID = fmt.Sprintf("%d", time.Now().UnixNano())
		}
	}

	fromUser := getMap(body, "from_user")
	userID := getString(fromUser, "user_id", "")

	chatID := getString(body, "chat_id", userID)
	chatType := getString(body, "chat_type", "c2c")
	isGroup := chatType == "group"

	text := ""
	msgType := getString(body, "msg_type", "text")
	if msgType == "text" {
		textBlock := getMap(body, "text")
		text = getString(textBlock, "content", "")
	}

	if text == "" {
		return nil
	}

	if isGroup && text != "" {
		text = strings.TrimSpace(strings.Replace(text, "@"+a.botID, "", 1))
	}

	return &MessageEvent{
		Text:        text,
		MessageType: MsgText,
		MessageID:   msgID,
		Source: &SessionSource{
			Platform: PlatformYuanbao,
			ChatID:   chatID,
			UserID:   userID,
			ChatType: chatType,
		},
		RawMessage: payload,
	}
}
