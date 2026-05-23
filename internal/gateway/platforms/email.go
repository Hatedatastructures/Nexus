// Package platforms 提供 Email 平台适配器。
// 通过 IMAP 接收邮件，SMTP 发送邮件。
package platforms

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
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

	// 已处理的 UID
	seenUIDs map[string]bool
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
		seenUIDs:       make(map[string]bool),
		threadContext:  make(map[string]*emailThreadContext),
	}
}

// ───────────────────────────── 连接生命周期 ─────────────────────────────

// Connect 启动邮件轮询。
func (a *EmailAdapter) Connect(ctx context.Context) (<-chan *MessageEvent, error) {
	if a.imapHost == "" || a.smtpHost == "" || a.emailAddress == "" || a.emailPassword == "" {
		return nil, fmt.Errorf("EMAIL_IMAP_HOST, EMAIL_SMTP_HOST, EMAIL_ADDRESS, EMAIL_PASSWORD 是必填项")
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
						msgCh <- event
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
	tag, err := a.imapCommand(conn, "LOGIN %s %s", a.emailAddress, a.emailPassword)
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
			return "", err
		}
		lines = append(lines, line)

		// 检查是否是目标 tag 的完成响应
		if strings.HasPrefix(line, expectedTag+" ") {
			status := strings.TrimPrefix(line, expectedTag+" ")
			if strings.HasPrefix(status, "OK") {
				break
			}
			if strings.HasPrefix(status, "NO") || strings.HasPrefix(status, "BAD") {
				return "", fmt.Errorf("IMAP 错误: %s", line)
			}
		}
	}
	return strings.Join(lines, "\n"), nil
}

// parseSearchResponse 解析搜索响应。
func (a *EmailAdapter) parseSearchResponse(response string) []string {
	var uids []string
	for _, line := range strings.Split(response, "\n") {
		if strings.Contains(line, "SEARCH") {
			parts := strings.Fields(line)
			for _, part := range parts {
				if part != "SEARCH" && part != "*" && !strings.HasPrefix(part, "A") {
					uids = append(uids, part)
				}
			}
		}
	}
	return uids
}

// fetchMessage 获取单个邮件。
func (a *EmailAdapter) fetchMessage(conn *textproto.Conn, uid string) (emailMessage, error) {
	// 发送 FETCH 命令
	tag, err := a.imapCommand(conn, "UID FETCH %s (BODY.PEEK[])", uid)
	if err != nil {
		return emailMessage{}, err
	}

	// 读取邮件内容
	var bodyLines []string
	inBody := false

	for {
		line, err := conn.ReadLine()
		if err != nil {
			return emailMessage{}, err
		}

		// 检查是否是目标 tag 的完成响应
		if strings.HasPrefix(line, tag+" ") {
			break
		}

		// 解析 IMAP FETCH 响应中的字节数
		// 格式: * N FETCH (BODY[] {size}
		if strings.Contains(line, "BODY[]") || strings.Contains(line, "BODY.PEEK[]") {
			inBody = true
			// 检查行尾是否有数据（某些服务器在同一行返回部分数据）
			if strings.Contains(line, "}") {
				// 找到 } 后的内容
				closeIdx := strings.Index(line, "}")
				remaining := line[closeIdx+1:]
				if len(remaining) > 0 {
					bodyLines = append(bodyLines, remaining)
				}
			}
			continue
		}

		if inBody {
			// 检查是否到达结尾 )
			if strings.TrimSpace(line) == ")" {
				break
			}
			bodyLines = append(bodyLines, line)
		}
	}

	rawBody := strings.Join(bodyLines, "\n")

	// 解析邮件
	return a.parseRawEmail(uid, rawBody)
}

