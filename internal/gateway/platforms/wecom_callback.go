// Package platforms 提供企业微信回调模式适配器。
// 通过 HTTP 回调接收企业微信消息，通过主动发送 API 回复。
package platforms

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// ───────────────────────────── 常量 ─────────────────────────────

const (
	wecomCallbackRequestTimeout = 15 * time.Second
	wecomCallbackMaxMessageLen  = 2048
	wecomCallbackMaxBodySize    = 1 << 20 // 1MB
)

// ───────────────────────────── WeComCallbackAdapter ─────────────────────────────

// WeComCallbackAdapter 企业微信回调模式适配器。
type WeComCallbackAdapter struct {
	corpID         string
	agentID        string
	token          string
	encodingAESKey string
	messageHandler func(*MessageEvent)
	httpClient     *http.Client
	msgCh          chan *MessageEvent
	running        bool
	webhookPort    int
}

// WeComXMLMessage 企业微信 XML 消息结构。
type WeComXMLMessage struct {
	XMLName      xml.Name `xml:"xml"`
	ToUserName   string   `xml:"ToUserName"`
	FromUserName string   `xml:"FromUserName"`
	CreateTime   int64    `xml:"CreateTime"`
	MsgType      string   `xml:"MsgType"`
	Content      string   `xml:"Content"`
	MsgID        string   `xml:"MsgId"`
	AgentID      string   `xml:"AgentID"`
}

// WeComXMLResponse 企业微信 XML 响应结构。
type WeComXMLResponse struct {
	XMLName      xml.Name `xml:"xml"`
	ToUserName   string   `xml:"ToUserName"`
	FromUserName string   `xml:"FromUserName"`
	CreateTime   int64    `xml:"CreateTime"`
	MsgType      string   `xml:"MsgType"`
	Content      string   `xml:"Content"`
}

// NewWeComCallbackAdapter 创建企业微信回调适配器。
func NewWeComCallbackAdapter(messageHandler func(*MessageEvent)) *WeComCallbackAdapter {
	corpID := os.Getenv("WECOM_CORP_ID")
	agentID := os.Getenv("WECOM_AGENT_ID")
	token := os.Getenv("WECOM_TOKEN")
	encodingAESKey := os.Getenv("WECOM_ENCODING_AES_KEY")

	webhookPort := 8083
	if p := os.Getenv("WECOM_WEBHOOK_PORT"); p != "" {
		fmt.Sscanf(p, "%d", &webhookPort)
	}

	return &WeComCallbackAdapter{
		corpID:         corpID,
		agentID:        agentID,
		token:          token,
		encodingAESKey: encodingAESKey,
		messageHandler: messageHandler,
		httpClient:     &http.Client{Timeout: wecomCallbackRequestTimeout},
		webhookPort:    webhookPort,
	}
}

// ───────────────────────────── PlatformAdapter 接口实现 ─────────────────────────────

func (a *WeComCallbackAdapter) Name() string          { return "WeCom Callback" }
func (a *WeComCallbackAdapter) PlatformType() Platform { return PlatformWeCom }
func (a *WeComCallbackAdapter) MaxMessageLength() int  { return wecomCallbackMaxMessageLen }
func (a *WeComCallbackAdapter) SupportsStreaming() bool { return false }

func (a *WeComCallbackAdapter) Connect(ctx context.Context) (<-chan *MessageEvent, error) {
	if a.corpID == "" || a.agentID == "" || a.token == "" {
		return nil, fmt.Errorf("WECOM_CORP_ID, WECOM_AGENT_ID 和 WECOM_TOKEN 是必填项")
	}

	a.msgCh = make(chan *MessageEvent, 100)
	a.running = true

	// 启动 webhook 服务器
	mux := http.NewServeMux()
	mux.HandleFunc("/wecom/callback", a.handleCallback)

	go func() {
		addr := fmt.Sprintf(":%d", a.webhookPort)
		slog.Info("[WeCom Callback] webhook server started", "addr", addr)
		if err := http.ListenAndServe(addr, mux); err != nil {
			slog.Error("[WeCom Callback] webhook server failed", "err", err)
		}
	}()

	slog.Info("[WeCom Callback] connected", "corp_id", a.corpID)
	return a.msgCh, nil
}

func (a *WeComCallbackAdapter) Disconnect(ctx context.Context) error {
	a.running = false
	slog.Info("[WeCom Callback] disconnected")
	return nil
}

func (a *WeComCallbackAdapter) Send(ctx context.Context, chatID string, content string, opts *SendOptions) (*SendResult, error) {
	// chatID 格式: "corp_id:user_id"
	parts := strings.SplitN(chatID, ":", 2)
	if len(parts) != 2 {
		return &SendResult{Success: false, Error: "无效的 chatID 格式，应为 corp_id:user_id"}, nil
	}

	userID := parts[1]

	// 调用企业微信发送消息 API
	accessToken, err := a.getAccessToken(ctx)
	if err != nil {
		return &SendResult{Success: false, Error: fmt.Sprintf("获取 access_token 失败: %v", err)}, nil
	}

	apiURL := fmt.Sprintf("https://qyapi.weixin.qq.com/cgi-bin/message/send?access_token=%s", accessToken)

	payload := map[string]any{
		"touser":  userID,
		"msgtype": "text",
		"agentid": a.agentID,
		"text":    map[string]string{"content": content},
	}

	jsonData, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(jsonData))
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, nil
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, nil
	}
	defer resp.Body.Close()

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)

	if errcode, ok := result["errcode"].(float64); ok && errcode != 0 {
		errmsg, _ := result["errmsg"].(string)
		return &SendResult{Success: false, Error: fmt.Sprintf("企业微信 API 错误: %s", errmsg)}, nil
	}

	return &SendResult{Success: true}, nil
}

