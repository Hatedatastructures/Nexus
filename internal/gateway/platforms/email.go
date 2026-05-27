// Package platforms 提供 Email 平台适配器。
// 通过 IMAP 接收邮件，SMTP 发送邮件。
package platforms

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/smtp"
	"net/textproto"
	"os"
	"strings"
	"sync"
	"time"
)

// ───────────────────────────── 常量 ─────────────────────────────

const (
	emailDefaultPollInterval = 15 * time.Second
	emailMaxSeenUIDs         = 2000
	emailMaxBodySize         = 1024 * 1024 // 1MB
)

// 自动过滤的发件人模式
var emailAutoReplyPatterns = []string{
	"noreply@", "no-reply@", "automated@", "auto@", "bot@", "notification@", "alert@", "daemon@",
}

// ───────────────────────────── EmailAdapter ─────────────────────────────

// EmailAdapter Email 平台适配器。
type EmailAdapter struct {
	imapHost     string
	smtpHost     string
	emailAddress string
	emailPassword string
	pollInterval time.Duration
	messageHandler func(*MessageEvent)

	// IMAP 客户端（使用 net/textproto 模拟）
	imapConn     *textproto.Conn
	smtpAuth     smtp.Auth

	// 运行状态
	running   bool
	connected bool
	mu        sync.Mutex

	// 已处理的 UID (map uid -> 序号，用于 LRU 淘汰)
	seenUIDs map[string]int64
	seenSeq  int64
	seenMu   sync.Mutex

	// Thread 上下文（用于回复）
	threadContext map[string]*emailThreadContext
	threadMu      sync.Mutex

	closeOnce sync.Once
	msgCh     chan *MessageEvent
}

// emailThreadContext 线程上下文。
type emailThreadContext struct {
	Subject   string
	MessageID string
	InReplyTo string
}

// NewEmailAdapter 创建 Email 适配器。
func NewEmailAdapter(messageHandler func(*MessageEvent)) *EmailAdapter {
	imapHost := os.Getenv("EMAIL_IMAP_HOST")
	smtpHost := os.Getenv("EMAIL_SMTP_HOST")
	emailAddress := os.Getenv("EMAIL_ADDRESS")
	emailPassword := os.Getenv("EMAIL_PASSWORD")

	pollInterval := emailDefaultPollInterval
	if p := os.Getenv("EMAIL_POLL_INTERVAL"); p != "" {
		if parsed, err := time.ParseDuration(p); err == nil && parsed > 0 {
			pollInterval = parsed
		}
	}

	return &EmailAdapter{
		imapHost:       imapHost,
		smtpHost:       smtpHost,
		emailAddress:   emailAddress,
		emailPassword:  emailPassword,
		pollInterval:   pollInterval,
		messageHandler: messageHandler,
		seenUIDs:       make(map[string]int64),
		threadContext:  make(map[string]*emailThreadContext),
	}
}

// ───────────────────────────── 连接生命周期 ─────────────────────────────

// Connect 启动邮件轮询。
func (a *EmailAdapter) Connect(ctx context.Context) (<-chan *MessageEvent, error) {
	if a.imapHost == "" || a.smtpHost == "" || a.emailAddress == "" || a.emailPassword == "" {
		return nil, fmt.Errorf("EMAIL_IMAP_HOST, EMAIL_SMTP_HOST, EMAIL_ADDRESS, EMAIL_PASSWORD 是必填项")
	}

	// 验证 IMAP 参数安全性
	if strings.ContainsAny(a.emailAddress, "\r\n\"\\") {
		return nil, fmt.Errorf("EMAIL_ADDRESS 包含非法字符")
	}
	if strings.ContainsAny(a.emailPassword, "\r\n\"\\") {
		return nil, fmt.Errorf("EMAIL_PASSWORD 包含非法字符")
	}

	a.mu.Lock()
	a.running = true
	a.connected = true
	a.mu.Unlock()

	// 设置 SMTP 认证
	a.smtpAuth = smtp.PlainAuth("", a.emailAddress, a.emailPassword, strings.Split(a.smtpHost, ":")[0])

	// 创建消息通道
	a.msgCh = make(chan *MessageEvent, 100)

	// 启动轮询循环
	go a.pollLoop(ctx, a.msgCh)

	slog.Info("[Email] connected", "address", a.emailAddress)
	return a.msgCh, nil
}

// Disconnect 断开连接。
func (a *EmailAdapter) Disconnect(ctx context.Context) error {
	a.mu.Lock()
	a.running = false
	a.connected = false
	a.mu.Unlock()

	if a.imapConn != nil {
		a.imapConn.Close()
		a.imapConn = nil
	}

	a.closeOnce.Do(func() { close(a.msgCh) })
	slog.Info("[Email] disconnected")
	return nil
}

// ───────────────────────────── 轮询循环 ─────────────────────────────

