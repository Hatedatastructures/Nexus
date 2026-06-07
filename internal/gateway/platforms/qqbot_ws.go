package platforms

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/gorilla/websocket"
)

// ───────────────────────────── WebSocket 连接与协议 ─────────────────────────────

// connectWebSocket 连接 WebSocket。
func (a *QQBotAdapter) connectWebSocket(ctx context.Context, gatewayURL string, msgCh chan *MessageEvent) {
	backoff := []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second}
	backoffIdx := 0

	for {
		a.mu.Lock()
		running := a.running
		a.mu.Unlock()

		if !running {
			a.closeOnce.Do(func() { close(msgCh) })
			return
		}

		dialer := websocket.DefaultDialer
		conn, _, err := dialer.DialContext(ctx, gatewayURL, nil)
		if err != nil {
			delay := backoff[min(backoffIdx, len(backoff)-1)]
			backoffIdx++
			slog.Warn("[QQBot] WebSocket connection failed", "err", err, "retry_after", delay)
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}

			// 刷新 token
			if err := a.getAccessToken(ctx); err != nil {
				slog.Warn("[QQBot] failed to refresh token", "err", err)
				continue
			}

			// 获取新的 gateway URL
			newURL, err := a.getGatewayURL(ctx)
			if err != nil {
				slog.Warn("[QQBot] failed to get gateway URL", "err", err)
				continue
			}
			gatewayURL = newURL
			continue
		}

		a.mu.Lock()
		a.conn = conn
		a.connected = true
		a.mu.Unlock()

		backoffIdx = 0

		// 等待 Hello
		_, msg, err := conn.ReadMessage()
		if err != nil {
			_ = conn.Close()
			continue
		}

		var helloPayload map[string]any
		if err := json.Unmarshal(msg, &helloPayload); err != nil {
			_ = conn.Close()
			continue
		}

		// 发送 Identify 或 Resume
		if a.sessionID == "" {
			a.sendIdentify(conn)
		} else {
			a.sendResume(conn)
		}

		// 启动心跳（先取消旧的）
		if a.heartbeatCancel != nil {
			a.heartbeatCancel()
		}
		hbCtx, hbCancel := context.WithCancel(ctx)
		a.heartbeatCancel = hbCancel
		go a.heartbeatLoop(hbCtx)

		// 监听消息，返回导致退出的错误用于提取 close code
		listenErr := a.listenLoop(msgCh)

		// 从 listenLoop 返回的错误中提取 close code
		a.mu.Lock()
		a.connected = false
		closeCode := 0
		if ce, ok := listenErr.(*websocket.CloseError); ok {
			closeCode = ce.Code
		}
		if a.conn != nil {
			_ = a.conn.Close()
			a.conn = nil
		}

		if qqbotFatalCloseCodes[closeCode] {
			slog.Error("[QQBot] fatal close code, stopping reconnect", "code", closeCode)
			a.mu.Unlock()
			return
		}
		if qqbotSessionClearCodes[closeCode] {
			slog.Warn("[QQBot] session-clear close code, resetting session", "code", closeCode)
			a.sessionID = ""
			a.seqNo = 0
		}
		a.mu.Unlock()
	}
}

// sendIdentify 发送 Identify 请求。
func (a *QQBotAdapter) sendIdentify(conn *websocket.Conn) {
	a.mu.Lock()
	token := a.accessToken
	a.mu.Unlock()

	identifyReq := map[string]any{
		"op": qqbotOpIdentify,
		"d": map[string]any{
			"token":   token,
			"intents": qqbotIntentC2CGroupAtMessages | qqbotIntentPublicGuildMessages | qqbotIntentDirectMessage | qqbotIntentInteraction,
			"shard":   []int{0, 1},
			"properties": map[string]any{
				"$os":      "linux",
				"$browser": "nexus-agent",
				"$device":  "nexus-agent",
			},
		},
	}

	a.writeMu.Lock()
	if err := conn.WriteJSON(identifyReq); err != nil {
		slog.Warn("[QQBot] failed to send Identify", "err", err)
	}
	a.writeMu.Unlock()
}

