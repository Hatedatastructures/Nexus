// Package platforms 提供企业微信回调模式适配器。
// 通过 HTTP 回调接收企业微信消息，通过主动发送 API 回复。
package platforms

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
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
	corpSecret     string
	agentID        string
	token          string
	encodingAESKey string
	messageHandler func(*MessageEvent)
	httpClient     *http.Client
	msgCh          chan *MessageEvent
	running        bool
	closeOnce      sync.Once
	mu             sync.RWMutex // 保护 msgCh 写入/关闭
	httpServer     *http.Server
	webhookPort    int

	tokenMu     sync.Mutex
	accessToken string
	tokenExpiry time.Time
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
	corpSecret := os.Getenv("WECOM_CORP_SECRET")
	agentID := os.Getenv("WECOM_AGENT_ID")
	token := os.Getenv("WECOM_TOKEN")
	encodingAESKey := os.Getenv("WECOM_ENCODING_AES_KEY")

	webhookPort := 8083
	if p := os.Getenv("WECOM_WEBHOOK_PORT"); p != "" {
		_, _ = fmt.Sscanf(p, "%d", &webhookPort)
	}

	return &WeComCallbackAdapter{
		corpID:         corpID,
		corpSecret:     corpSecret,
		agentID:        agentID,
		token:          token,
		encodingAESKey: encodingAESKey,
		messageHandler: messageHandler,
		httpClient:     &http.Client{Timeout: wecomCallbackRequestTimeout},
		webhookPort:    webhookPort,
	}
}

// ───────────────────────────── PlatformAdapter 接口实现 ─────────────────────────────

func (a *WeComCallbackAdapter) Name() string            { return "WeCom Callback" }
func (a *WeComCallbackAdapter) PlatformType() Platform  { return PlatformWeCom }
func (a *WeComCallbackAdapter) MaxMessageLength() int   { return wecomCallbackMaxMessageLen }
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

	a.httpServer = &http.Server{Addr: fmt.Sprintf("%s:%d", func() string {
		bind := os.Getenv("WECOM_CALLBACK_BIND")
		if bind == "" {
			bind = "127.0.0.1"
		}
		return bind
	}(), a.webhookPort), Handler: mux}

	go func() {
		addr := a.httpServer.Addr
		if a.webhookPort != 443 && a.webhookPort != 8443 {
			slog.Warn("[WeCom Callback] webhook 使用 HTTP，建议启用 TLS", "addr", addr)
		}
		slog.Info("[WeCom Callback] webhook server started", "addr", addr)
		if err := a.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("[WeCom Callback] webhook server failed", "err", err)
		}
	}()

	slog.Info("[WeCom Callback] connected", "corp_id", a.corpID)
	return a.msgCh, nil
}

func (a *WeComCallbackAdapter) Disconnect(ctx context.Context) error {
	a.running = false
	if a.httpServer != nil {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_ = a.httpServer.Shutdown(shutdownCtx)
	}
	a.mu.Lock()
	a.closeOnce.Do(func() {
		if a.msgCh != nil {
			close(a.msgCh)
		}
	})
	a.mu.Unlock()
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

	sendURL, _ := url.Parse("https://qyapi.weixin.qq.com/cgi-bin/message/send")
	q := sendURL.Query()
	q.Set("access_token", accessToken)
	sendURL.RawQuery = q.Encode()
	apiURL := sendURL.String()

	payload := map[string]any{
		"touser":  userID,
		"msgtype": "text",
		"agentid": a.agentID,
		"text":    map[string]string{"content": content},
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return &SendResult{Success: false, Error: fmt.Sprintf("序列化消息失败: %v", err)}, nil
	}
	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(jsonData))
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, nil
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, nil
	}
	defer func() { _ = resp.Body.Close() }()

	var result map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return &SendResult{Success: false, Error: fmt.Sprintf("解析响应失败: %v", err)}, nil
	}

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

// ───────────────────────────── 回调处理与辅助函数 ─────────────────────────────
// (见 wecom_callback_handlers.go)
