package platforms

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
)

// ───────────────────────────── 发送消息 ─────────────────────────────

// Send 发送 Markdown 消息。
func (a *WeComAdapter) Send(ctx context.Context, chatID string, content string, opts *SendOptions) (*SendResult, error) {
	if chatID == "" {
		return nil, fmt.Errorf("chat_id 是必填项")
	}

	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()

	if conn == nil {
		return nil, fmt.Errorf("WebSocket 未连接")
	}

	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil, fmt.Errorf("消息内容为空")
	}

	// 检查是否有缓存的回复 req_id
	replyReqID := a.getReplyReqID(chatID)

	reqID := a.newReqID("send")
	req := map[string]any{
		"cmd":     wecomCmdSend,
		"headers": map[string]any{"req_id": reqID},
		"body": map[string]any{
			"chatid":  chatID,
			"msgtype": "markdown",
			"markdown": map[string]any{
				"content": trimmed[:min(len(trimmed), wecomMaxMessageLength)],
			},
		},
	}

	// 如果有回复 req_id，使用 respond 命令
	if replyReqID != "" {
		req["cmd"] = wecomCmdRespond
		req["headers"] = map[string]any{"req_id": replyReqID}
	}

	if err := a.writeWS(conn, req); err != nil {
		return nil, fmt.Errorf("发送失败: %w", err)
	}

	return &SendResult{Success: true}, nil
}