// parseRawEmail 解析原始邮件。
func (a *EmailAdapter) parseRawEmail(uid, rawBody string) (emailMessage, error) {
	msg := emailMessage{UID: uid}

	// 解析邮件头和正文
	headerEnd := strings.Index(rawBody, "\r\n\r\n")
	if headerEnd == -1 {
		headerEnd = strings.Index(rawBody, "\n\n")
	}

	var headerText, bodyText string
	if headerEnd > 0 {
		headerText = rawBody[:headerEnd]
		bodyText = rawBody[headerEnd+4:]
	} else {
		bodyText = rawBody
	}

	// 解析邮件头（处理续行）
	header := mail.Header{}
	var currentKey string
	var currentValue strings.Builder

	for _, line := range strings.Split(headerText, "\n") {
		// 续行以空格或制表符开头
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			if currentKey != "" {
				currentValue.WriteString(strings.TrimSpace(line))
			}
			continue
		}

		// 保存上一个键值
		if currentKey != "" {
			header[currentKey] = []string{currentValue.String()}
		}

		// 解析新的键值
		if strings.Contains(line, ":") {
			parts := strings.SplitN(line, ": ", 2)
			if len(parts) == 2 {
				currentKey = parts[0]
				currentValue.Reset()
				currentValue.WriteString(strings.TrimSpace(parts[1]))
			} else {
				currentKey = ""
			}
		} else {
			currentKey = ""
		}
	}

	// 保存最后一个键值
	if currentKey != "" {
		header[currentKey] = []string{currentValue.String()}
	}

	// 提取字段
	msg.From = a.decodeHeader(header.Get("From"))
	msg.Subject = a.decodeHeader(header.Get("Subject"))
	msg.MessageID = header.Get("Message-ID")
	msg.InReplyTo = header.Get("In-Reply-To")

	// 解析日期
	if dateStr := header.Get("Date"); dateStr != "" {
		if t, err := mail.ParseDate(dateStr); err == nil {
			msg.Date = t
		}
	}

	// 解析正文和附件
	msg.Body, msg.Attachments = a.parseBody(bodyText, header)

	return msg, nil
}

// decodeHeader 解码邮件头（RFC 2047）。
func (a *EmailAdapter) decodeHeader(value string) string {
	// 简化处理：去除编码标记
	if strings.Contains(value, "=?") {
		// 尝试解码
		dec := new(mime.WordDecoder)
		decoded, err := dec.DecodeHeader(value)
		if err == nil {
			return decoded
		}
	}
	return value
}

// parseBody 解析邮件正文。
func (a *EmailAdapter) parseBody(bodyText string, header mail.Header) (string, []string) {
	contentType := header.Get("Content-Type")
	if contentType == "" {
		contentType = "text/plain"
	}

	mediaType, params, _ := mime.ParseMediaType(contentType)

	if strings.HasPrefix(mediaType, "multipart/") {
		return a.parseMultipart(bodyText, params["boundary"])
	}

	// 单部分正文
	if mediaType == "text/plain" {
		return a.decodeBody(bodyText, header.Get("Content-Transfer-Encoding")), nil
	}

	if mediaType == "text/html" {
		html := a.decodeBody(bodyText, header.Get("Content-Transfer-Encoding"))
		return a.stripHTMLTags(html), nil
	}

	return "", nil
}

// parseMultipart 解析多部分邮件。
func (a *EmailAdapter) parseMultipart(bodyText, boundary string) (string, []string) {
	if boundary == "" {
		return "", nil
	}

	var textParts []string
	var attachments []string

	reader := multipart.NewReader(strings.NewReader(bodyText), boundary)

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}

		contentType := part.Header.Get("Content-Type")
		mediaType, _, _ := mime.ParseMediaType(contentType)

		partBody, err := io.ReadAll(io.LimitReader(part, emailMaxBodySize))
		if err != nil {
			continue
		}

		encoding := part.Header.Get("Content-Transfer-Encoding")
		decoded := a.decodeBody(string(partBody), encoding)

		if mediaType == "text/plain" {
			textParts = append(textParts, decoded)
		} else if mediaType == "text/html" && len(textParts) == 0 {
			textParts = append(textParts, a.stripHTMLTags(decoded))
		} else if strings.HasPrefix(mediaType, "image/") || strings.HasPrefix(mediaType, "application/") {
			// 附件处理（简化：只记录类型）
			attachments = append(attachments, mediaType)
		}
	}

	return strings.Join(textParts, "\n"), attachments
}

// decodeBody 解码正文。
func (a *EmailAdapter) decodeBody(body, encoding string) string {
	switch strings.ToLower(encoding) {
	case "quoted-printable":
		decoded, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(body)))
		if err == nil {
			return string(decoded)
		}
	case "base64":
		decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(body, "\r\n", ""))
		if err == nil {
			return string(decoded)
		}
	}
	return body
}

// stripHTMLTags 去除 HTML 标签。
func (a *EmailAdapter) stripHTMLTags(html string) string {
	// 简化处理
	var result strings.Builder
	inTag := false
	for _, c := range html {
		if c == '<' {
			inTag = true
			continue
		}
		if c == '>' {
			inTag = false
			continue
		}
		if !inTag {
			result.WriteRune(c)
		}
	}
	return result.String()
}

