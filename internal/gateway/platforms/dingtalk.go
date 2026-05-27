// 参考文档: https://open.dingtalk.com/document/orgapp/message-types-and-message-template
// 钉钉适配器通过消息回调接收消息, 通过机器人消息 API 发送和回复消息。
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

// ───────────────────────────── 钉钉适配器 ─────────────────────────────

// DingTalkAdapter 实现钉钉开放平台适配器。
// 使用 access_token 认证, 通过消息回调接收消息, 通过机器人 API 发送消息。
type DingTalkAdapter struct {
	tokenMu        sync.Mutex         // token 访问锁
	callbackSecret string             // 回调签名验证密钥
	closeOnce      sync.Once          // 确保 msgCh 只关闭一次
	appKey    string             // 应用 AppKey
	appSecret string             // 应用 AppSecret
	client    *http.Client       // HTTP 客户端
	msgCh     chan *MessageEvent // 入站消息通道
	accessToken string           // 缓存的 access_token
	tokenExpiry time.Time        // token 过期时间
}

// NewDingTalkAdapter 创建钉钉适配器。
// appKey 为应用 AppKey, appSecret 为应用 AppSecret。
func NewDingTalkAdapter(appKey string, appSecret string) *DingTalkAdapter {
	return &DingTalkAdapter{
		appKey:    appKey,
		appSecret: appSecret,
		client:    &http.Client{Timeout: 30 * time.Second},
		msgCh:     make(chan *MessageEvent, 128),
	}
}

// Name 返回平台名称。
func (d *DingTalkAdapter) Name() string { return "DingTalk" }

// PlatformType 返回平台类型枚举。
func (d *DingTalkAdapter) PlatformType() Platform { return PlatformDingTalk }

// MaxMessageLength 钉钉消息最大长度 20000 字符。
func (d *DingTalkAdapter) MaxMessageLength() int { return 20000 }

// SupportsStreaming 钉钉支持流式输出 (AI 卡片)。
func (d *DingTalkAdapter) SupportsStreaming() bool { return true }

// Connect 建立连接。
func (d *DingTalkAdapter) Connect(ctx context.Context) (<-chan *MessageEvent, error) {
	if _, err := d.getAccessToken(ctx); err != nil {
		slog.Warn("dingtalk: failed to get initial token", "err", err)
	}
	slog.Info("dingtalk adapter connected (callback mode)")
	return d.msgCh, nil
}

// Disconnect 关闭消息通道。
func (d *DingTalkAdapter) Disconnect(ctx context.Context) error {
	d.closeOnce.Do(func() {
		close(d.msgCh)
	})
	slog.Info("dingtalk adapter disconnected")
	return nil
}

// Send 发送文本消息 (使用消息通知 API)。
func (d *DingTalkAdapter) Send(ctx context.Context, chatID string, content string, opts *SendOptions) (*SendResult, error) {
	msgParamJSON, _ := json.Marshal(map[string]string{"content": content})
	body := map[string]any{
		"msgParam": string(msgParamJSON),
		"msgKey":   "sampleText",
		"openConversationId": chatID,
		"robotCode": d.appKey,
	}
	return d.doAPI(ctx, "POST", "/v1.0/robot/groupMessages/send", body)
}

// EditMessage 钉钉支持流式更新 (通过 Stream AI 卡片方式)。
func (d *DingTalkAdapter) EditMessage(ctx context.Context, chatID string, messageID string, content string) (*SendResult, error) {
	if err := validateMessageID(messageID); err != nil {
		return nil, err
	}
	// 钉钉的流式编辑通过更新 AI 流式卡片实现
	body := map[string]any{
		"msgParam": fmt.Sprintf(`{"content":"%s"}`, escapeJSONDing(content)),
		"msgKey":   "sampleText",
		"openConversationId": chatID,
	}
	return d.doAPI(ctx, "PUT", "/v1.0/robot/groupMessages/"+messageID, body)
}

// DeleteMessage 钉钉支持撤回消息。
func (d *DingTalkAdapter) DeleteMessage(ctx context.Context, chatID string, messageID string) error {
	if err := validateMessageID(messageID); err != nil {
		return err
	}
	body := map[string]string{
		"openConversationId": chatID,
		"processQueryKeys":   messageID,
	}
	_, err := d.doAPI(ctx, "POST", "/v1.0/robot/groupMessages/recall", body)
	return err
}

