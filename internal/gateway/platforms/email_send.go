// Package platforms 提供 Email 平台适配器。
// 通过 IMAP 接收邮件，SMTP 发送邮件。
package platforms

import (
	"fmt"
	"net/textproto"
	"strings"
	"time"
	"unicode/utf8"
)

// ───────────────────────────── 辅助方法 ─────────────────────────────

// isAutoReply 检查发件人是否为自动回复。
func (a *EmailAdapter) isAutoReply(from string) bool {
	fromLower := strings.ToLower(from)
	for _, pattern := range emailAutoReplyPatterns {
		if strings.Contains(fromLower, pattern) {
			return true
		}
	}
	return false
}

// parseMessage 将 emailMessage 转换为 MessageEvent。
func (a *EmailAdapter) parseMessage(msg emailMessage) *MessageEvent {
	// 存储线程上下文（用于回复）
	if msg.MessageID != "" {
		a.threadMu.Lock()
		a.threadContext[msg.From] = &emailThreadContext{
			Subject:   msg.Subject,
			MessageID: msg.MessageID,
			InReplyTo: msg.InReplyTo,
		}
		if len(a.threadContext) > emailMaxThreadContext {
			for k := range a.threadContext {
				delete(a.threadContext, k)
				break
			}
		}
		a.threadMu.Unlock()
	}

	// 截断过长的邮件正文（rune 安全）
	body := msg.Body
	if utf8.RuneCountInString(body) > emailMaxBodySize {
		runes := []rune(body)
		body = string(runes[:min(len(runes), emailMaxBodySize)])
	}

	return &MessageEvent{
		Text: body,
		Source: &SessionSource{
			Platform: PlatformEmail,
			ChatID:   a.emailAddress,
			ChatName: a.emailAddress,
			ChatType: "dm",
			UserID:   msg.From,
			UserName: msg.From,
		},
		MessageID:    msg.UID,
		ReplyToMsgID: msg.InReplyTo,
		ThreadID:     msg.MessageID,
		RawMessage:   msg,
		Timestamp:    msg.Date,
	}
}

// parseSearchResponse 解析 IMAP SEARCH 响应，提取 UID 列表。
func (a *EmailAdapter) parseSearchResponse(response string) []string {
	// IMAP SEARCH 响应格式: * SEARCH 1 2 3 ...
	var uids []string
	for _, line := range strings.Split(response, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "* SEARCH") {
			continue
		}
		parts := strings.Fields(line)
		for _, p := range parts[2:] { // 跳过 "* SEARCH"
			if len(p) > 0 {
				uids = append(uids, p)
			}
		}
	}
	return uids
}

// isSeen 检查 UID 是否已处理。
func (a *EmailAdapter) isSeen(uid string) bool {
	a.seenMu.Lock()
	defer a.seenMu.Unlock()

	if _, ok := a.seenUIDs[uid]; ok {
		return true
	}

	// 超出容量时淘汰最旧的一半记录
	if len(a.seenUIDs) >= emailMaxSeenUIDs {
		threshold := a.seenSeq - int64(emailMaxSeenUIDs/2)
		for k, seq := range a.seenUIDs {
			if seq < threshold {
				delete(a.seenUIDs, k)
			}
		}
	}

	return false
}

// markSeen 标记 UID 为已处理。
func (a *EmailAdapter) markSeen(uid string) {
	a.seenMu.Lock()
	defer a.seenMu.Unlock()
	a.seenSeq++
	a.seenUIDs[uid] = a.seenSeq
}

// fetchMessage 获取指定 UID 的邮件内容。
func (a *EmailAdapter) fetchMessage(conn *textproto.Conn, uid string) (emailMessage, error) {
	var msg emailMessage
	msg.UID = uid

	// 验证 UID 仅包含数字，防止 IMAP 注入
	for _, r := range uid {
		if r < '0' || r > '9' {
			return msg, fmt.Errorf("非法 UID 字符: %q", r)
		}
	}

	// 获取邮件头部和正文
	tag, err := a.imapCommand(conn, "FETCH %s (BODY.PEEK[])", uid)
	if err != nil {
		return msg, fmt.Errorf("发送 FETCH 命令失败: %w", err)
	}

	response, err := a.imapReadResponse(conn, tag)
	if err != nil {
		return msg, fmt.Errorf("读取 FETCH 响应失败: %w", err)
	}

	// 简易解析邮件内容
	// 提取 From
	if fromIdx := strings.Index(response, "From: "); fromIdx != -1 {
		end := strings.Index(response[fromIdx:], "\n")
		if end != -1 {
			msg.From = strings.TrimSpace(response[fromIdx+6 : fromIdx+end])
		}
	}

	// 提取 Subject
	if subjIdx := strings.Index(response, "Subject: "); subjIdx != -1 {
		end := strings.Index(response[subjIdx:], "\n")
		if end != -1 {
			msg.Subject = strings.TrimSpace(response[subjIdx+9 : subjIdx+end])
		}
	}

	// 提取 Message-ID
	if midIdx := strings.Index(response, "Message-ID: "); midIdx != -1 {
		end := strings.Index(response[midIdx:], "\n")
		if end != -1 {
			msg.MessageID = strings.TrimSpace(response[midIdx+12 : midIdx+end])
		}
	}

	// 提取 In-Reply-To
	if irtIdx := strings.Index(response, "In-Reply-To: "); irtIdx != -1 {
		end := strings.Index(response[irtIdx:], "\n")
		if end != -1 {
			msg.InReplyTo = strings.TrimSpace(response[irtIdx+13 : irtIdx+end])
		}
	}

	// 提取正文（简化：空行之后的内容）
	if bodyStart := strings.Index(response, "\r\n\r\n"); bodyStart != -1 {
		msg.Body = response[bodyStart+4:]
	} else if bodyStart := strings.Index(response, "\n\n"); bodyStart != -1 {
		msg.Body = response[bodyStart+2:]
	}

	msg.Date = time.Now()

	return msg, nil
}

// sanitizeIMAPString 清洗 IMAP 协议字符串，移除注入风险的字符。
func sanitizeIMAPString(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return s
}
