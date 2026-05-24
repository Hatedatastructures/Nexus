// 参考文档: https://developers.facebook.com/docs/whatsapp/cloud-api
// WhatsApp Cloud API 通过 Webhook 接收消息, 通过 HTTP POST 发送消息。
package platforms

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// ───────────────────────────── WhatsApp 适配器 ─────────────────────────────

// WhatsAppAdapter 实现 WhatsApp Cloud API 适配器。
// 使用 Webhook 接收消息，通过 /{phoneID}/messages 端点发送消息。
type WhatsAppAdapter struct {
	token       string             // 访问令牌 (系统用户或用户令牌)
	phoneID     string             // 电话号码 ID
	verifyToken string             // Webhook 验证令牌
	client      *http.Client       // HTTP 客户端
	baseURL     string             // API 基础 URL
	msgCh       chan *MessageEvent // 入站消息通道
	closeOnce   sync.Once          // 确保 msgCh 只关闭一次
}

// NewWhatsAppAdapter 创建 WhatsApp 适配器。
// token 为 API 访问令牌, phoneID 为 WhatsApp Business Account 电话号码 ID。
func NewWhatsAppAdapter(token string, phoneID string) *WhatsAppAdapter {
	return &WhatsAppAdapter{
		token:   token,
		phoneID: phoneID,
		client:  &http.Client{Timeout: 30 * time.Second},
		baseURL: "https://graph.facebook.com/v18.0",
		msgCh:   make(chan *MessageEvent, 128),
	}
}

// Name 返回平台名称。
func (w *WhatsAppAdapter) Name() string { return "WhatsApp" }

// PlatformType 返回平台类型枚举。
func (w *WhatsAppAdapter) PlatformType() Platform { return PlatformWhatsApp }

// MaxMessageLength 返回 WhatsApp 最大消息长度。
func (w *WhatsAppAdapter) MaxMessageLength() int { return 4096 }

// SupportsStreaming 返回是否支持流式编辑。
func (w *WhatsAppAdapter) SupportsStreaming() bool { return false }

// Connect 建立连接并开始监听消息 (Webhook 模式)。
func (w *WhatsAppAdapter) Connect(ctx context.Context) (<-chan *MessageEvent, error) {
	// Webhook 由外部 HTTP 服务器驱动，适配器等待 Webhook 推送
	slog.Info("whatsapp adapter connected (webhook mode)")
	return w.msgCh, nil
}

// Disconnect 关闭消息通道。
func (w *WhatsAppAdapter) Disconnect(ctx context.Context) error {
	w.closeOnce.Do(func() {
		close(w.msgCh)
	})
	slog.Info("whatsapp adapter disconnected")
	return nil
}

// Send 发送文本消息。
func (w *WhatsAppAdapter) Send(ctx context.Context, chatID string, content string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"messaging_product": "whatsapp",
		"to":                chatID,
		"type":              "text",
		"text": map[string]string{
			"body": content,
		},
	}
	return w.doAPI(ctx, "POST", fmt.Sprintf("/%s/messages", w.phoneID), body)
}

// EditMessage WhatsApp 不支持编辑已发送消息。
func (w *WhatsAppAdapter) EditMessage(ctx context.Context, chatID string, messageID string, content string) (*SendResult, error) {
	return &SendResult{Success: false, Error: "whatsapp does not support message editing"}, nil
}

// DeleteMessage WhatsApp 不支持删除消息。
func (w *WhatsAppAdapter) DeleteMessage(ctx context.Context, chatID string, messageID string) error {
	return fmt.Errorf("whatsapp does not support message deletion via API")
}

// SendTyping WhatsApp 不支持 typing 指示器。
func (w *WhatsAppAdapter) SendTyping(ctx context.Context, chatID string) error {
	return nil // 静默忽略
}

// SendImage 发送图片消息。
func (w *WhatsAppAdapter) SendImage(ctx context.Context, chatID string, imageURL string, caption string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"messaging_product": "whatsapp",
		"to":                chatID,
		"type":              "image",
		"image": map[string]string{
			"link":    imageURL,
			"caption": caption,
		},
	}
	return w.doAPI(ctx, "POST", fmt.Sprintf("/%s/messages", w.phoneID), body)
}

// SendVoice 发送语音消息。
func (w *WhatsAppAdapter) SendVoice(ctx context.Context, chatID string, audioPath string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"messaging_product": "whatsapp",
		"to":                chatID,
		"type":              "audio",
		"audio": map[string]string{
			"link": audioPath,
		},
	}
	return w.doAPI(ctx, "POST", fmt.Sprintf("/%s/messages", w.phoneID), body)
}

// SendVideo 发送视频消息。
func (w *WhatsAppAdapter) SendVideo(ctx context.Context, chatID string, videoPath string, caption string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"messaging_product": "whatsapp",
		"to":                chatID,
		"type":              "video",
		"video": map[string]string{
			"link":    videoPath,
			"caption": caption,
		},
	}
	return w.doAPI(ctx, "POST", fmt.Sprintf("/%s/messages", w.phoneID), body)
}

// SendDocument 发送文件消息。
func (w *WhatsAppAdapter) SendDocument(ctx context.Context, chatID string, filePath string, caption string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"messaging_product": "whatsapp",
		"to":                chatID,
		"type":              "document",
		"document": map[string]string{
			"link":    filePath,
			"caption": caption,
		},
	}
	return w.doAPI(ctx, "POST", fmt.Sprintf("/%s/messages", w.phoneID), body)
}

