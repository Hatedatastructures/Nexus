// 参考文档: https://developers.weixin.qq.com/doc/offiaccount/Message_Management/Receiving_standard_messages.html
// 微信公众号适配器通过 XML 格式接收消息, 通过客服消息 API (JSON) 发送和回复消息。
package platforms

import (
	"sync"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"
)

// ───────────────────────────── 微信适配器 ─────────────────────────────

// WeChatAdapter 实现微信公众号适配器。
// 使用 XML 格式接收用户消息, 通过客服消息 API (JSON) 主动发送/回复消息。
type WeChatAdapter struct {
ttokenMu     sync.Mutex         // token 访问锁
	tokenMu     sync.Mutex         // token 访问锁
	appID  string             // 公众号 AppID
	secret string             // 公众号 AppSecret
	token  string             // 服务器验证 Token (用于签名验证)
	client *http.Client       // HTTP 客户端
	msgCh  chan *MessageEvent // 入站消息通道
	accessToken string        // access_token 缓存
	tokenExpiry time.Time     // token 过期时间
}

// NewWeChatAdapter 创建微信适配器。
// appID 为公众号 AppID, secret 为 AppSecret, token 为服务器配置的 Token。
func NewWeChatAdapter(appID string, secret string, token string) *WeChatAdapter {
	return &WeChatAdapter{
		appID:  appID,
		secret: secret,
		token:  token,
		client: &http.Client{Timeout: 30 * time.Second},
		msgCh:  make(chan *MessageEvent, 128),
	}
}

// Name 返回平台名称。
func (w *WeChatAdapter) Name() string { return "WeChat" }

// PlatformType 返回平台类型枚举。
func (w *WeChatAdapter) PlatformType() Platform { return PlatformWeChat }

// MaxMessageLength 微信客服消息最大长度 2048 字符。
func (w *WeChatAdapter) MaxMessageLength() int { return 2048 }

// SupportsStreaming 微信不支持消息编辑。
func (w *WeChatAdapter) SupportsStreaming() bool { return false }

// Connect 建立连接。
func (w *WeChatAdapter) Connect(ctx context.Context) (<-chan *MessageEvent, error) {
	slog.Info("wechat adapter connected (callback mode)")
	return w.msgCh, nil
}

// Disconnect 关闭消息通道。
func (w *WeChatAdapter) Disconnect(ctx context.Context) error {
	close(w.msgCh)
	slog.Info("wechat adapter disconnected")
	return nil
}

// Send 发送客服消息。
func (w *WeChatAdapter) Send(ctx context.Context, chatID string, content string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"touser":  chatID,
		"msgtype": "text",
		"text":    map[string]string{"content": content},
	}
	return w.doAPI(ctx, "POST", "/cgi-bin/message/custom/send", body)
}

// EditMessage 微信不支持编辑消息。
func (w *WeChatAdapter) EditMessage(ctx context.Context, chatID string, messageID string, content string) (*SendResult, error) {
	return &SendResult{Success: false, Error: "wechat does not support message editing"}, nil
}

// DeleteMessage 微信不支持删除消息。
func (w *WeChatAdapter) DeleteMessage(ctx context.Context, chatID string, messageID string) error {
	return fmt.Errorf("wechat does not support message deletion")
}

// SendTyping 微信不支持 typing 指示器。
func (w *WeChatAdapter) SendTyping(ctx context.Context, chatID string) error {
	return nil
}

// SendImage 发送图片消息。
func (w *WeChatAdapter) SendImage(ctx context.Context, chatID string, imageURL string, caption string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"touser":  chatID,
		"msgtype": "image",
		"image":   map[string]string{"media_id": imageURL},
	}
	return w.doAPI(ctx, "POST", "/cgi-bin/message/custom/send", body)
}

// SendVoice 发送语音消息。
func (w *WeChatAdapter) SendVoice(ctx context.Context, chatID string, audioPath string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"touser":  chatID,
		"msgtype": "voice",
		"voice":   map[string]string{"media_id": audioPath},
	}
	return w.doAPI(ctx, "POST", "/cgi-bin/message/custom/send", body)
}

// SendVideo 发送视频消息。
func (w *WeChatAdapter) SendVideo(ctx context.Context, chatID string, videoPath string, caption string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"touser":  chatID,
		"msgtype": "video",
		"video":   map[string]string{"media_id": videoPath, "title": caption},
	}
	return w.doAPI(ctx, "POST", "/cgi-bin/message/custom/send", body)
}

// SendDocument 微信不支持直接发文件。
func (w *WeChatAdapter) SendDocument(ctx context.Context, chatID string, filePath string, caption string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "wechat does not support direct file sending"}, nil
}

// ReceiveCallback 处理来自微信服务器的 XML 回调，转换为 MessageEvent。
func (w *WeChatAdapter) ReceiveCallback(xmlBody []byte) (*MessageEvent, string, error) {
	var wxMsg wechatXMLMessage
	if err := xml.Unmarshal(xmlBody, &wxMsg); err != nil {
		return nil, "", err
	}

	source := &SessionSource{
		Platform: PlatformWeChat,
		ChatID:   wxMsg.FromUserName,
		UserID:   wxMsg.FromUserName,
		ChatType: "dm",
	}

	event := &MessageEvent{
		Source:    source,
		MessageID: wxMsg.MsgID,
		Timestamp: time.Unix(int64(wxMsg.CreateTime), 0),
	}

	switch wxMsg.MsgType {
	case "text":
		event.Text = wxMsg.Content
		event.MessageType = MsgText
	case "image":
		event.MessageType = MsgPhoto
		event.MediaURLs = []string{wxMsg.PicURL}
	case "voice":
		event.MessageType = MsgVoice
	case "video":
		event.MessageType = MsgVideo
	case "location":
		event.MessageType = MsgLocation
	default:
		event.MessageType = MsgText
		event.Text = "[不支持的消息类型: " + wxMsg.MsgType + "]"
	}

	// 生成被动回复 (自动回复)
	replyXML := w.buildReplyXML(wxMsg.ToUserName, wxMsg.FromUserName)

	return event, replyXML, nil
}