// SendTyping 钉钉不支持 typing 指示器。
func (d *DingTalkAdapter) SendTyping(ctx context.Context, chatID string) error {
	return nil
}

// SendImage 发送图片消息。
func (d *DingTalkAdapter) SendImage(ctx context.Context, chatID string, imageURL string, caption string, opts *SendOptions) (*SendResult, error) {
	imgParamJSON, _ := json.Marshal(map[string]string{"photoURL": imageURL, "title": caption})
	body := map[string]any{
		"msgParam": string(imgParamJSON),
		"msgKey":   "samplePhotoMsg",
		"openConversationId": chatID,
	}
	return d.doAPI(ctx, "POST", "/v1.0/robot/groupMessages/send", body)
}

// SendVoice 钉钉支持语音消息。
func (d *DingTalkAdapter) SendVoice(ctx context.Context, chatID string, audioPath string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"msgParam": fmt.Sprintf(`{"media_id":"%s"}`, audioPath),
		"msgKey":   "sampleVoiceMsg",
		"openConversationId": chatID,
	}
	return d.doAPI(ctx, "POST", "/v1.0/robot/groupMessages/send", body)
}

// SendVideo 钉钉支持视频消息。
func (d *DingTalkAdapter) SendVideo(ctx context.Context, chatID string, videoPath string, caption string, opts *SendOptions) (*SendResult, error) {
	vidParamJSON, _ := json.Marshal(map[string]string{"videoURL": videoPath, "title": caption})
	body := map[string]any{
		"msgParam": string(vidParamJSON),
		"msgKey":   "sampleVideoMsg",
		"openConversationId": chatID,
	}
	return d.doAPI(ctx, "POST", "/v1.0/robot/groupMessages/send", body)
}

// SendDocument 发送文件消息。
func (d *DingTalkAdapter) SendDocument(ctx context.Context, chatID string, filePath string, caption string, opts *SendOptions) (*SendResult, error) {
	docParamJSON, _ := json.Marshal(map[string]string{"media_id": filePath, "title": caption})
	body := map[string]any{
		"msgParam": string(docParamJSON),
		"msgKey":   "sampleFileMsg",
		"openConversationId": chatID,
	}
	return d.doAPI(ctx, "POST", "/v1.0/robot/groupMessages/send", body)
}

// ReceiveCallback 处理钉钉消息回调。
// 由外部 HTTP 服务器调用。
func (d *DingTalkAdapter) ReceiveCallback(payload []byte) error {
	// 钉钉回调格式: 加密后的 JSON
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return err
	}

	// 验证回调签名 (如果配置了 callbackSecret)
	if d.callbackSecret != "" {
		ts, _ := raw["timestamp"].(string)
		sign, _ := raw["sign"].(string)
		if ts != "" && sign != "" {
			h := hmac.New(sha256.New, []byte(d.callbackSecret))
			h.Write([]byte(ts + "\n" + d.callbackSecret))
			expected := base64.StdEncoding.EncodeToString(h.Sum(nil))
			if subtle.ConstantTimeCompare([]byte(sign), []byte(expected)) != 1 {
				return fmt.Errorf("钉钉回调签名验证失败")
			}
		} else {
			return fmt.Errorf("钉钉回调缺少签名参数 (timestamp/sign)")
		}
	} else {
		slog.Warn("[DingTalk] callbackSecret 未配置，回调签名验证被跳过")
	}

	// 检查是否为加密回调
	if encrypt, ok := raw["encrypt"].(string); ok && encrypt != "" {
		// 加密回调需要 AES 解密，当前简化实现中记录警告
		slog.Warn("dingtalk: 收到加密回调，当前版本暂不支持解密")
		return nil
	}

	source := &SessionSource{
		Platform: PlatformDingTalk,
		ChatType: "dm",
	}

	// 解析发送者
	if senderID, ok := raw["senderId"].(string); ok {
		source.UserID = senderID
	}
	if senderNick, ok := raw["senderNick"].(string); ok {
		source.UserName = senderNick
	}

	// 解析会话 ID
	if convID, ok := raw["conversationId"].(string); ok {
		source.ChatID = convID
	}
	if convType, ok := raw["conversationType"].(string); ok {
		if convType == "2" {
			source.ChatType = "group"
		}
	}

	event := &MessageEvent{
		Source:    source,
		Timestamp: time.Now(),
	}

	// 解析文本内容
	if text, ok := raw["text"].(map[string]any); ok {
		if content, ok := text["content"].(string); ok {
			event.Text = content
			event.MessageType = MsgText
		}
	} else if textContent, ok := raw["text"].(string); ok {
		event.Text = textContent
		event.MessageType = MsgText
	}

	if event.Text == "" {
		return nil // 忽略空消息
	}

	if msgID, ok := raw["msgId"].(string); ok {
		event.MessageID = msgID
	}

	select {
	case d.msgCh <- event:
	default:
		slog.Warn("dingtalk message channel full, dropping message")
	}
	return nil
}

