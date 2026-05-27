// Package platforms 提供 Signal 平台适配器。
// 通过 signal-cli daemon HTTP API 连接 Signal 网络。
package platforms

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
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
	httpURL   string
	account   string
	messageHandler func(*MessageEvent)

	// HTTP 客户端
	httpClient *http.Client

	// SSE 连接
	conn      *websocket.Conn
	running   bool
	connected bool
	msgCh     chan *MessageEvent

	// 并发控制
	mu          sync.Mutex
	closeOnce   sync.Once
	dedup       *signalDeduplicator
	sentTimestamps map[int64]bool

	// 群信息缓存
	groupInfoCache map[string]*SignalGroupInfo
}

// SignalGroupInfo 群信息。
type SignalGroupInfo struct {
	GroupID   string
	Name      string
	Members   []string
	IsMember  bool
}

// signalDeduplicator 消息去重器。
type signalDeduplicator struct {
	mu      sync.Mutex
	msgIDs  map[string]time.Time
	maxSize int
}

func newSignalDeduplicator(maxSize int) *signalDeduplicator {
	return &signalDeduplicator{
		msgIDs:  make(map[string]time.Time),
		maxSize: maxSize,
	}
}

func (d *signalDeduplicator) isDuplicate(msgID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, exists := d.msgIDs[msgID]; exists {
		return true
	}

	d.msgIDs[msgID] = time.Now()

	if len(d.msgIDs) > d.maxSize {
		for id, t := range d.msgIDs {
			if time.Since(t) > 5*time.Minute {
				delete(d.msgIDs, id)
			}
		}
	}

	return false
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
		sentTimestamps: make(map[int64]bool),
		groupInfoCache: make(map[string]*SignalGroupInfo),
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
	a.connected = true
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
	a.connected = false
	a.mu.Unlock()

	a.closeOnce.Do(func() { close(a.msgCh) })

	slog.Info("[Signal] disconnected")
	return nil
}

// ───────────────────────────── SSE 监听 ─────────────────────────────