// pollLoop 轮询邮件。
func (a *EmailAdapter) pollLoop(ctx context.Context, msgCh chan *MessageEvent) {
	ticker := time.NewTicker(a.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.mu.Lock()
			running := a.running
			a.mu.Unlock()

			if !running {
				// msgCh 由 Disconnect 通过 closeOnce 关闭
				return
			}

			messages, err := a.fetchUnseen(ctx)
			if err != nil {
				slog.Warn("[Email] failed to fetch emails", "err", err)
				continue
			}

			for _, msg := range messages {
				if !a.isAutoReply(msg.From) {
					event := a.parseMessage(msg)
					if event != nil {
						select {
						case msgCh <- event:
						default:
							slog.Warn("[Email] message channel full, dropping message")
						}

					}
				}
			}
		case <-ctx.Done():
			return
		}
	}
}

// ───────────────────────────── IMAP 操作 ─────────────────────────────

// emailMessage 邮件消息。
type emailMessage struct {
	UID       string
	From      string
	Subject   string
	MessageID string
	InReplyTo string
	Body      string
	Attachments []string
	Date      time.Time
}

// fetchUnseen 获取未读邮件。
func (a *EmailAdapter) fetchUnseen(ctx context.Context) ([]emailMessage, error) {
	// 连接 IMAP 服务器
	conn, err := a.connectIMAP()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	// 选择 INBOX
	tag, err := a.imapCommand(conn, "SELECT INBOX")
	if err != nil {
		return nil, err
	}
	if _, err := a.imapReadResponse(conn, tag); err != nil {
		return nil, err
	}

	// 搜索未读邮件
	tag, err = a.imapCommand(conn, "SEARCH UNSEEN")
	if err != nil {
		return nil, err
	}

	// 获取搜索结果
	response, err := a.imapReadResponse(conn, tag)
	if err != nil {
		return nil, err
	}

	// 解析 UID 列表
	uids := a.parseSearchResponse(response)
	if len(uids) == 0 {
		return nil, nil
	}

	// 获取邮件内容
	var messages []emailMessage
	for _, uid := range uids {
		// 检查是否已处理
		if a.isSeen(uid) {
			continue
		}

		msg, err := a.fetchMessage(conn, uid)
		if err != nil {
			slog.Warn("[Email] failed to fetch email", "uid", uid, "err", err)
			continue
		}

		messages = append(messages, msg)
		a.markSeen(uid)
	}

	return messages, nil
}

// connectIMAP 连接 IMAP 服务器。
func (a *EmailAdapter) connectIMAP() (*textproto.Conn, error) {
	host := a.imapHost
	if !strings.Contains(host, ":") {
		host = host + ":993"
	}

	// 使用 TLS 连接
	tlsConn, err := tls.Dial("tcp", host, &tls.Config{
		InsecureSkipVerify: false,
	})
	if err != nil {
		return nil, fmt.Errorf("连接 IMAP 失败: %w", err)
	}

	conn := textproto.NewConn(tlsConn)

	// 读取问候
	_, err = conn.ReadLine()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("读取问候失败: %w", err)
	}

	// 登录
	tag, err := a.imapCommand(conn, "LOGIN \"%s\" \"%s\"", a.emailAddress, a.emailPassword)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("发送登录命令失败: %w", err)
	}
	if _, err := a.imapReadResponse(conn, tag); err != nil {
		conn.Close()
		return nil, fmt.Errorf("登录失败: %w", err)
	}

	return conn, nil
}

// imapCommand 发送 IMAP 命令并返回 tag。
func (a *EmailAdapter) imapCommand(conn *textproto.Conn, format string, args ...any) (string, error) {
	tag := fmt.Sprintf("A%04d", time.Now().UnixNano()%10000)
	cmd := fmt.Sprintf(format, args...)
	// 使用 PrintfLine 发送命令
	err := conn.PrintfLine("%s %s", tag, cmd)
	return tag, err
}

// imapReadResponse 读取 IMAP 响应，等待指定 tag 的完成响应。
func (a *EmailAdapter) imapReadResponse(conn *textproto.Conn, expectedTag string) (string, error) {
	var lines []string
	for {
		line, err := conn.ReadLine()
		if err != nil {
			return strings.Join(lines, "\n"), fmt.Errorf("读取 IMAP 响应失败: %w", err)
		}

		lines = append(lines, line)

		// 检查是否为完成响应 (tag + OK/NO/BAD)
		if strings.HasPrefix(line, expectedTag+" ") {
			status := line[len(expectedTag)+1:]
			if strings.HasPrefix(status, "OK") {
				return strings.Join(lines, "\n"), nil
			}
			if strings.HasPrefix(status, "NO") || strings.HasPrefix(status, "BAD") {
				return strings.Join(lines, "\n"), fmt.Errorf("IMAP 命令失败: %s", line)
			}
		}
	}
}

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
		a.threadMu.Unlock()
	}

	// 截断过长的邮件正文
	body := msg.Body
	if len(body) > emailMaxBodySize {
		body = body[:emailMaxBodySize]
	}

	return &MessageEvent{
		Text:    body,
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
