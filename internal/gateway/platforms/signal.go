// Package platforms 提供 Signal 平台适配器。
// 通过 signal-cli daemon HTTP API 连接 Signal 网络。
package platforms

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// ───────────────────────────── 常量 ─────────────────────────────

const (
	signalDefaultHTTPURL    = "http://127.0.0.1:8080"
	signalMaxMessageLength  = 2000
	signalRequestTimeout    = 30 * time.Second
	signalSSETimeout        = 60 * time.Second
	signalDedupMaxSize      = 1000
	signalMaxAttachmentSize = 100 * 1024 * 1024 // 100MB
)

// ───────────────────────────── SignalAdapter ─────────────────────────────

// SignalAdapter Signal 平台适配器。
type SignalAdapter struct {
	httpURL        string
	account        string
	messageHandler func(*MessageEvent)

	// HTTP 客户端
	httpClient *http.Client

	running bool
	msgCh   chan *MessageEvent

	// 并发控制
	mu             sync.Mutex
	closeOnce      sync.Once
	dedup          *signalDeduplicator
	sentTimestamps map[int64]time.Time

	// 群信息缓存
}

// SignalGroupInfo 群信息。
type SignalGroupInfo struct {
	GroupID  string
	Name     string
	Members  []string
	IsMember bool
}

// NewSignalAdapter 创建 Signal 适配器。
func NewSignalAdapter(messageHandler func(*MessageEvent)) *SignalAdapter {
	httpURL := os.Getenv("SIGNAL_HTTP_URL")
	if httpURL == "" {
		httpURL = signalDefaultHTTPURL
	}

	account := os.Getenv("SIGNAL_ACCOUNT")

	return &SignalAdapter{
		httpURL:        httpURL,
		account:        account,
		messageHandler: messageHandler,
		httpClient:     &http.Client{Timeout: signalRequestTimeout},
		dedup:          newSignalDeduplicator(signalDedupMaxSize),
		sentTimestamps: make(map[int64]time.Time),
	}
}

// ───────────────────────────── 连接生命周期 ─────────────────────────────

// Connect 启动 SSE 监听。
func (a *SignalAdapter) Connect(ctx context.Context) (<-chan *MessageEvent, error) {
	if a.account == "" {
		return nil, fmt.Errorf("SIGNAL_ACCOUNT 是必填项")
	}

	a.mu.Lock()
	a.running = true
	a.mu.Unlock()

	// 创建消息通道
	msgCh := make(chan *MessageEvent, 100)
	a.msgCh = msgCh

	// 启动 SSE 监听
	go a.sseListener(ctx, msgCh)

	slog.Info("[Signal] connected", "account", a.account)
	return msgCh, nil
}

// Disconnect 断开连接。
func (a *SignalAdapter) Disconnect(ctx context.Context) error {
	a.mu.Lock()
	a.running = false
	a.mu.Unlock()

	// sseListener 退出时负责关闭 msgCh，这里不再重复关闭
	slog.Info("[Signal] disconnected")
	return nil
}

// ───────────────────────────── 发送消息 ─────────────────────────────

// Send 发送文本消息。
func (a *SignalAdapter) Send(ctx context.Context, chatID string, content string, opts *SendOptions) (*SendResult, error) {
	if chatID == "" {
		return &SendResult{Success: false, Error: "chat_id 是必填项"}, nil
	}

	// 转换 Markdown 为 Signal bodyRanges 样式
	content, bodyRanges := a.convertMarkdownToSignal(content)

	// 构建请求
	params := map[string]any{
		"account":   a.account,
		"recipient": chatID,
		"message":   content,
	}

	// 群聊需要使用 group_id
	if strings.HasPrefix(chatID, "group.") {
		params["group-id"] = strings.TrimPrefix(chatID, "group.")
		delete(params, "recipient")
	}

	// 添加样式
	if len(bodyRanges) > 0 {
		params["bodyRanges"] = bodyRanges
	}

	// 调用 RPC
	resp, err := a.rpc(ctx, "send", params)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, nil
	}

	// 记录时间戳
	ts := getInt(resp, "timestamp", 0)
	a.markSentTimestamp(int64(ts))

	return &SendResult{Success: true, MessageID: fmt.Sprintf("%d", ts)}, nil
}