// SendImage 发送图片。
func (a *WeComAdapter) SendImage(ctx context.Context, chatID string, imageURL string, caption string, opts *SendOptions) (*SendResult, error) {
	// 企业微信需要先上传媒体，简化为发送 URL 文本
	result, err := a.Send(ctx, chatID, imageURL, opts)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (a *WeComAdapter) Name() string            { return "WeCom" }
func (a *WeComAdapter) PlatformType() Platform  { return PlatformWeCom }
func (a *WeComAdapter) MaxMessageLength() int   { return wecomMaxMessageLength }
func (a *WeComAdapter) SupportsStreaming() bool { return false }
func (a *WeComAdapter) EditMessage(ctx context.Context, chatID string, messageID string, content string) (*SendResult, error) {
	return a.Send(ctx, chatID, content, nil)
}
func (a *WeComAdapter) DeleteMessage(ctx context.Context, chatID string, messageID string) error {
	return nil
}
func (a *WeComAdapter) SendTyping(ctx context.Context, chatID string) error {
	return nil
}
func (a *WeComAdapter) SendVoice(ctx context.Context, chatID string, audioPath string, opts *SendOptions) (*SendResult, error) {
	return nil, fmt.Errorf("企业微信暂不支持语音消息")
}
func (a *WeComAdapter) SendVideo(ctx context.Context, chatID string, videoPath string, caption string, opts *SendOptions) (*SendResult, error) {
	return nil, fmt.Errorf("企业微信暂不支持视频消息")
}
func (a *WeComAdapter) SendDocument(ctx context.Context, chatID string, filePath string, caption string, opts *SendOptions) (*SendResult, error) {
	return nil, fmt.Errorf("企业微信暂不支持文件消息")
}

// ───────────────────────────── 消息处理 ─────────────────────────────

// onMessage 处理接收到的消息回调。
func (a *WeComAdapter) onMessage(payload map[string]any) {
	body := getMap(payload, "body")
	if body == nil {
		return
	}

	msgID := getString(body, "msgid", a.payloadReqID(payload))
	if msgID == "" {
		msgID = uuid.New().String()
	}

	// 去重检查
	if a.dedup.isDuplicate(msgID) {
		slog.Debug("[WeCom] duplicate message ignored", "msg_id", msgID)
		return
	}

	// 记录回复 req_id
	reqID := a.payloadReqID(payload)
	a.rememberReplyReqID(msgID, reqID)

	// 提取发送者信息
	sender := getMap(body, "from")
	senderID := getString(sender, "userid", "")
	chatID := getString(body, "chatid", senderID)

	if chatID == "" {
		return
	}

	// 检查群组/私聊权限
	isGroup := getString(body, "chattype", "") == "group"
	if isGroup {
		if !a.isGroupAllowed(chatID, senderID) {
			return
		}
	} else {
		if !a.isDMAllowed(senderID) {
			return
		}
	}

	// 记录聊天的 req_id（用于群聊回复）
	a.rememberChatReqID(chatID, reqID)

	// 提取文本
	text := a.extractText(body)

	// 去除群聊中的 @mention
	if isGroup && text != "" {
		text = strings.TrimSpace(strings.ReplaceAll(text, "@"+a.botID, ""))
	}

	if text == "" {
		return
	}

	// 构建消息事件
	chatType := "dm"
	if isGroup {
		chatType = "group"
	}

	event := &MessageEvent{
		Text:        text,
		MessageType: MsgText,
		MessageID:   msgID,
		Source: &SessionSource{
			Platform: PlatformWeCom,
			ChatID:   chatID,
			UserID:   senderID,
			ChatType: chatType,
		},
		RawMessage: payload,
	}

	select {
	case a.msgCh <- event:
	default:
		slog.Warn("[WeCom] message channel full, dropping message", "msg_id", msgID)
	}
}

// extractText 从消息体提取文本。
func (a *WeComAdapter) extractText(body map[string]any) string {
	msgType := getString(body, "msgtype", "")

	if msgType == "mixed" {
		mixed := getMap(body, "mixed")
		items := getListAny(mixed, "msg_item")
		var parts []string
		for _, item := range items {
			itemMap, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if getString(itemMap, "msgtype", "") == "text" {
				textBlock := getMap(itemMap, "text")
				content := getString(textBlock, "content", "")
				if content != "" {
					parts = append(parts, content)
				}
			}
		}
		return strings.Join(parts, "\n")
	}

	textBlock := getMap(body, "text")
	return getString(textBlock, "content", "")
}

// ───────────────────────────── 权限检查 ─────────────────────────────

// isDMAllowed 检查私聊权限。
func (a *WeComAdapter) isDMAllowed(senderID string) bool {
	if a.dmPolicy == "disabled" {
		return false
	}
	if a.dmPolicy == "allowlist" {
		return entryMatches(a.allowFrom, senderID)
	}
	return true
}

// isGroupAllowed 检查群聊权限。
func (a *WeComAdapter) isGroupAllowed(chatID, _ string) bool {
	if a.groupPolicy == "disabled" {
		return false
	}
	if a.groupPolicy == "allowlist" {
		return entryMatches(a.groupAllowFrom, chatID)
	}
	return true
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

func (a *WeComAdapter) newReqID(prefix string) string {
	return fmt.Sprintf("%s-%s", prefix, uuid.New().String()[:8])
}

func (a *WeComAdapter) payloadReqID(payload map[string]any) string {
	headers := getMap(payload, "headers")
	return getString(headers, "req_id", "")
}

func (a *WeComAdapter) rememberReplyReqID(msgID, reqID string) {
	if msgID == "" || reqID == "" {
		return
	}
	a.mu.Lock()
	if len(a.replyReqIDs) > wecomMapMaxSize {
		for k := range a.replyReqIDs {
			delete(a.replyReqIDs, k)
			if len(a.replyReqIDs) <= wecomMapMaxSize/2 {
				break
			}
		}
	}
	a.replyReqIDs[msgID] = reqID
	a.mu.Unlock()
}

func (a *WeComAdapter) rememberChatReqID(chatID, reqID string) {
	if chatID == "" || reqID == "" {
		return
	}
	a.mu.Lock()
	if len(a.lastChatReqIDs) > wecomMapMaxSize {
		for k := range a.lastChatReqIDs {
			delete(a.lastChatReqIDs, k)
			if len(a.lastChatReqIDs) <= wecomMapMaxSize/2 {
				break
			}
		}
	}
	a.lastChatReqIDs[chatID] = reqID
	a.mu.Unlock()
}

func (a *WeComAdapter) getReplyReqID(chatID string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastChatReqIDs[chatID]
}

// entryMatches 检查条目是否匹配（支持 * 通配符）。
func entryMatches(entries []string, target string) bool {
	targetLower := strings.ToLower(strings.TrimSpace(target))
	for _, entry := range entries {
		normalized := strings.ToLower(strings.TrimSpace(entry))
		if normalized == "*" || normalized == targetLower {
			return true
		}
	}
	return false
}
