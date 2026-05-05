// Package platforms 提供 Twilio SMS 短信适配器。
package platforms

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// ───────────────────────────── 常量 ─────────────────────────────

const (
	smsRequestTimeout = 15 * time.Second
	smsMaxMessageLen  = 160
)

// ───────────────────────────── SMSAdapter ─────────────────────────────

// SMSAdapter Twilio SMS 短信适配器。
type SMSAdapter struct {
	accountSID  string
	authToken   string
	fromNumber  string
	messageHandler func(*MessageEvent)
	httpClient  *http.Client
	msgCh       chan *MessageEvent
	running     bool
	webhookPort int
}

// NewSMSAdapter 创建 SMS 适配器。
func NewSMSAdapter(messageHandler func(*MessageEvent)) *SMSAdapter {
	accountSID := os.Getenv("TWILIO_ACCOUNT_SID")
	authToken := os.Getenv("TWILIO_AUTH_TOKEN")
	fromNumber := os.Getenv("TWILIO_FROM_NUMBER")

	webhookPort := 8082
	if p := os.Getenv("SMS_WEBHOOK_PORT"); p != "" {
		fmt.Sscanf(p, "%d", &webhookPort)
	}

	return &SMSAdapter{
		accountSID:     accountSID,
		authToken:      authToken,
		fromNumber:     fromNumber,
		messageHandler: messageHandler,
		httpClient:     &http.Client{Timeout: smsRequestTimeout},
		webhookPort:    webhookPort,
	}
}

// ───────────────────────────── PlatformAdapter 接口实现 ─────────────────────────────

func (a *SMSAdapter) Name() string          { return "SMS" }
func (a *SMSAdapter) PlatformType() Platform { return PlatformSMS }
func (a *SMSAdapter) MaxMessageLength() int  { return smsMaxMessageLen }
func (a *SMSAdapter) SupportsStreaming() bool { return false }

func (a *SMSAdapter) Connect(ctx context.Context) (<-chan *MessageEvent, error) {
	if a.accountSID == "" || a.authToken == "" {
		return nil, fmt.Errorf("TWILIO_ACCOUNT_SID 和 TWILIO_AUTH_TOKEN 是必填项")
	}

	a.msgCh = make(chan *MessageEvent, 100)
	a.running = true

	// 启动 webhook 服务器接收入站短信
	mux := http.NewServeMux()
	mux.HandleFunc("/sms/webhook", a.handleWebhook)

	go func() {
		addr := fmt.Sprintf(":%d", a.webhookPort)
		slog.Info("[SMS] 启动 webhook 服务器", "addr", addr)
		if err := http.ListenAndServe(addr, mux); err != nil {
			slog.Error("[SMS] webhook 服务器失败", "err", err)
		}
	}()

	slog.Info("[SMS] 已连接", "from", a.fromNumber)
	return a.msgCh, nil
}

func (a *SMSAdapter) Disconnect(ctx context.Context) error {
	a.running = false
	slog.Info("[SMS] 已断开")
	return nil
}

func (a *SMSAdapter) Send(ctx context.Context, chatID string, content string, opts *SendOptions) (*SendResult, error) {
	// chatID 是手机号码
	if !isValidPhoneNumber(chatID) {
		return &SendResult{Success: false, Error: "无效的手机号码"}, nil
	}

	// 分割长消息
	if len(content) > smsMaxMessageLen {
		content = content[:smsMaxMessageLen]
	}

	// 调用 Twilio API
	apiURL := fmt.Sprintf("https://api.twilio.com/2010-04-01/Accounts/%s/Messages.json", a.accountSID)

	data := url.Values{}
	data.Set("To", chatID)
	data.Set("From", a.fromNumber)
	data.Set("Body", content)

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, strings.NewReader(data.Encode()))
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, nil
	}

	req.SetBasicAuth(a.accountSID, a.authToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return &SendResult{Success: false, Error: fmt.Sprintf("Twilio API 错误: %s", string(body))}, nil
	}

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)

	messageSID := ""
	if sid, ok := result["sid"].(string); ok {
		messageSID = sid
	}

	return &SendResult{Success: true, MessageID: messageSID}, nil
}

func (a *SMSAdapter) EditMessage(ctx context.Context, chatID string, messageID string, content string) (*SendResult, error) {
	return &SendResult{Success: false, Error: "SMS 不支持编辑消息"}, nil
}

func (a *SMSAdapter) DeleteMessage(ctx context.Context, chatID string, messageID string) error {
	return fmt.Errorf("SMS 不支持删除消息")
}

func (a *SMSAdapter) SendTyping(ctx context.Context, chatID string) error {
	return nil
}

func (a *SMSAdapter) SendImage(ctx context.Context, chatID string, imageURL string, caption string, opts *SendOptions) (*SendResult, error) {
	// SMS 不支持图片，发送 URL
	text := caption
	if text == "" {
		text = imageURL
	}
	return a.Send(ctx, chatID, text, opts)
}

func (a *SMSAdapter) SendVoice(ctx context.Context, chatID string, audioPath string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "SMS 不支持语音"}, nil
}

func (a *SMSAdapter) SendVideo(ctx context.Context, chatID string, videoPath string, caption string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "SMS 不支持视频"}, nil
}

func (a *SMSAdapter) SendDocument(ctx context.Context, chatID string, filePath string, caption string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "SMS 不支持文件"}, nil
}

// ───────────────────────────── Webhook 处理 ─────────────────────────────

func (a *SMSAdapter) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "解析表单失败", http.StatusBadRequest)
		return
	}

	from := r.FormValue("From")
	body := r.FormValue("Body")

	if from == "" || body == "" {
		http.Error(w, "缺少必要参数", http.StatusBadRequest)
		return
	}

	// 发送消息到 Agent
	msgEvent := &MessageEvent{
		Text:        body,
		MessageType: MsgText,
		MessageID:   r.FormValue("MessageSid"),
		Source: &SessionSource{
			Platform: PlatformSMS,
			ChatID:   from,
			UserID:   from,
			ChatType: "dm",
		},
	}

	a.msgCh <- msgEvent

	// 返回 TwiML 响应
	w.Header().Set("Content-Type", "text/xml")
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><Response></Response>`)
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

func isValidPhoneNumber(phone string) bool {
	// 简单验证 E.164 格式
	return len(phone) >= 10 && len(phone) <= 15 && phone[0] == '+'
}

// ───────────────────────────── 自注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&AdapterEntry{
		Platform: PlatformSMS,
		Name:     "SMS",
		Factory:  func() PlatformAdapter { return NewSMSAdapter(nil) },
	})
}
