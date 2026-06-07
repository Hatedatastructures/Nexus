// 参考文档: https://discord.com/developers/docs/resources/channel
// Discord 适配器通过 HTTP REST API 和 WebSocket 网关与 Discord 通信。
// 使用原生的 net/http 和 WebSocket (gorilla/websocket) 实现。
package platforms

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// ───────────────────────────── Discord 适配器 ─────────────────────────────

// DiscordAdapter 实现 Discord Bot API 适配器。
// 通过 HTTP REST API 发送/编辑消息，通过 WebSocket 接收消息。
type DiscordAdapter struct {
	token   string             // Bot Token
	client  *http.Client       // HTTP 客户端
	baseURL string             // API 基础 URL: https://discord.com/api/v10
	msgCh   chan *MessageEvent // 入站消息通道

	// WebSocket 网关状态
	lastSeq           int             // 最后收到的序列号 (用于重连)
	sessionID         string          // 当前会话 ID
	heartbeatInterval time.Duration   // 心跳间隔
	ws                *websocket.Conn // 当前 WebSocket 连接
	wsMu              sync.Mutex      // WebSocket 连接锁
	stateMu           sync.RWMutex    // 网关状态锁 (保护 lastSeq, sessionID, heartbeatInterval)
	wsRetryDelay      time.Duration   //
	closeOnce         sync.Once       //
	stopped           atomic.Bool     // 确保 msgCh 只关闭一次

	// 频道类型缓存 (channelID -> "dm"/"group")
	channelTypeCache map[string]string
	channelTypeMu    sync.RWMutex
}

const discordChannelCacheMaxSize = 500

// NewDiscordAdapter 创建 Discord 适配器。
// token 为 Discord Bot Token。
func NewDiscordAdapter(token string) *DiscordAdapter {
	return &DiscordAdapter{
		token:            token,
		client:           &http.Client{Timeout: 30 * time.Second},
		baseURL:          "https://discord.com/api/v10",
		msgCh:            make(chan *MessageEvent, 128),
		channelTypeCache: make(map[string]string),
	}
}

// Name 返回平台名称。
func (d *DiscordAdapter) Name() string { return "Discord" }

// PlatformType 返回平台类型枚举。
func (d *DiscordAdapter) PlatformType() Platform { return PlatformDiscord }

// MaxMessageLength 返回 Discord 最大消息长度 (2000)。
func (d *DiscordAdapter) MaxMessageLength() int { return 2000 }

// SupportsStreaming 返回是否支持流式编辑。
func (d *DiscordAdapter) SupportsStreaming() bool { return true }

// Connect 建立连接并开始监听消息。
// 通过 HTTP Gateway API 获取 WebSocket URL，然后建立 WebSocket 连接。
func (d *DiscordAdapter) Connect(ctx context.Context) (<-chan *MessageEvent, error) {
	// 启动 WebSocket 连接 goroutine
	go d.wsLoop(ctx)
	slog.Info("discord adapter connected")
	return d.msgCh, nil
}

// Disconnect 关闭消息通道。
func (d *DiscordAdapter) Disconnect(ctx context.Context) error {
	d.stopped.Store(true)
	d.closeOnce.Do(func() {
		close(d.msgCh)
	})
	slog.Info("discord adapter disconnected")
	return nil
}

// Send 发送文本消息到指定频道。
func (d *DiscordAdapter) Send(ctx context.Context, chatID string, content string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"content": content,
	}
	if opts != nil {
		if opts.ReplyToMsgID != "" {
			body["message_reference"] = map[string]string{
				"message_id": opts.ReplyToMsgID,
			}
		}
		if opts.Metadata != nil {
			if embeds, ok := opts.Metadata["embeds"]; ok {
				body["embeds"] = embeds
			}
		}
	}
	return d.doAPI(ctx, "POST", "/channels/"+chatID+"/messages", body)
}

// EditMessage 编辑已发送的消息。
func (d *DiscordAdapter) EditMessage(ctx context.Context, chatID string, messageID string, content string) (*SendResult, error) {
	body := map[string]any{
		"content": content,
	}
	return d.doAPI(ctx, "PATCH", "/channels/"+chatID+"/messages/"+messageID, body)
}

// DeleteMessage 删除消息。
func (d *DiscordAdapter) DeleteMessage(ctx context.Context, chatID string, messageID string) error {
	_, err := d.doAPI(ctx, "DELETE", "/channels/"+chatID+"/messages/"+messageID, nil)
	return err
}

// SendTyping 发送"正在输入..."指示器。
func (d *DiscordAdapter) SendTyping(ctx context.Context, chatID string) error {
	_, err := d.doAPI(ctx, "POST", "/channels/"+chatID+"/typing", nil)
	return err
}

// SendImage 发送图片。
func (d *DiscordAdapter) SendImage(ctx context.Context, chatID string, imageURL string, caption string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"content": caption,
		"embeds": []map[string]any{
			{"image": map[string]string{"url": imageURL}},
		},
	}
	if opts != nil && opts.ReplyToMsgID != "" {
		body["message_reference"] = map[string]string{"message_id": opts.ReplyToMsgID}
	}
	return d.doAPI(ctx, "POST", "/channels/"+chatID+"/messages", body)
}