func (a *WeComCallbackAdapter) EditMessage(ctx context.Context, chatID string, messageID string, content string) (*SendResult, error) {
	return &SendResult{Success: false, Error: "企业微信不支持编辑消息"}, nil
}

func (a *WeComCallbackAdapter) DeleteMessage(ctx context.Context, chatID string, messageID string) error {
	return fmt.Errorf("企业微信不支持删除消息")
}

func (a *WeComCallbackAdapter) SendTyping(ctx context.Context, chatID string) error {
	return nil
}

func (a *WeComCallbackAdapter) SendImage(ctx context.Context, chatID string, imageURL string, caption string, opts *SendOptions) (*SendResult, error) {
	// 简化处理：发送文本
	text := caption
	if text == "" {
		text = imageURL
	}
	return a.Send(ctx, chatID, text, opts)
}

func (a *WeComCallbackAdapter) SendVoice(ctx context.Context, chatID string, audioPath string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "企业微信回调模式不支持语音"}, nil
}

func (a *WeComCallbackAdapter) SendVideo(ctx context.Context, chatID string, videoPath string, caption string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "企业微信回调模式不支持视频"}, nil
}

func (a *WeComCallbackAdapter) SendDocument(ctx context.Context, chatID string, filePath string, caption string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "企业微信回调模式不支持文件"}, nil
}

// ───────────────────────────── 回调处理 ─────────────────────────────

func (a *WeComCallbackAdapter) handleCallback(w http.ResponseWriter, r *http.Request) {
	// 处理 URL 验证 (GET 请求)
	if r.Method == http.MethodGet {
		a.handleVerification(w, r)
		return
	}

	// 处理消息 (POST 请求)
	if r.Method == http.MethodPost {
		a.handleMessage(w, r)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (a *WeComCallbackAdapter) handleVerification(w http.ResponseWriter, r *http.Request) {
	msgSignature := r.URL.Query().Get("msg_signature")
	timestamp := r.URL.Query().Get("timestamp")
	nonce := r.URL.Query().Get("nonce")
	echoStr := r.URL.Query().Get("echostr")

	// 验证签名
	expectedSignature := a.generateSignature(timestamp, nonce, echoStr)
	if subtle.ConstantTimeCompare([]byte(msgSignature), []byte(expectedSignature)) != 1 {
		http.Error(w, "签名验证失败", http.StatusForbidden)
		return
	}

	// 返回 echostr
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprint(w, echoStr)
}

func (a *WeComCallbackAdapter) handleMessage(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, wecomCallbackMaxBodySize))
	if err != nil {
		http.Error(w, "读取请求失败", http.StatusBadRequest)
		return
	}

	// 验证 POST 回调签名 — 必须提供签名参数
	msgSignature := r.URL.Query().Get("msg_signature")
	timestamp := r.URL.Query().Get("timestamp")
	nonce := r.URL.Query().Get("nonce")
	if msgSignature == "" || timestamp == "" || nonce == "" {
		http.Error(w, "缺少签名参数", http.StatusBadRequest)
		return
	}
	expected := a.generateSignature(timestamp, nonce, string(body))
	if subtle.ConstantTimeCompare([]byte(msgSignature), []byte(expected)) != 1 {
		http.Error(w, "签名验证失败", http.StatusForbidden)
		return
	}

	var msg WeComXMLMessage
	if err := xml.Unmarshal(body, &msg); err != nil {
		http.Error(w, "解析 XML 失败", http.StatusBadRequest)
		return
	}

	// 只处理文本消息
	if msg.MsgType != "text" {
		w.Header().Set("Content-Type", "text/xml")
		fmt.Fprint(w, `<xml></xml>`)
		return
	}

	// 发送消息到 Agent
	msgEvent := &MessageEvent{
		Text:        msg.Content,
		MessageType: MsgText,
		MessageID:   msg.MsgID,
		Source: &SessionSource{
			Platform: PlatformWeCom,
			ChatID:   a.corpID + ":" + msg.FromUserName,
			UserID:   msg.FromUserName,
			ChatType: "dm",
		},
	}

	select {
		case a.msgCh <- msgEvent:
		default:
			slog.Warn("[WeComCallback] message channel full, dropping message")
		}

	// 返回空响应（使用主动发送 API 回复）
	w.Header().Set("Content-Type", "text/xml")
	fmt.Fprint(w, `<xml></xml>`)
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

func (a *WeComCallbackAdapter) generateSignature(timestamp, nonce, echostr string) string {
	// 排序参数
	params := []string{a.token, timestamp, nonce, echostr}
	sort.Strings(params)

	// 拼接并 SHA1
	str := strings.Join(params, "")
	hash := sha1.Sum([]byte(str))
	return fmt.Sprintf("%x", hash)
}

func (a *WeComCallbackAdapter) getAccessToken(ctx context.Context) (string, error) {
	u, _ := url.Parse("https://qyapi.weixin.qq.com/cgi-bin/gettoken")
	q := u.Query()
	q.Set("corpid", a.corpID)
	q.Set("corpsecret", a.encodingAESKey)
	u.RawQuery = q.Encode()
	apiURL := u.String()

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if errcode, ok := result["errcode"].(float64); ok && errcode != 0 {
		errmsg, _ := result["errmsg"].(string)
		return "", fmt.Errorf("企业微信 API 错误: %s", errmsg)
	}

	accessToken, ok := result["access_token"].(string)
	if !ok {
		return "", fmt.Errorf("无法获取 access_token")
	}

	return accessToken, nil
}

// ───────────────────────────── 自注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&AdapterEntry{
		Platform: PlatformWeCom,
		Name:     "WeCom Callback",
		Factory:  func() PlatformAdapter { return NewWeComCallbackAdapter(nil) },
	})
}