// VerifySignature 验证微信服务器签名。
func (w *WeChatAdapter) VerifySignature(signature string, timestamp string, nonce string) bool {
	parts := []string{w.token, timestamp, nonce}
	sort.Strings(parts)
	joined := strings.Join(parts, "")
	hash := fmt.Sprintf("%x", sha1.Sum([]byte(joined)))
	return hash == signature
}

// ───────────────────────────── 自注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&AdapterEntry{
		Platform: PlatformWeChat,
		Name:     "WeChat",
		Factory:  func() PlatformAdapter { return &WeChatAdapter{} },
	})
}

// Configure 注入微信公众号平台配置。
// settings 必须包含 "app_id"、"secret" 和 "token" 键。
func (w *WeChatAdapter) Configure(settings map[string]any) error {
	appID, _ := settings["app_id"].(string)
	secret, _ := settings["secret"].(string)
	token, _ := settings["token"].(string)
	if appID == "" || secret == "" || token == "" {
		return fmt.Errorf("wechat 平台缺少 app_id、secret 或 token 配置")
	}
	w.appID = appID
	w.secret = secret
	w.token = token
	w.client = &http.Client{Timeout: 30 * time.Second}
	w.msgCh = make(chan *MessageEvent, 128)
	return nil
}

// ───────────────────────────── 内部方法 ─────────────────────────────

// buildReplyXML 构建微信被动回复 XML。
func (w *WeChatAdapter) buildReplyXML(toUser string, fromUser string) string {
	return fmt.Sprintf(
		`<xml><ToUserName><![CDATA[%s]]></ToUserName>`+
			`<FromUserName><![CDATA[%s]]></FromUserName>`+
			`<CreateTime>%d</CreateTime>`+
			`<MsgType><![CDATA[text]]></MsgType>`+
			`<Content><![CDATA[收到您的消息，正在处理中...]]></Content></xml>`,
		toUser, fromUser, time.Now().Unix(),
	)
}

// doAPI 发送微信 API 请求。
func (w *WeChatAdapter) doAPI(ctx context.Context, method string, path string, body any) (*SendResult, error) {
	token, err := w.getAccessToken(ctx)
	if err != nil {
		return &SendResult{Success: false, Error: "failed to get access_token: " + err.Error()}, err
	}

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return &SendResult{Success: false, Error: err.Error()}, err
		}
		bodyReader = bytes.NewReader(data)
	}

	url := "https://api.weixin.qq.com" + path + "?access_token=" + token
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := w.client.Do(req)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error(), Retryable: true}, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, err
	}

	// 解析微信响应
	var wxResp struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
		MsgID   int64  `json:"msgid"`
	}
	if err := json.Unmarshal(raw, &wxResp); err != nil {
		return &SendResult{Success: false, Error: err.Error()}, err
	}
	if wxResp.ErrCode != 0 {
		return &SendResult{
			Success:   false,
			Error:     fmt.Sprintf("wechat error %d: %s", wxResp.ErrCode, wxResp.ErrMsg),
			Retryable: wxResp.ErrCode == -1 || wxResp.ErrCode >= 45000,
		}, nil
	}

	return &SendResult{
		Success:   true,
		MessageID: fmt.Sprintf("%d", wxResp.MsgID),
	}, nil
}

// getAccessToken 获取 access_token (带缓存)。
func (w *WeChatAdapter) getAccessToken(ctx context.Context) (string, error) {
	w.tokenMu.Lock()
	defer w.tokenMu.Unlock()

	if w.accessToken != "" && time.Now().Before(w.tokenExpiry) {
		return w.accessToken, nil
	}

	url := fmt.Sprintf(
		"https://api.weixin.qq.com/cgi-bin/token?grant_type=client_credential&appid=%s&secret=%s",
		w.appID, w.secret,
	)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}

	resp, err := w.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		ErrCode     int    `json:"errcode"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", err
	}
	if result.ErrCode != 0 {
		return "", fmt.Errorf("get access_token failed: %s", string(raw))
	}

	w.accessToken = result.AccessToken
	w.tokenExpiry = time.Now().Add(time.Duration(result.ExpiresIn-60) * time.Second)

	return w.accessToken, nil
}

// ───────────────────────────── 微信消息类型 ─────────────────────────────

// wechatXMLMessage 微信 XML 消息体。
type wechatXMLMessage struct {
	XMLName      xml.Name `xml:"xml"`
	ToUserName   string   `xml:"ToUserName"`
	FromUserName string   `xml:"FromUserName"`
	CreateTime   int      `xml:"CreateTime"`
	MsgType      string   `xml:"MsgType"`
	Content      string   `xml:"Content"`
	MsgID        string   `xml:"MsgId"`
	PicURL       string   `xml:"PicUrl"`
	MediaID      string   `xml:"MediaId"`
	Format       string   `xml:"Format"`
	Recognition  string   `xml:"Recognition"`
	LocationX    string   `xml:"Location_X"`
	LocationY    string   `xml:"Location_Y"`
	Scale        string   `xml:"Scale"`
	Label        string   `xml:"Label"`
}
