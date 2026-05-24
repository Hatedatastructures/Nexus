// 参考文档: https://open.feishu.cn/document/server-docs/im-v1/message/create
// 飞书适配器使用事件订阅 (Event Subscriptions) 接收消息,
// 通过消息 API (im/v1/messages) 发送和回复消息。
package platforms

import (
	"sync"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// ───────────────────────────── 飞书适配器 ─────────────────────────────

// FeishuAdapter 实现飞书开放平台适配器。
// 使用 tenant_access_token 认证, 通过事件订阅接收消息, 通过消息 API 发送消息。
type FeishuAdapter struct {
	tokenMu           sync.Mutex         // token 访问锁
	verificationToken string             // 事件订阅验证令牌
	closeOnce         sync.Once          // 确保 msgCh 只关闭一次
	appID       string             // 应用 App ID
	appSecret   string             // 应用 App Secret
	client      *http.Client       // HTTP 客户端
	tenantToken string             // 缓存的 tenant_access_token
	tokenExpiry time.Time          // token 过期时间
	msgCh       chan *MessageEvent // 入站消息通道
}

// NewFeishuAdapter 创建飞书适配器。
// appID 为应用 App ID, appSecret 为应用 App Secret。
func NewFeishuAdapter(appID string, appSecret string) *FeishuAdapter {
	return &FeishuAdapter{
		appID:     appID,
		appSecret: appSecret,
		client:    &http.Client{Timeout: 30 * time.Second},
		msgCh:     make(chan *MessageEvent, 128),
	}
}

// Name 返回平台名称。
func (f *FeishuAdapter) Name() string { return "Feishu" }

// PlatformType 返回平台类型枚举。
func (f *FeishuAdapter) PlatformType() Platform { return PlatformFeishu }

// MaxMessageLength 飞书消息最大长度 30000 字符。
func (f *FeishuAdapter) MaxMessageLength() int { return 30000 }

// SupportsStreaming 飞书支持消息编辑 (通过更新卡片)。
func (f *FeishuAdapter) SupportsStreaming() bool { return true }

// Connect 建立连接。
func (f *FeishuAdapter) Connect(ctx context.Context) (<-chan *MessageEvent, error) {
	// 预先获取 token
	if _, err := f.getTenantToken(ctx); err != nil {
		slog.Warn("feishu: failed to get initial token", "err", err)
	}
	slog.Info("feishu adapter connected (event subscription mode)")
	return f.msgCh, nil
}

// Disconnect 关闭消息通道。
func (f *FeishuAdapter) Disconnect(ctx context.Context) error {
	f.closeOnce.Do(func() {
		close(f.msgCh)
	})
	slog.Info("feishu adapter disconnected")
	return nil
}

// Send 发送文本消息。
func (f *FeishuAdapter) Send(ctx context.Context, chatID string, content string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"receive_id": chatID,
		"msg_type":   "text",
		"content":    fmt.Sprintf(`{"text":"%s"}`, escapeJSON(content)),
	}
	return f.doAPI(ctx, "POST", "/open-apis/im/v1/messages?receive_id_type=chat_id", body)
}

// EditMessage 编辑已发送的消息 (飞书卡片更新)。
func (f *FeishuAdapter) EditMessage(ctx context.Context, chatID string, messageID string, content string) (*SendResult, error) {
	// 飞书通过更新消息卡片来编辑
	body := map[string]any{
		"content": fmt.Sprintf(`{"text":"%s"}`, escapeJSON(content)),
	}
	return f.doAPI(ctx, "PATCH", "/open-apis/im/v1/messages/"+messageID, body)
}

// DeleteMessage 删除消息。
func (f *FeishuAdapter) DeleteMessage(ctx context.Context, chatID string, messageID string) error {
	_, err := f.doAPI(ctx, "DELETE", "/open-apis/im/v1/messages/"+messageID, nil)
	return err
}

// SendTyping 飞书不支持 typing 指示器。
func (f *FeishuAdapter) SendTyping(ctx context.Context, chatID string) error {
	return nil
}

// SendImage 发送图片消息。
func (f *FeishuAdapter) SendImage(ctx context.Context, chatID string, imageURL string, caption string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"receive_id": chatID,
		"msg_type":   "image",
		"content":    fmt.Sprintf(`{"image_key":"%s"}`, imageURL),
	}
	return f.doAPI(ctx, "POST", "/open-apis/im/v1/messages?receive_id_type=chat_id", body)
}

