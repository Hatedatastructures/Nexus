package platforms

import (
	"log/slog"
	"strings"
)

// ───────────────────────────── 消息处理 ─────────────────────────────

// onC2CMessage 处理私聊消息。
func (a *QQBotAdapter) onC2CMessage(d map[string]any, msgCh chan *MessageEvent) {
	author := getMap(d, "author")
	userOpenID := getString(author, "id", "")

	// 检查权限
	if !a.isDMAllowed(userOpenID) {
		return
	}

	msgID := getString(d, "id", "")
	if a.dedup.isDuplicate(msgID) {
		return
	}

	content := getString(d, "content", "")
	if content == "" {
		return
	}

	event := &MessageEvent{
		Text:        content,
		MessageType: MsgText,
		MessageID:   msgID,
		Source: &SessionSource{
			Platform: PlatformQQBot,
			ChatID:   userOpenID,
			UserID:   userOpenID,
			ChatType: "dm",
		},
		RawMessage: d,
	}

	select {
	case msgCh <- event:
	default:
		slog.Warn("[QQBot] message channel full, dropping message")
	}
}

// onGroupAtMessage 处理群 @消息。
func (a *QQBotAdapter) onGroupAtMessage(d map[string]any, msgCh chan *MessageEvent) {
	groupID := getString(d, "group_openid", "")
	author := getMap(d, "author")
	userOpenID := getString(author, "id", "")

	// 检查权限
	if !a.isGroupAllowed(groupID, userOpenID) {
		return
	}

	msgID := getString(d, "id", "")
	if a.dedup.isDuplicate(msgID) {
		return
	}

	content := getString(d, "content", "")
	if content == "" {
		return
	}

	// 去除 @mention
	a.mu.Lock()
	botID := a.botOpenID
	a.mu.Unlock()
	content = strings.ReplaceAll(content, "@"+botID, "")
	content = strings.TrimSpace(content)

	if content == "" {
		return
	}

	event := &MessageEvent{
		Text:        content,
		MessageType: MsgText,
		MessageID:   msgID,
		Source: &SessionSource{
			Platform: PlatformQQBot,
			ChatID:   groupID,
			UserID:   userOpenID,
			ChatType: "group",
		},
		RawMessage: d,
	}

	select {
	case msgCh <- event:
	default:
		slog.Warn("[QQBot] message channel full, dropping message")
	}
}

// onDirectMessage 处理频道私信。
func (a *QQBotAdapter) onDirectMessage(d map[string]any, msgCh chan *MessageEvent) {
	author := getMap(d, "author")
	userID := getString(author, "id", "")
	guildID := getString(d, "guild_id", "")

	msgID := getString(d, "id", "")
	if a.dedup.isDuplicate(msgID) {
		return
	}

	content := getString(d, "content", "")
	if content == "" {
		return
	}

	chatID := "guild:" + guildID + ":" + userID

	event := &MessageEvent{
		Text:        content,
		MessageType: MsgText,
		MessageID:   msgID,
		Source: &SessionSource{
			Platform: PlatformQQBot,
			ChatID:   chatID,
			UserID:   userID,
			ChatType: "channel",
		},
		RawMessage: d,
	}

	select {
	case msgCh <- event:
	default:
		slog.Warn("[QQBot] message channel full, dropping message")
	}
}