// ───────────────────────────── 自注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&AdapterEntry{
		Platform: PlatformDingTalk,
		Name:     "DingTalk",
		Factory:  func() PlatformAdapter { return &DingTalkAdapter{} },
	})
}

// Configure 注入钉钉平台配置。
// settings 必须包含 "app_key" 和 "app_secret" 键。
func (d *DingTalkAdapter) Configure(settings map[string]any) error {
	appKey, _ := settings["app_key"].(string)
	appSecret, _ := settings["app_secret"].(string)
	if appKey == "" || appSecret == "" {
		return fmt.Errorf("dingtalk 平台缺少 app_key 或 app_secret 配置")
	}
	d.appKey = appKey
	d.appSecret = appSecret
	d.callbackSecret, _ = settings["callback_secret"].(string)
	d.client = &http.Client{Timeout: 30 * time.Second}
	d.msgCh = make(chan *MessageEvent, 128)
	return nil
}

// ───────────────────────────── 内部方法 ─────────────────────────────

// doAPI 发送钉钉 API 请求。
func (d *DingTalkAdapter) doAPI(ctx context.Context, method string, path string, body any) (*SendResult, error) {
	token, err := d.getAccessToken(ctx)
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

	// 添加钉钉 API 要求的请求头
	timestamp := fmt.Sprintf("%d", time.Now().UnixMilli())
	sign := d.calcSign(timestamp)

	req, err := http.NewRequestWithContext(ctx, method, "https://api.dingtalk.com"+path, bodyReader)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, err
	}
	req.Header.Set("x-acs-dingtalk-access-token", token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-acs-dingtalk-sdk-version", "1.0.0")
	req.Header.Set("x-acs-timestamp", timestamp)
	req.Header.Set("x-acs-signature", sign)

	resp, err := d.client.Do(req)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error(), Retryable: true}, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseSize))
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, err
	}

	var result struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		MsgID   string `json:"processQueryKey"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return &SendResult{Success: false, Error: err.Error()}, err
	}

	if result.Code != "" && result.Code != "0" {
		return &SendResult{
			Success:   false,
			Error:     fmt.Sprintf("dingtalk error %s: %s", result.Code, result.Message),
			Retryable: result.Code == "401" || result.Code == "90018",
		}, nil
	}

	return &SendResult{Success: true, MessageID: result.MsgID}, nil
}

// getAccessToken 获取 access_token (带缓存)。
func (d *DingTalkAdapter) getAccessToken(ctx context.Context) (string, error) {
	d.tokenMu.Lock()
	defer d.tokenMu.Unlock()

	if d.accessToken != "" && time.Now().Before(d.tokenExpiry) {
		return d.accessToken, nil
	}

	payload := map[string]string{
		"appKey":    d.appKey,
		"appSecret": d.appSecret,
	}
	data, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://api.dingtalk.com/v1.0/oauth2/accessToken", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseSize))
	if err != nil {
		return "", err
	}

	var result struct {
		AccessToken string `json:"accessToken"`
		ExpireIn    int    `json:"expireIn"` // 秒
		Code        string `json:"code"`
		Message     string `json:"message"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", err
	}
	if result.Code != "" && result.Code != "0" {
		return "", fmt.Errorf("get access_token failed: %s", string(raw))
	}

	d.accessToken = result.AccessToken
	buffer := 60
	if result.ExpireIn < buffer {
		buffer = result.ExpireIn / 2
	}
	d.tokenExpiry = time.Now().Add(time.Duration(result.ExpireIn-buffer) * time.Second)

	return d.accessToken, nil
}

// calcSign 计算钉钉 API 请求签名。
func (d *DingTalkAdapter) calcSign(timestamp string) string {
	h := hmac.New(sha256.New, []byte(d.appSecret))
	h.Write([]byte(timestamp + "\n" + d.appSecret))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// escapeJSONDing 为钉钉消息 JSON 转义内容。
func escapeJSONDing(s string) string {
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