// SendVoice 飞书支持语音消息。
func (f *FeishuAdapter) SendVoice(ctx context.Context, chatID string, audioPath string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"receive_id": chatID,
		"msg_type":   "audio",
		"content":    fmt.Sprintf(`{"file_key":"%s"}`, audioPath),
	}
	return f.doAPI(ctx, "POST", "/open-apis/im/v1/messages?receive_id_type=chat_id", body)
}

// SendVideo 发送视频消息。
func (f *FeishuAdapter) SendVideo(ctx context.Context, chatID string, videoPath string, caption string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"receive_id": chatID,
		"msg_type":   "media",
		"content":    fmt.Sprintf(`{"file_key":"%s","image_key":"%s"}`, videoPath, videoPath),
	}
	return f.doAPI(ctx, "POST", "/open-apis/im/v1/messages?receive_id_type=chat_id", body)
}

// SendDocument 发送文件消息。
func (f *FeishuAdapter) SendDocument(ctx context.Context, chatID string, filePath string, caption string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"receive_id": chatID,
		"msg_type":   "file",
		"content":    fmt.Sprintf(`{"file_key":"%s"}`, filePath),
	}
	return f.doAPI(ctx, "POST", "/open-apis/im/v1/messages?receive_id_type=chat_id", body)
}

// ReplyMessage 回复指定消息。
// 飞书支持线程回复，通过 root_id 指定被回复的消息。
func (f *FeishuAdapter) ReplyMessage(ctx context.Context, messageID string, content string) (*SendResult, error) {
	body := map[string]any{
		"msg_type": "text",
		"content":  fmt.Sprintf(`{"text":"%s"}`, escapeJSON(content)),
	}
	return f.doAPI(ctx, "POST", "/open-apis/im/v1/messages/"+messageID+"/reply", body)
}

// ReceiveEvent 处理飞书事件推送。
// 由外部 HTTP 服务器调用。
// Deprecated: 使用 ReceiveEventWithVerification 代替，以便验证请求签名。
func (f *FeishuAdapter) ReceiveEvent(payload []byte) error {
	return f.receiveEventInternal(payload)
}

// ReceiveEventWithVerification 处理飞书事件推送并验证签名。
// timestamp 和 signature 来自 HTTP 请求头。
func (f *FeishuAdapter) ReceiveEventWithVerification(payload []byte, timestamp, signature string) error {
	// 验证飞书事件签名
	if f.verificationToken != "" && signature != "" && timestamp != "" {
		h := hmac.New(sha256.New, []byte(f.verificationToken))
		h.Write([]byte(timestamp))
		h.Write([]byte("\n"))
		h.Write(payload)
		expected := base64.StdEncoding.EncodeToString(h.Sum(nil))
		if subtle.ConstantTimeCompare([]byte(signature), []byte(expected)) != 1 {
			return fmt.Errorf("飞书事件签名验证失败")
		}
	}
	return f.receiveEventInternal(payload)
}

func (f *FeishuAdapter) receiveEventInternal(payload []byte) error {
	var event struct {
		Schema string `json:"schema"`
		Header struct {
			EventType string `json:"event_type"`
		} `json:"header"`
		Event map[string]any `json:"event"`
	}
	if err := json.Unmarshal(payload, &event); err != nil {
		return err
	}

	// 仅处理消息事件
	if event.Header.EventType != "im.message.receive_v1" {
		return nil
	}

	msg, ok := event.Event["message"].(map[string]any)
	if !ok {
		return nil
	}

	chatID, _ := msg["chat_id"].(string)
	msgID, _ := msg["message_id"].(string)
	msgType, _ := msg["message_type"].(string)

	sender, _ := event.Event["sender"].(map[string]any)
	senderID, _ := sender["sender_id"].(map[string]any)

	userID := "unknown"
	if sid, ok := senderID["open_id"].(string); ok {
		userID = sid
	} else if sid, ok := senderID["user_id"].(string); ok {
		userID = sid
	}

	source := &SessionSource{
		Platform: PlatformFeishu,
		ChatID:   chatID,
		UserID:   userID,
		ChatType: "dm",
	}

	eventMsg := &MessageEvent{
		Source:    source,
		MessageID: msgID,
		Timestamp: time.Now(),
	}

	switch msgType {
	case "text":
		eventMsg.MessageType = MsgText
		if content, ok := msg["content"].(string); ok {
			var textContent struct {
				Text string `json:"text"`
			}
			if json.Unmarshal([]byte(content), &textContent) == nil {
				eventMsg.Text = textContent.Text
			}
		}
	case "image":
		eventMsg.MessageType = MsgPhoto
	default:
		eventMsg.MessageType = MsgText
		eventMsg.Text = "[不支持的消息类型: " + msgType + "]"
	}

	select {
	case f.msgCh <- eventMsg:
	default:
		slog.Warn("feishu message channel full, dropping message")
	}
	return nil
}