// convertMarkdownToSignal 转换 Markdown 为 Signal 样式。
func (a *SignalAdapter) convertMarkdownToSignal(content string) (string, []map[string]any) {
	var bodyRanges []map[string]any
	var result strings.Builder
	runes := []rune(content)
	runePos := 0
	i := 0
	for i < len(runes) {
		// Bold: **text**
		if i+1 < len(runes) && runes[i] == '*' && runes[i+1] == '*' {
			end := strings.Index(string(runes[i+2:]), "**")
			if end != -1 {
				end += i + 2
				start := runePos
				text := string(runes[i+2 : end])
				result.WriteString(text)
				textRunes := len([]rune(text))
				runePos += textRunes
				bodyRanges = append(bodyRanges, map[string]any{
					"start":  start,
					"length": textRunes,
					"style":  "BOLD",
				})
				i = end + 2
				continue
			}
		}
		// Italic: *text*
		if runes[i] == '*' && (i+1 >= len(runes) || runes[i+1] != '*') {
			if i > 0 && runes[i-1] == '*' {
				i++
				continue
			}
			end := strings.Index(string(runes[i+1:]), "*")
			if end != -1 {
				end += i + 1
				start := runePos
				text := string(runes[i+1 : end])
				result.WriteString(text)
				textRunes := len([]rune(text))
				runePos += textRunes
				bodyRanges = append(bodyRanges, map[string]any{
					"start":  start,
					"length": textRunes,
					"style":  "ITALIC",
				})
				i = end + 1
				continue
			}
		}
		result.WriteRune(runes[i])
		runePos++
		i++
	}
	return result.String(), bodyRanges
}

// SendImage 发送图片。
func (a *SignalAdapter) SendImage(ctx context.Context, chatID string, imageURL string, caption string, opts *SendOptions) (*SendResult, error) {
	text := caption
	if text == "" {
		text = "[Image] " + imageURL
	} else {
		text = caption + "\n" + imageURL
	}
	return a.Send(ctx, chatID, text, opts)
}

// SendTyping 发送正在输入指示。
func (a *SignalAdapter) SendTyping(ctx context.Context, chatID string) error {
	params := map[string]any{
		"account":   a.account,
		"recipient": chatID,
	}
	_, err := a.rpc(ctx, "typing", params)
	return err
}

// ───────────────────────────── RPC 调用 ─────────────────────────────

// rpc 调用 JSON-RPC 2.0。
func (a *SignalAdapter) rpc(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	rpcID, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	reqBody := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
		"id":      rpcID.String(),
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("序列化 RPC 请求失败: %w", err)
	}

	url := a.httpURL + "/api/v1/rpc"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("RPC 错误 (HTTP %d)", resp.StatusCode)
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	// 检查 RPC 错误
	if rpcErr := getMap(result, "error"); rpcErr != nil {
		errmsg := getString(rpcErr, "message", "RPC 错误")
		return nil, fmt.Errorf("RPC 错误: %s", errmsg)
	}

	return getMap(result, "result"), nil
}

// ───────────────────────────── 接口实现 ─────────────────────────────

// Name 返回适配器名称。
func (a *SignalAdapter) Name() string { return "Signal" }

// PlatformType 返回平台类型。
func (a *SignalAdapter) PlatformType() Platform { return PlatformSignal }

// EditMessage 编辑消息（Signal 不支持）。
func (a *SignalAdapter) EditMessage(ctx context.Context, chatID string, messageID string, content string) (*SendResult, error) {
	return &SendResult{Success: false, Error: "Signal 不支持编辑消息"}, nil
}

// DeleteMessage 删除消息（Signal 不支持）。
func (a *SignalAdapter) DeleteMessage(ctx context.Context, chatID string, messageID string) error {
	return fmt.Errorf("signal 不支持删除消息")
}

// SendVoice 发送语音。
func (a *SignalAdapter) SendVoice(ctx context.Context, chatID string, audioPath string, opts *SendOptions) (*SendResult, error) {
	params := map[string]any{
		"account":   a.account,
		"recipient": chatID,
		"attachments": []map[string]any{
			{"filename": audioPath},
		},
	}
	resp, err := a.rpc(ctx, "send", params)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, nil
	}
	ts := getInt(resp, "timestamp", 0)
	return &SendResult{Success: true, MessageID: fmt.Sprintf("%d", ts)}, nil
}

// SendVideo 发送视频。
func (a *SignalAdapter) SendVideo(ctx context.Context, chatID string, videoPath string, caption string, opts *SendOptions) (*SendResult, error) {
	params := map[string]any{
		"account":   a.account,
		"recipient": chatID,
		"message":   caption,
		"attachments": []map[string]any{
			{"filename": videoPath},
		},
	}
	resp, err := a.rpc(ctx, "send", params)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, nil
	}
	ts := getInt(resp, "timestamp", 0)
	return &SendResult{Success: true, MessageID: fmt.Sprintf("%d", ts)}, nil
}

// SendDocument 发送文件。
func (a *SignalAdapter) SendDocument(ctx context.Context, chatID string, filePath string, caption string, opts *SendOptions) (*SendResult, error) {
	params := map[string]any{
		"account":   a.account,
		"recipient": chatID,
		"message":   caption,
		"attachments": []map[string]any{
			{"filename": filePath},
		},
	}
	resp, err := a.rpc(ctx, "send", params)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, nil
	}
	ts := getInt(resp, "timestamp", 0)
	return &SendResult{Success: true, MessageID: fmt.Sprintf("%d", ts)}, nil
}

// MaxMessageLength 返回最大消息长度。
func (a *SignalAdapter) MaxMessageLength() int { return signalMaxMessageLength }

// SupportsStreaming 返回是否支持流式输出。
func (a *SignalAdapter) SupportsStreaming() bool { return false }