// SendVoice 发送语音 (Discord 使用附件)。
func (d *DiscordAdapter) SendVoice(ctx context.Context, chatID string, audioPath string, opts *SendOptions) (*SendResult, error) {
	// Discord 音频需要 multipart 上传，这里返回不支持
	return &SendResult{Success: false, Error: "voice via raw HTTP not supported, use file upload"}, nil
}

// SendVideo 发送视频。
func (d *DiscordAdapter) SendVideo(ctx context.Context, chatID string, videoPath string, caption string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"content": caption,
	}
	return d.doAPI(ctx, "POST", "/channels/"+chatID+"/messages", body)
}

// SendDocument 发送文件 (Discord 附件)。
func (d *DiscordAdapter) SendDocument(ctx context.Context, chatID string, filePath string, caption string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "document via raw HTTP not supported, use file upload"}, nil
}

// Configure 注入 Discord 平台配置。
// settings 必须包含 "token" 键。
func (d *DiscordAdapter) Configure(settings map[string]any) error {
	token, _ := settings["token"].(string)
	if token == "" {
		return fmt.Errorf("discord 平台缺少 token 配置")
	}
	d.token = token
	d.client = &http.Client{Timeout: 30 * time.Second}
	d.baseURL = "https://discord.com/api/v10"
	d.msgCh = make(chan *MessageEvent, 128)
	d.channelTypeCache = make(map[string]string)
	return nil
}

// closeWS 关闭 WebSocket 连接。
func (d *DiscordAdapter) closeWS() {
	d.wsMu.Lock()
	defer d.wsMu.Unlock()
	if d.ws != nil {
		_ = d.ws.Close()
		d.ws = nil
	}
}

// getGatewayURL 获取 Gateway WebSocket URL。
func (d *DiscordAdapter) getGatewayURL(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", d.baseURL+"/gateway", nil)
	if err != nil {
		return "", fmt.Errorf("创建 gateway 请求失败: %w", err)
	}
	req.Header.Set("Authorization", "Bot "+d.token)

	resp, err := d.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseSize))
	if err != nil {
		return "", fmt.Errorf("读取 gateway 响应失败: %w", err)
	}
	var result struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", err
	}
	if result.URL == "" {
		return "", fmt.Errorf("discord gateway returned empty url (status=%d)", resp.StatusCode)
	}

	// 将 https:// 替换为 wss://
	wsURL := result.URL
	if strings.HasPrefix(wsURL, "https") {
		wsURL = "wss" + wsURL[5:]
	}
	return wsURL, nil
}

// chatTypeFromChannelID 根据频道 ID 判断聊天类型。
// 使用缓存避免每条消息都发起 HTTP 请求。
func (d *DiscordAdapter) chatTypeFromChannelID(channelID string) string {
	// 查缓存
	d.channelTypeMu.RLock()
	if ct, ok := d.channelTypeCache[channelID]; ok {
		d.channelTypeMu.RUnlock()
		return ct
	}
	d.channelTypeMu.RUnlock()

	// 缓存未命中，查询 API
	chatType := d.fetchChannelType(context.Background(), channelID)

	// 写入缓存
	d.channelTypeMu.Lock()
	if len(d.channelTypeCache) > discordChannelCacheMaxSize {
		// 简单淘汰: 清空重建
		d.channelTypeCache = make(map[string]string, len(d.channelTypeCache))
	}
	d.channelTypeCache[channelID] = chatType
	d.channelTypeMu.Unlock()

	return chatType
}

// fetchChannelType 通过 Discord API 查询频道类型。
func (d *DiscordAdapter) fetchChannelType(ctx context.Context, channelID string) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.baseURL+"/channels/"+channelID, nil)
	if err != nil {
		return "dm"
	}
	req.Header.Set("Authorization", "Bot "+d.token)

	resp, err := d.client.Do(req)
	if err != nil {
		return "dm"
	}
	defer func() { _ = resp.Body.Close() }()

	var ch struct {
		Type int `json:"type"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAPIResponseSize)).Decode(&ch); err != nil {
		return "dm"
	}

	switch ch.Type {
	case 1, 3:
		return "dm"
	default:
		return "group"
	}
}

// doAPI 发送 Discord API 请求。
func (d *DiscordAdapter) doAPI(ctx context.Context, method string, path string, body any) (*SendResult, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return &SendResult{Success: false, Error: err.Error()}, err
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, d.baseURL+path, bodyReader)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, err
	}
	req.Header.Set("Authorization", "Bot "+d.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error(), Retryable: true}, err
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseSize))
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, err
	}

	if resp.StatusCode >= 400 {
		slog.Warn("discord api error", "status", resp.StatusCode, "body", string(raw))
		errMsg := fmt.Sprintf("discord api error %d", resp.StatusCode)
		retryable := resp.StatusCode == 429 || resp.StatusCode >= 500
		return &SendResult{Success: false, Error: errMsg, Retryable: retryable}, nil
	}

	// 解析消息 ID
	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		slog.Warn("discord: failed to parse send response", "error", err)
	}

	return &SendResult{
		Success:   true,
		MessageID: result.ID,
	}, nil
}