// ───────────────────────────── 已处理检查 ─────────────────────────────

func (a *EmailAdapter) isSeen(uid string) bool {
	a.seenMu.Lock()
	defer a.seenMu.Unlock()
	return a.seenUIDs[uid]
}

func (a *EmailAdapter) markSeen(uid string) {
	a.seenMu.Lock()
	defer a.seenMu.Unlock()

	a.seenUIDs[uid] = true

	// 清理超过上限的条目
	if len(a.seenUIDs) > emailMaxSeenUIDs {
		// 删除部分旧条目
		count := 0
		for key := range a.seenUIDs {
			delete(a.seenUIDs, key)
			count++
			if count >= emailMaxSeenUIDs/2 {
				break
			}
		}
	}
}

// isAutoReply 检查是否是自动回复。
func (a *EmailAdapter) isAutoReply(from string) bool {
	fromLower := strings.ToLower(from)
	for _, pattern := range emailAutoReplyPatterns {
		if strings.Contains(fromLower, pattern) {
			return true
		}
	}
	return false
}

// ───────────────────────────── 消息解析 ─────────────────────────────

// parseMessage 转换为 MessageEvent。
func (a *EmailAdapter) parseMessage(msg emailMessage) *MessageEvent {
	if msg.Body == "" {
		return nil
	}

	// 存储线程上下文（使用 Message-ID 作为 key，便于回复）
	if msg.MessageID != "" {
		a.setThreadContext(msg.MessageID, msg.Subject, msg.MessageID, msg.InReplyTo)
	}

	return &MessageEvent{
		Text:        msg.Body,
		MessageType: MsgText,
		MessageID:   msg.MessageID,
		Source: &SessionSource{
			Platform: PlatformEmail,
			ChatID:   msg.From,
			UserID:   msg.From,
			ChatType: "dm",
		},
		RawMessage: map[string]any{
			"uid":         msg.UID,
			"from":        msg.From,
			"subject":     msg.Subject,
			"message_id":  msg.MessageID,
			"in_reply_to": msg.InReplyTo,
			"date":        msg.Date,
		},
	}
}

func (a *EmailAdapter) setThreadContext(chatID, subject, messageID, inReplyTo string) {
	a.threadMu.Lock()
	defer a.threadMu.Unlock()
	a.threadContext[chatID] = &emailThreadContext{
		Subject:   subject,
		MessageID: messageID,
		InReplyTo: inReplyTo,
	}
}

func (a *EmailAdapter) getThreadContext(chatID string) *emailThreadContext {
	a.threadMu.Lock()
	defer a.threadMu.Unlock()
	return a.threadContext[chatID]
}

// ───────────────────────────── 发送消息 ─────────────────────────────

// Send 发送邮件。
func (a *EmailAdapter) Send(ctx context.Context, chatID string, content string, opts *SendOptions) (*SendResult, error) {
	if chatID == "" {
		return &SendResult{Success: false, Error: "收件人地址是必填项"}, nil
	}

	// 获取线程上下文
	threadCtx := a.getThreadContext(chatID)

	// 构建邮件
	var subject string
	var refs []string

	if threadCtx != nil {
		// 回复邮件
		subject = "Re: " + threadCtx.Subject
		if threadCtx.MessageID != "" {
			refs = []string{threadCtx.MessageID}
		}
	} else {
		// 新邮件
		subject = "AI Bot Response"
	}

	// 构建邮件正文
	from := a.emailAddress
	to := chatID

	// 构建邮件头
	headers := map[string]string{
		"From":    from,
		"To":      to,
		"Subject": subject,
		"Date":    time.Now().Format(time.RFC1123Z),
		"MIME-Version": "1.0",
		"Content-Type": "text/plain; charset=UTF-8",
		"Content-Transfer-Encoding": "quoted-printable",
	}

	if len(refs) > 0 {
		headers["In-Reply-To"] = refs[0]
		headers["References"] = strings.Join(refs, " ")
	}

	// 编码正文
	var qpWriter bytes.Buffer
	qpEnc := quotedprintable.NewWriter(&qpWriter)
	qpEnc.Write([]byte(content))
	qpEnc.Close()
	encodedBody := qpWriter.String()

	// 构建完整邮件
	var msgBuilder strings.Builder
	for k, v := range headers {
		msgBuilder.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
	}
	msgBuilder.WriteString("\r\n")
	msgBuilder.WriteString(encodedBody)

	// 发送邮件
	smtpHost := a.smtpHost
	if !strings.Contains(smtpHost, ":") {
		smtpHost = smtpHost + ":587"
	}

	// 使用 STARTTLS
	client, err := smtp.Dial(smtpHost)
	if err != nil {
		return &SendResult{Success: false, Error: fmt.Sprintf("连接 SMTP 失败: %v", err)}, nil
	}

	// 启用 STARTTLS
	if err := client.StartTLS(&tls.Config{
		ServerName: strings.Split(smtpHost, ":")[0],
	}); err != nil {
		client.Close()
		return &SendResult{Success: false, Error: fmt.Sprintf("STARTTLS 失败: %v", err)}, nil
	}

	// 认证
	if err := client.Auth(a.smtpAuth); err != nil {
		client.Close()
		return &SendResult{Success: false, Error: fmt.Sprintf("认证失败: %v", err)}, nil
	}

	// 发送
	if err := client.Mail(from); err != nil {
		client.Close()
		return &SendResult{Success: false, Error: fmt.Sprintf("设置发件人失败: %v", err)}, nil
	}

	if err := client.Rcpt(to); err != nil {
		client.Close()
		return &SendResult{Success: false, Error: fmt.Sprintf("设置收件人失败: %v", err)}, nil
	}

	wc, err := client.Data()
	if err != nil {
		client.Close()
		return &SendResult{Success: false, Error: fmt.Sprintf("准备发送失败: %v", err)}, nil
	}

	_, err = wc.Write([]byte(msgBuilder.String()))
	if err != nil {
		wc.Close()
		client.Close()
		return &SendResult{Success: false, Error: fmt.Sprintf("发送失败: %v", err)}, nil
	}

	wc.Close()
	client.Quit()

	return &SendResult{Success: true}, nil
}