// sseListener SSE 消息监听。
func (a *SignalAdapter) sseListener(ctx context.Context, msgCh chan *MessageEvent) {
	for {
		a.mu.Lock()
		running := a.running
		a.mu.Unlock()

		if !running {
			a.closeOnce.Do(func() { close(a.msgCh) })
			return
		}

		// SSE 连接
		url := a.httpURL + "/api/v1/receive/" + a.account

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			slog.Warn("[Signal] failed to create request", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}

		req.Header.Set("Accept", "text/event-stream")

		resp, err := a.httpClient.Do(req)
		if err != nil {
			slog.Warn("[Signal] SSE connection failed", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}

		// 解析 SSE 流
		a.parseSSEStream(resp.Body, msgCh)
		resp.Body.Close()

		// 连接断开，重连
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

// parseSSEStream 解析 SSE 流。
func (a *SignalAdapter) parseSSEStream(body io.ReadCloser, msgCh chan *MessageEvent) {
	decoder := json.NewDecoder(io.LimitReader(body, 100<<20))

	for {
		a.mu.Lock()
		running := a.running
		a.mu.Unlock()

		if !running {
			return
		}

		var envelope map[string]any
		if err := decoder.Decode(&envelope); err != nil {
			if err != io.EOF {
				slog.Debug("[Signal] SSE parse failed", "err", err)
			}
			return
		}

		// 处理信封
		event := a.handleEnvelope(envelope)
		if event != nil {
			select {
			case msgCh <- event:
			default:
				slog.Warn("[Signal] message channel full, dropping message")
			}
		}
	}
}

// handleEnvelope 处理 Signal 信封。
func (a *SignalAdapter) handleEnvelope(envelope map[string]any) *MessageEvent {
	// 检查是否是自己发送的消息（防循环）
	timestamp := getInt(envelope, "timestamp", 0)
	if a.isSentTimestamp(int64(timestamp)) {
		return nil
	}

	// 提取数据消息
	dataMessage := getMap(envelope, "data_message")
	if dataMessage == nil {
		return nil
	}

	// 提取文本
	text := getString(dataMessage, "message", "")
	if text == "" {
		return nil
	}

	// 提取发送者信息
	source := getMap(envelope, "source")
	sourceNumber := getString(source, "number", "")
	if sourceNumber == "" {
		sourceNumber = getString(envelope, "source", "")
	}

	// 确定聊天 ID 和类型
	chatID := sourceNumber
	chatType := "dm"

	groupInfo := getMap(dataMessage, "group_info")
	if groupInfo != nil {
		chatID = getString(groupInfo, "group_id", "")
		chatType = "group"

		// 去除群聊中的 @mention
		text = strings.Replace(text, "@"+a.account, "", -1)
		text = strings.TrimSpace(text)
	}

	// 去重
	msgID := fmt.Sprintf("%d", timestamp)
	if a.dedup.isDuplicate(msgID) {
		return nil
	}

	// 构建消息事件
	event := &MessageEvent{
		Text:        text,
		MessageType: MsgText,
		MessageID:   msgID,
		Source: &SessionSource{
			Platform: PlatformSignal,
			ChatID:   chatID,
			UserID:   sourceNumber,
			ChatType: chatType,
		},
		RawMessage: envelope,
	}

	// 提取附件
	attachments := getListAny(dataMessage, "attachments")
	if len(attachments) > 0 {
		for _, att := range attachments {
			if attMap, ok := att.(map[string]any); ok {
				contentType := getString(attMap, "content_type", "")
				if strings.HasPrefix(contentType, "image/") {
					event.MessageType = MsgPhoto
				} else if strings.HasPrefix(contentType, "audio/") {
					event.MessageType = MsgVoice
				}
				break
			}
		}
	}

	// 提取引用回复
	quote := getMap(dataMessage, "quote")
	if quote != nil {
		event.ReplyToMsgID = fmt.Sprintf("%d", getInt(quote, "timestamp", 0))
	}

	return event
}

// isSentTimestamp 检查是否是自己发送的时间戳。
func (a *SignalAdapter) isSentTimestamp(ts int64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sentTimestamps[ts]
}

// markSentTimestamp 记录发送时间戳。
func (a *SignalAdapter) markSentTimestamp(ts int64) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.sentTimestamps[ts] = true

	// 清理超过上限的条目：删除超过一半的旧记录
	if len(a.sentTimestamps) > signalDedupMaxSize {
		deleteCount := len(a.sentTimestamps) / 2
		deleted := 0
		for key := range a.sentTimestamps {
			delete(a.sentTimestamps, key)
			deleted++
			if deleted >= deleteCount {
				break
			}
		}
	}
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
		"account":  a.account,
		"recipient": chatID,
		"message":  content,
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
	i := 0
	for i < len(runes) {
		// Bold: **text**
		if i+1 < len(runes) && runes[i] == '*' && runes[i+1] == '*' {
			end := strings.Index(string(runes[i+2:]), "**")
			if end != -1 {
				end += i + 2
				start := result.Len()
				result.WriteString(string(runes[i+2 : end]))
				bodyRanges = append(bodyRanges, map[string]any{
					"start":  start,
					"length": end - i - 2,
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
				start := result.Len()
				result.WriteString(string(runes[i+1 : end]))
				bodyRanges = append(bodyRanges, map[string]any{
					"start":  start,
					"length": end - i - 1,
					"style":  "ITALIC",
				})
				i = end + 1
				continue
			}
		}
		result.WriteRune(runes[i])
		i++
	}
	return result.String(), bodyRanges
}

// SendImage 发送图片。
func (a *SignalAdapter) SendImage(ctx context.Context, chatID string, imageURL string, caption string, opts *SendOptions) (*SendResult, error) {
	// Signal 需要先下载并上传图片
	// 简化实现：发送图片 URL 作为文本
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
		"account":  a.account,
		"recipient": chatID,
	}
	_, err := a.rpc(ctx, "typing", params)
	return err
}

// ───────────────────────────── RPC 调用 ─────────────────────────────

// rpc 调用 JSON-RPC 2.0。
func (a *SignalAdapter) rpc(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	reqBody := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
		"id":      fmt.Sprintf("%d", time.Now().UnixNano()),
	}

	bodyBytes, _ := json.Marshal(reqBody)

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
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("RPC 错误 (HTTP %d): %s", resp.StatusCode, string(respBody))
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
	return fmt.Errorf("Signal 不支持删除消息")
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

// ───────────────────────────── 自注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&AdapterEntry{
		Platform: PlatformSignal,
		Name:     "Signal",
		Factory:  func() PlatformAdapter { return NewSignalAdapter(nil) },
	})
}