// sendResume 发送 Resume 请求。
func (a *QQBotAdapter) sendResume(conn *websocket.Conn) {
	a.mu.Lock()
	token := a.accessToken
	sessID := a.sessionID
	seq := a.seqNo
	a.mu.Unlock()

	resumeReq := map[string]any{
		"op": qqbotOpResume,
		"d": map[string]any{
			"token":      token,
			"session_id": sessID,
			"seq":        seq,
		},
	}

	a.writeMu.Lock()
	if err := conn.WriteJSON(resumeReq); err != nil {
		slog.Warn("[QQBot] failed to send Resume", "err", err)
	}
	a.writeMu.Unlock()
}

// listenLoop WebSocket 消息监听循环。
// 返回导致退出的错误（可用于提取 WebSocket close code）。
func (a *QQBotAdapter) listenLoop(msgCh chan *MessageEvent) error {
	for {
		a.mu.Lock()
		running := a.running
		conn := a.conn
		a.mu.Unlock()

		if !running || conn == nil {
			return nil
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			return err
		}

		var payload map[string]any
		if err := json.Unmarshal(msg, &payload); err != nil {
			continue
		}

		a.dispatchPayload(payload, msgCh)
	}
}

// dispatchPayload 处理 WebSocket 消息。
// 每个临界区使用 Lock + defer Unlock 防止 panic 导致死锁。
func (a *QQBotAdapter) dispatchPayload(payload map[string]any, msgCh chan *MessageEvent) {
	op := getInt(payload, "op", 0)

	switch op {
	case qqbotOpDispatch:
		t := getString(payload, "t", "")
		s := getInt(payload, "s", 0)
		d := getMap(payload, "d")

		a.mu.Lock()
		a.seqNo = int64(s)
		a.mu.Unlock()

		switch t {
		case "READY":
			a.mu.Lock()
			a.sessionID = getString(d, "session_id", "")
			userMap := getMap(d, "user")
			if userMap != nil {
				a.botOpenID = getString(userMap, "id", "")
			}
			a.mu.Unlock()
			slog.Info("[QQBot] authenticated", "session_id", a.sessionID)

		case "C2C_MESSAGE_CREATE":
			a.onC2CMessage(d, msgCh)

		case "GROUP_AT_MESSAGE_CREATE":
			a.onGroupAtMessage(d, msgCh)

		case "DIRECT_MESSAGE_CREATE":
			a.onDirectMessage(d, msgCh)
		}

	case qqbotOpHeartbeatAck:
		a.mu.Lock()
		a.lastHeartbeat = time.Now()
		a.mu.Unlock()

	case qqbotOpReconnect:
		slog.Warn("[QQBot] received Reconnect command")
		return

	case qqbotOpInvalidSession:
		d := payload["d"]
		a.mu.Lock()
		if boolVal, ok := d.(bool); ok && boolVal {
			slog.Warn("[QQBot] invalid session (resumable), will resume")
		} else {
			slog.Warn("[QQBot] invalid session (non-resumable), clearing session")
			a.sessionID = ""
			a.seqNo = 0
		}
		a.mu.Unlock()
		return
	}
}

// heartbeatLoop 发送心跳。
func (a *QQBotAdapter) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(qqbotHeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.mu.Lock()
			running := a.running
			conn := a.conn
			seq := a.seqNo
			a.mu.Unlock()

			if !running || conn == nil {
				return
			}

			heartbeatReq := map[string]any{
				"op": qqbotOpHeartbeat,
				"d":  seq,
			}

			a.writeMu.Lock()
			if err := conn.WriteJSON(heartbeatReq); err != nil {
				slog.Debug("[QQBot] heartbeat send failed", "err", err)
			}
			a.writeMu.Unlock()
		}
	}
}
