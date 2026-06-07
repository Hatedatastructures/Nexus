package platforms

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ───────────────────────────── 轮询循环 ─────────────────────────────

// pollLoop 轮询新消息。
func (a *BlueBubblesAdapter) pollLoop(ctx context.Context, msgCh chan *MessageEvent) {
	defer a.closeOnce.Do(func() { close(msgCh) })
	ticker := time.NewTicker(bbPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			a.mu.Lock()
			running := a.running
			a.mu.Unlock()

			if !running {
				return
			}

			messages, err := a.fetchNewMessages(ctx)
			if err != nil {
				slog.Warn("[BlueBubbles] failed to fetch messages", "err", err)
				continue
			}

			for _, msg := range messages {
				select {
				case <-ctx.Done():
					return
				default:
					if err := a.safeSend(msgCh, msg); err != nil {
						return
					}
				}
			}
		case <-ctx.Done():
			// msgCh 由 Disconnect 通过 closeOnce 关闭
			return
		}
	}
}

// safeSend 安全发送消息到 channel，防止向已关闭 channel 发送导致 panic。
func (a *BlueBubblesAdapter) safeSend(ch chan *MessageEvent, msg *MessageEvent) error {
	defer func() {
		_ = recover() // channel 已关闭
	}()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-timer.C:
		return fmt.Errorf("send timeout")
	case ch <- msg:
		return nil
	}
}

// fetchNewMessages 获取新消息。
func (a *BlueBubblesAdapter) fetchNewMessages(ctx context.Context) ([]*MessageEvent, error) {
	// 调用 BlueBubbles API 获取最近消息
	params := "limit=50&withChats=true&sort=DESC"
	if a.lastGUID != "" {
		params += "&afterGuid=" + url.QueryEscape(a.lastGUID)
	}

	resp, err := a.apiRequest(ctx, "GET", "/api/v1/message?"+params, nil)
	if err != nil {
		return nil, err
	}

	// 解析响应
	data := getMap(resp, "data")
	if data == nil {
		return nil, nil
	}

	messagesList := getListAny(data, "messages")
	if len(messagesList) == 0 {
		return nil, nil
	}

	var events []*MessageEvent
	for i := len(messagesList) - 1; i >= 0; i-- {
		msgMap, ok := messagesList[i].(map[string]any)
		if !ok {
			continue
		}

		// 去重
		msgGUID := getString(msgMap, "guid", "")
		if msgGUID == "" || a.dedup.isDuplicate(msgGUID) {
			continue
		}

		// 过滤自己发送的消息
		isFromMe, _ := msgMap["isFromMe"].(bool)
		if isFromMe {
			continue
		}

		event := a.parseMessage(msgMap)
		if event != nil {
			events = append(events, event)
			a.lastGUID = msgGUID
		}
	}

	return events, nil
}

// parseMessage 将 BlueBubbles 消息转换为 MessageEvent。
func (a *BlueBubblesAdapter) parseMessage(msg map[string]any) *MessageEvent {
	text := getString(msg, "text", "")
	msgGUID := getString(msg, "guid", "")

	// 提取聊天信息
	chats := getListAny(msg, "chats")
	chatID := ""
	chatType := "dm"
	chatName := ""

	if len(chats) > 0 {
		if chatMap, ok := chats[0].(map[string]any); ok {
			chatID = getString(chatMap, "guid", "")
			chatName = getString(chatMap, "displayName", "")
			// isGroup 判断聊天类型
			isGroup, _ := chatMap["isGroup"].(bool)
			if isGroup {
				chatType = "group"
			}
		}
	}

	// 提取发送者
	handle := getMap(msg, "handle")
	userID := ""
	userName := ""
	if handle != nil {
		userID = getString(handle, "id", "")
		userName = getString(handle, "address", "")
	}

	// 确定消息类型
	msgType := MsgText
	if text == "" {
		// 检查是否有附件
		attachments := getListAny(msg, "attachments")
		if len(attachments) > 0 {
			if attMap, ok := attachments[0].(map[string]any); ok {
				mimeType := getString(attMap, "mimeType", "")
				if strings.HasPrefix(mimeType, "image/") {
					msgType = MsgPhoto
				} else if strings.HasPrefix(mimeType, "audio/") {
					msgType = MsgVoice
				} else if strings.HasPrefix(mimeType, "video/") {
					msgType = MsgVideo
				} else {
					msgType = MsgDocument
				}
			}
		}
	}

	// 提取时间戳
	var timestamp time.Time
	if dateCreated, ok := msg["dateCreated"].(float64); ok && dateCreated > 0 {
		// BlueBubbles 时间戳是毫秒级 Unix 时间
		timestamp = time.UnixMilli(int64(dateCreated))
	}

	return &MessageEvent{
		Text:        text,
		MessageType: msgType,
		MessageID:   msgGUID,
		Source: &SessionSource{
			Platform: PlatformBlueBubbles,
			ChatID:   chatID,
			ChatName: chatName,
			ChatType: chatType,
			UserID:   userID,
			UserName: userName,
		},
		Timestamp:  timestamp,
		RawMessage: msg,
	}
}

// ───────────────────────────── API 调用 ─────────────────────────────

// apiRequest 发送 API 请求并返回结果。
func (a *BlueBubblesAdapter) apiRequest(ctx context.Context, method, path string, body map[string]any) (map[string]any, error) {
	reqURL := a.baseURL + path

	var bodyReader io.Reader
	if body != nil {
		if method == "GET" {
			// GET 请求: 将参数编码到 URL 查询字符串
			q := url.Values{}
			for k, v := range body {
				q.Set(k, fmt.Sprintf("%v", v))
			}
			if len(q) > 0 {
				if strings.Contains(reqURL, "?") {
					reqURL += "&" + q.Encode()
				} else {
					reqURL += "?" + q.Encode()
				}
			}
		} else {
			// POST 请求: JSON 编码请求体
			data, err := json.Marshal(body)
			if err != nil {
				return nil, fmt.Errorf("序列化请求体失败: %w", err)
			}
			bodyReader = bytes.NewReader(data)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	// 设置请求头
	req.Header.Set("Content-Type", "application/json")

	// 添加密码认证（使用 header 传递）
	if a.password != "" {
		req.Header.Set("Authorization", "Bearer "+a.password)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("API 错误 (HTTP %d)", resp.StatusCode)
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	// 检查 API 错误
	status := getString(result, "status", "")
	if status == "error" {
		errMsg := getString(result, "error", "未知错误")
		return nil, fmt.Errorf("API 错误: %s", errMsg)
	}

	return result, nil
}

// ping 验证与 BlueBubbles 服务器的连接。
func (a *BlueBubblesAdapter) ping(ctx context.Context) error {
	resp, err := a.apiRequest(ctx, "GET", "/api/v1/ping", nil)
	if err != nil {
		return fmt.Errorf("ping 失败: %w", err)
	}

	data := getMap(resp, "data")
	if data == nil {
		return fmt.Errorf("ping 响应无效")
	}

	// 检查是否需要密码
	encrypted, _ := data["encrypted"].(bool)
	if encrypted && a.password == "" {
		return fmt.Errorf("BlueBubbles 服务器需要密码，请设置 BLUEBUBBLES_PASSWORD")
	}

	return nil
}