// ───────────────────────────── 自注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&AdapterEntry{
		Platform: PlatformFeishu,
		Name:     "Feishu",
		Factory:  func() PlatformAdapter { return &FeishuAdapter{} },
	})
}

// Configure 注入飞书平台配置。
// settings 必须包含 "app_id" 和 "app_secret" 键。
func (f *FeishuAdapter) Configure(settings map[string]any) error {
	appID, _ := settings["app_id"].(string)
	appSecret, _ := settings["app_secret"].(string)
	if appID == "" || appSecret == "" {
		return fmt.Errorf("feishu 平台缺少 app_id 或 app_secret 配置")
	}
	f.appID = appID
	f.appSecret = appSecret
	f.verificationToken, _ = settings["verification_token"].(string)
	f.client = &http.Client{Timeout: 30 * time.Second}
	f.msgCh = make(chan *MessageEvent, 128)
	return nil
}

// ───────────────────────────── 内部方法 ─────────────────────────────

// doAPI 发送飞书 API 请求。
func (f *FeishuAdapter) doAPI(ctx context.Context, method string, path string, body any) (*SendResult, error) {
	token, err := f.getTenantToken(ctx)
	if err != nil {
		return &SendResult{Success: false, Error: "failed to get token: " + err.Error()}, err
	}

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return &SendResult{Success: false, Error: err.Error()}, err
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, "https://open.feishu.cn"+path, bodyReader)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := f.client.Do(req)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error(), Retryable: true}, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseSize))
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, err
	}

	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			MessageID string `json:"message_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return &SendResult{Success: false, Error: err.Error()}, err
	}

	if result.Code != 0 {
		return &SendResult{
			Success:   false,
			Error:     fmt.Sprintf("feishu error %d: %s", result.Code, result.Msg),
			Retryable: result.Code == 99991663 || result.Code == 99991661, // 频率限制
		}, nil
	}

	return &SendResult{Success: true, MessageID: result.Data.MessageID}, nil
}

// getTenantToken 获取 tenant_access_token (带缓存)。
func (f *FeishuAdapter) getTenantToken(ctx context.Context) (string, error) {
	f.tokenMu.Lock()
	defer f.tokenMu.Unlock()

	if f.tenantToken != "" && time.Now().Before(f.tokenExpiry) {
		return f.tenantToken, nil
	}

	body := map[string]string{
		"app_id":     f.appID,
		"app_secret": f.appSecret,
	}
	data, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal",
		bytes.NewReader(data),
	)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := f.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseSize))
	if err != nil {
		return "", err
	}

	var result struct {
		Code              int    `json:"code"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"` // 秒
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", err
	}
	if result.Code != 0 {
		return "", fmt.Errorf("get tenant_access_token failed: %s", string(raw))
	}

	f.tenantToken = result.TenantAccessToken
	f.tokenExpiry = time.Now().Add(time.Duration(result.Expire-60) * time.Second)
	return f.tenantToken, nil
}

// escapeJSON 转义 JSON 字符串中的特殊字符。
func escapeJSON(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	for _, c := range s {
		switch c {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		case '\b':
			sb.WriteString(`\b`)
		case '\f':
			sb.WriteString(`\f`)
		default:
			if c < 0x20 {
				fmt.Fprintf(&sb, `\u%04x`, c)
			} else {
				sb.WriteRune(c)
			}
		}
	}
	return sb.String()
}
