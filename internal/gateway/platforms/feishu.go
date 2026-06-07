// 参考文档: https://open.feishu.cn/document/server-docs/im-v1/message/create
// 飞书适配器使用事件订阅 (Event Subscriptions) 接收消息,
// 通过消息 API (im/v1/messages) 发送和回复消息。
package platforms

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// ───────────────────────────── 飞书适配器 ─────────────────────────────

// FeishuAdapter 实现飞书开放平台适配器。
// 使用 tenant_access_token 认证, 通过事件订阅接收消息, 通过消息 API 发送消息。
type FeishuAdapter struct {
	tokenMu           sync.Mutex         // token 访问锁
	verificationToken string             // 事件订阅验证令牌
	closeOnce         sync.Once          // 确保 msgCh 只关闭一次
	mu                sync.RWMutex       // 保护 msgCh 发送/关闭
	appID             string             // 应用 App ID
	appSecret         string             // 应用 App Secret
	client            *http.Client       // HTTP 客户端
	tenantToken       string             // 缓存的 tenant_access_token
	tokenExpiry       time.Time          // token 过期时间
	msgCh             chan *MessageEvent // 入站消息通道
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
		f.mu.Lock()
		close(f.msgCh)
		f.mu.Unlock()
	})
	slog.Info("feishu adapter disconnected")
	return nil
}

// ReceiveEvent 处理飞书事件推送。
// 由外部 HTTP 服务器调用。
// Deprecated: 使用 ReceiveEventWithVerification 代替，以便验证请求签名。
func (f *FeishuAdapter) ReceiveEvent(payload []byte) ([]byte, error) {
	if f.verificationToken != "" {
		return nil, fmt.Errorf("ReceiveEvent 已弃用，请使用 ReceiveEventWithVerification 并提供签名参数")
	}
	// 尝试处理 url_verification 挑战
	var challengeReq struct {
		Type      string `json:"type"`
		Challenge string `json:"challenge"`
	}
	if err := json.Unmarshal(payload, &challengeReq); err == nil && challengeReq.Type == "url_verification" {
		resp, err := json.Marshal(map[string]string{"challenge": challengeReq.Challenge})
		if err != nil {
			return nil, fmt.Errorf("序列化挑战响应失败: %w", err)
		}
		return resp, nil
	}
	return nil, f.receiveEventInternal(payload)
}

// ReceiveEventWithVerification 处理飞书事件推送并验证签名。
// timestamp 和 signature 来自 HTTP 请求头。
// 当收到 url_verification 挑战时，返回包含 challenge 值的 JSON 响应。
func (f *FeishuAdapter) ReceiveEventWithVerification(payload []byte, timestamp, signature string) ([]byte, error) {
	// 处理 url_verification 挑战事件
	var challengeReq struct {
		Type      string `json:"type"`
		Challenge string `json:"challenge"`
		Token     string `json:"token"`
	}
	if err := json.Unmarshal(payload, &challengeReq); err == nil && challengeReq.Type == "url_verification" {
		if f.verificationToken != "" && challengeReq.Token != f.verificationToken {
			return nil, fmt.Errorf("url_verification token 验证失败")
		}
		resp, err := json.Marshal(map[string]string{"challenge": challengeReq.Challenge})
		if err != nil {
			return nil, fmt.Errorf("序列化挑战响应失败: %w", err)
		}
		return resp, nil
	}

	// 验证飞书事件签名
	if f.verificationToken != "" {
		if signature == "" || timestamp == "" {
			return nil, fmt.Errorf("飞书事件缺少签名参数 (X-Lark-Signature / X-Lark-Request-Timestamp)")
		}
		h := hmac.New(sha256.New, []byte(f.verificationToken))
		h.Write([]byte(timestamp))
		h.Write([]byte("\n"))
		h.Write(payload)
		expected := base64.StdEncoding.EncodeToString(h.Sum(nil))
		if subtle.ConstantTimeCompare([]byte(signature), []byte(expected)) != 1 {
			return nil, fmt.Errorf("飞书事件签名验证失败")
		}
	}
	return nil, f.receiveEventInternal(payload)
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

	f.mu.RLock()
	select {
	case f.msgCh <- eventMsg:
	default:
		slog.Warn("feishu message channel full, dropping message")
	}
	f.mu.RUnlock()
	return nil
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