// SendImage 发送图片（邮件附件）。
func (a *EmailAdapter) SendImage(ctx context.Context, chatID string, imageURL string, caption string, opts *SendOptions) (*SendResult, error) {
	// Email 不支持直接发送图片 URL，简化为发送文本
	text := caption
	if text == "" {
		text = imageURL
	} else {
		text = text + "\n" + imageURL
	}
	return a.Send(ctx, chatID, text, opts)
}

// SendTyping 发送正在输入指示（Email 不支持）。
func (a *EmailAdapter) SendTyping(ctx context.Context, chatID string) error {
	return nil
}

// ───────────────────────────── 接口实现 ─────────────────────────────

// Name 返回适配器名称。
func (a *EmailAdapter) Name() string { return "Email" }

// PlatformType 返回平台类型。
func (a *EmailAdapter) PlatformType() Platform { return PlatformEmail }

// EditMessage 编辑消息（Email 不支持）。
func (a *EmailAdapter) EditMessage(ctx context.Context, chatID string, messageID string, content string) (*SendResult, error) {
	return &SendResult{Success: false, Error: "Email 不支持编辑消息"}, nil
}

// DeleteMessage 删除消息（Email 不支持）。
func (a *EmailAdapter) DeleteMessage(ctx context.Context, chatID string, messageID string) error {
	return fmt.Errorf("Email 不支持删除消息")
}

// SendVoice 发送语音（Email 不支持）。
func (a *EmailAdapter) SendVoice(ctx context.Context, chatID string, audioPath string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "Email 不支持发送语音"}, nil
}

// SendVideo 发送视频（Email 不支持）。
func (a *EmailAdapter) SendVideo(ctx context.Context, chatID string, videoPath string, caption string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "Email 不支持发送视频"}, nil
}

// SendDocument 发送文件（Email 不支持）。
func (a *EmailAdapter) SendDocument(ctx context.Context, chatID string, filePath string, caption string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "Email 文件发送需要附件上传"}, nil
}

// MaxMessageLength 返回最大消息长度。
func (a *EmailAdapter) MaxMessageLength() int { return 100000 }

// SupportsStreaming 返回是否支持流式输出。
func (a *EmailAdapter) SupportsStreaming() bool { return false }

// ───────────────────────────── 自注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&AdapterEntry{
		Platform: PlatformEmail,
		Name:     "Email",
		Factory:  func() PlatformAdapter { return NewEmailAdapter(nil) },
	})
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

// parseEmailInt 解析整数字符串（email 专用）。
func parseEmailInt(s string) int {
	var result int
	for _, c := range strings.TrimSpace(s) {
		if c >= '0' && c <= '9' {
			result = result * 10 + int(c - '0')
		} else {
			break
		}
	}
	return result
}