// ReceiveWebhook 处理来自 WhatsApp 的 Webhook 推送。
// 由外部 HTTP 服务器调用，将入站消息转发到内部消息通道。
func (w *WhatsAppAdapter) ReceiveWebhook(payload []byte) error {
	var hook struct {
		Entry []struct {
			Changes []struct {
				Value struct {
					Messages []whatsappMessage `json:"messages"`
				} `json:"value"`
			} `json:"changes"`
		} `json:"entry"`
	}
	if err := json.Unmarshal(payload, &hook); err != nil {
		return err
	}

	for _, entry := range hook.Entry {
		for _, change := range entry.Changes {
			for _, msg := range change.Value.Messages {
				event := w.convertMessage(&msg)
				if event != nil {
					select {
					case w.msgCh <- event:
					default:
						slog.Warn("whatsapp message channel full, dropping message")
					}
				}
			}
		}
	}
	return nil
}

// VerifyWebhook 验证 Webhook (用于 WhatsApp 配置验证)。
func (w *WhatsAppAdapter) VerifyWebhook(mode string, challenge string, verifyToken string) (string, bool) {
	if mode == "subscribe" && subtle.ConstantTimeCompare([]byte(verifyToken), []byte(w.verifyToken)) == 1 {
		return challenge, true
	}
	return "", false
}

// ───────────────────────────── 自注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&AdapterEntry{
		Platform: PlatformWhatsApp,
		Name:     "WhatsApp",
		Factory:  func() PlatformAdapter { return &WhatsAppAdapter{} },
	})
}

// Configure 注入 WhatsApp 平台配置。
// settings 必须包含 "token" 和 "phone_id" 键。
func (w *WhatsAppAdapter) Configure(settings map[string]any) error {
	token, _ := settings["token"].(string)
	phoneID, _ := settings["phone_id"].(string)
	if token == "" || phoneID == "" {
		return fmt.Errorf("whatsapp 平台缺少 token 或 phone_id 配置")
	}
	w.token = token
	w.phoneID = phoneID
	w.verifyToken, _ = settings["verify_token"].(string)
	w.client = &http.Client{Timeout: 30 * time.Second}
	w.baseURL = "https://graph.facebook.com/v18.0"
	w.msgCh = make(chan *MessageEvent, 128)
	return nil
}

// ───────────────────────────── 内部方法 ─────────────────────────────

// convertMessage 将 WhatsApp 消息转换为 MessageEvent。
func (w *WhatsAppAdapter) convertMessage(msg *whatsappMessage) *MessageEvent {
	source := &SessionSource{
		Platform: PlatformWhatsApp,
		ChatID:   msg.From,
		UserID:   msg.From,
		ChatType: "dm",
	}

	event := &MessageEvent{
		Source:    source,
		MessageID: msg.ID,
		Timestamp: time.Unix(msg.Timestamp, 0),
	}

	switch msg.Type {
	case "text":
		event.Text = msg.Text.Body
		event.MessageType = MsgText
	case "image":
		event.MessageType = MsgPhoto
		if msg.Image != nil {
			event.MediaURLs = []string{msg.Image.ID}
		}
		event.Text = msg.Image.Caption
	case "audio", "voice":
		event.MessageType = MsgVoice
	case "video":
		event.MessageType = MsgVideo
		if msg.Video != nil {
			event.Text = msg.Video.Caption
		}
	case "document":
		event.MessageType = MsgDocument
		if msg.Document != nil {
			event.Text = msg.Document.Caption
		}
	case "sticker":
		event.MessageType = MsgSticker
	case "location":
		event.MessageType = MsgLocation
	default:
		event.MessageType = MsgText
		event.Text = "[unsupported message type: " + msg.Type + "]"
	}

	return event
}

// doAPI 发送 WhatsApp API 请求。
func (w *WhatsAppAdapter) doAPI(ctx context.Context, method string, path string, body any) (*SendResult, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return &SendResult{Success: false, Error: err.Error()}, err
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, w.baseURL+path, bodyReader)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, err
	}
	req.Header.Set("Authorization", "Bearer "+w.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error(), Retryable: true}, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseSize))
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, err
	}

	if resp.StatusCode >= 400 {
		return &SendResult{
			Success:   false,
			Error:     fmt.Sprintf("whatsapp api error %d: %s", resp.StatusCode, string(raw)),
			Retryable: resp.StatusCode == 429 || resp.StatusCode >= 500,
		}, nil
	}

	// 解析消息 ID
	var result struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	json.Unmarshal(raw, &result)
	msgID := ""
	if len(result.Messages) > 0 {
		msgID = result.Messages[0].ID
	}

	return &SendResult{Success: true, MessageID: msgID}, nil
}

// ───────────────────────────── WhatsApp 消息类型 ─────────────────────────────

type whatsappMessage struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	Type      string `json:"type"`
	Timestamp int64  `json:"timestamp"`
	Text      struct {
		Body string `json:"body"`
	} `json:"text"`
	Image    *whatsappMedia `json:"image"`
	Video    *whatsappMedia `json:"video"`
	Audio    *whatsappMedia `json:"audio"`
	Document *whatsappMedia `json:"document"`
}

type whatsappMedia struct {
	ID      string `json:"id"`
	Caption string `json:"caption"`
}
