// Package platforms 提供 Email 平台适配器。
// 通过 IMAP 接收邮件，SMTP 发送邮件。
package platforms

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"log/slog"
	"math/big"
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
	emailMaxThreadContext    = 1000
	emailMaxResponseLines    = 5000
)

// 自动过滤的发件人模式
var emailAutoReplyPatterns = []string{
	"noreply@", "no-reply@", "automated@", "auto@", "bot@", "notification@", "alert@", "daemon@",
}

// ───────────────────────────── EmailAdapter ─────────────────────────────

// EmailAdapter Email 平台适配器。
type EmailAdapter struct {
	imapHost       string
	smtpHost       string
	emailAddress   string
	emailPassword  string
	pollInterval   time.Duration
	messageHandler func(*MessageEvent)

	// IMAP 客户端（使用 net/textproto 模拟）
	imapConn *textproto.Conn
	smtpAuth smtp.Auth

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
		_ = a.imapConn.Close()
		a.imapConn = nil
	}

	// pollLoop 退出时负责关闭 msgCh，这里不再重复关闭
	slog.Info("[Email] disconnected")
	return nil
}

// ───────────────────────────── 轮询循环 ─────────────────────────────

// pollLoop 轮询邮件。
func (a *EmailAdapter) pollLoop(ctx context.Context, msgCh chan *MessageEvent) {
	defer a.closeOnce.Do(func() { close(msgCh) })

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
		}
	}
}

// ───────────────────────────── IMAP 操作 ─────────────────────────────

// emailMessage 邮件消息。
type emailMessage struct {
	UID         string
	From        string
	Subject     string
	MessageID   string
	InReplyTo   string
	Body        string
	Attachments []string
	Date        time.Time
}

// fetchUnseen 获取未读邮件。
func (a *EmailAdapter) fetchUnseen(ctx context.Context) ([]emailMessage, error) {
	// 连接 IMAP 服务器
	conn, err := a.connectIMAP()
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

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
		_ = conn.Close()
		return nil, fmt.Errorf("读取问候失败: %w", err)
	}

	// 登录
	sanitizedAddr := sanitizeIMAPString(a.emailAddress)
	sanitizedPass := sanitizeIMAPString(a.emailPassword)
	tag, err := a.imapCommand(conn, "LOGIN \"%s\" \"%s\"", sanitizedAddr, sanitizedPass)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("发送登录命令失败: %w", err)
	}
	if _, err := a.imapReadResponse(conn, tag); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("登录失败: %w", err)
	}

	return conn, nil
}

// imapCommand 发送 IMAP 命令并返回 tag。
func (a *EmailAdapter) imapCommand(conn *textproto.Conn, format string, args ...any) (string, error) {
	randNum, err := rand.Int(rand.Reader, big.NewInt(10000))
	if err != nil {
		return "", fmt.Errorf("生成 IMAP tag 失败: %w", err)
	}
	tag := fmt.Sprintf("A%04d", randNum.Int64())
	cmd := fmt.Sprintf(format, args...)
	if err := conn.PrintfLine("%s %s", tag, cmd); err != nil {
		return "", err
	}
	return tag, nil
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

		if len(lines) > emailMaxResponseLines {
			return strings.Join(lines, "\n"), fmt.Errorf("IMAP 响应行数超过限制 (%d)", emailMaxResponseLines)
		}

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
