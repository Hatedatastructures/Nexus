// 参考文档: https://core.telegram.org/bots/api
// Telegram Bot API 通过 HTTP 长轮询 (getUpdates) 接收消息,
// 通过 HTTP POST 发送和编辑消息。
package platforms

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"sync"
	"time"
)

// ───────────────────────────── Telegram 适配器 ─────────────────────────────

// TelegramAdapter 实现 Telegram Bot API 的 HTTP 长轮询适配器。
// 使用 getUpdates 方法接收消息，通过 sendMessage/editMessageText 发送/编辑消息。
type TelegramAdapter struct {
	token         string             // Bot Token
	client        *http.Client       // HTTP 客户端
	baseURL       string             // API 基础 URL: https://api.telegram.org/bot{token}
	msgCh         chan *MessageEvent // 入站消息通道
	cancelFn      context.CancelFunc // 取消函数
	closeOnce     sync.Once          // 确保 msgCh 只关闭一次
	disconnectOnce sync.Once         // 确保 Disconnect 只执行一次
	mu             sync.Mutex        // 保护 cancelFn

	// 状态消息缓存: "chatID:statusKey" → messageID
	statusMsgIDs map[string]string
	statusMu     sync.Mutex
}

// NewTelegramAdapter 创建 Telegram 适配器。
// token 为 Telegram Bot Token。
func NewTelegramAdapter(token string) *TelegramAdapter {
	return &TelegramAdapter{
		token:         token,
		client:        &http.Client{Timeout: 30 * time.Second},
		baseURL:       "https://api.telegram.org/bot" + token,
		msgCh:         make(chan *MessageEvent, 128),
		statusMsgIDs:  make(map[string]string),
	}
}

// Name 返回平台名称。
func (t *TelegramAdapter) Name() string { return "Telegram" }

// PlatformType 返回平台类型枚举。
func (t *TelegramAdapter) PlatformType() Platform { return PlatformTelegram }

// MaxMessageLength 返回 Telegram 最大消息长度。
func (t *TelegramAdapter) MaxMessageLength() int { return 4096 }

// SupportsStreaming 返回是否支持流式编辑。
func (t *TelegramAdapter) SupportsStreaming() bool { return true }

// Connect 启动长轮询 goroutine 开始接收消息。
func (t *TelegramAdapter) Connect(ctx context.Context) (<-chan *MessageEvent, error) {
	ctx, cancel := context.WithCancel(ctx)

	t.mu.Lock()
	t.cancelFn = cancel
	t.mu.Unlock()

	go t.pollLoop(ctx)
	slog.Info("telegram adapter connected")
	return t.msgCh, nil
}

// Disconnect 取消上下文并等待 pollLoop 退出。
func (t *TelegramAdapter) Disconnect(_ context.Context) error {
	t.disconnectOnce.Do(func() {
		t.mu.Lock()
		if t.cancelFn != nil {
			t.cancelFn()
		}
		t.mu.Unlock()
		// pollLoop 会在 ctx.Done() 后关闭 msgCh
	})
	slog.Info("telegram adapter disconnected")
	return nil
}

// Send 发送文本消息。
func (t *TelegramAdapter) Send(ctx context.Context, chatID string, content string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"chat_id": chatID,
		"text":    content,
	}
	if opts != nil {
		if opts.ParseMode != "" {
			body["parse_mode"] = opts.ParseMode
		}
		if opts.ReplyToMsgID != "" {
			body["reply_to_message_id"] = opts.ReplyToMsgID
		}
	}
	return t.doPost(ctx, "/sendMessage", body)
}

// EditMessage 编辑已发送的消息。
func (t *TelegramAdapter) EditMessage(ctx context.Context, chatID string, messageID string, content string) (*SendResult, error) {
	body := map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
		"text":       content,
	}
	return t.doPost(ctx, "/editMessageText", body)
}

// DeleteMessage 删除消息。
func (t *TelegramAdapter) DeleteMessage(ctx context.Context, chatID string, messageID string) error {
	_, err := t.doPost(ctx, "/deleteMessage", map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
	})
	return err
}

// SendTyping 发送"正在输入..."指示器。
func (t *TelegramAdapter) SendTyping(ctx context.Context, chatID string) error {
	_, err := t.doPost(ctx, "/sendChatAction", map[string]any{
		"chat_id": chatID,
		"action":  "typing",
	})
	return err
}

// SendOrUpdateStatus 发送或更新状态消息。
// 如果同一 chatID+statusKey 已有缓存消息，则编辑而非追加新消息。
func (t *TelegramAdapter) SendOrUpdateStatus(ctx context.Context, chatID, statusKey, content string) error {
	key := chatID + ":" + statusKey

	t.statusMu.Lock()
	existingID, hasExisting := t.statusMsgIDs[key]
	t.statusMu.Unlock()

	if hasExisting {
		_, err := t.EditMessage(ctx, chatID, existingID, content)
		if err == nil {
			return nil
		}
		// Edit 失败（消息被删除等），回退到发送新消息
	}

	result, err := t.Send(ctx, chatID, content, nil)
	if err != nil {
		return err
	}

	if result != nil && result.MessageID != "" {
		t.statusMu.Lock()
		t.statusMsgIDs[key] = result.MessageID
		t.statusMu.Unlock()
	}
	return nil
}

// SendImage 发送图片。
func (t *TelegramAdapter) SendImage(ctx context.Context, chatID string, imageURL string, caption string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"chat_id": chatID,
		"photo":   imageURL,
		"caption": caption,
	}
	if opts != nil && opts.ReplyToMsgID != "" {
		body["reply_to_message_id"] = opts.ReplyToMsgID
	}
	return t.doPost(ctx, "/sendPhoto", body)
}

// SendVoice 发送语音。
func (t *TelegramAdapter) SendVoice(ctx context.Context, chatID string, audioPath string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"chat_id": chatID,
		"voice":   audioPath,
	}
	if opts != nil && opts.ReplyToMsgID != "" {
		body["reply_to_message_id"] = opts.ReplyToMsgID
	}
	return t.doPost(ctx, "/sendVoice", body)
}

// SendVideo 发送视频。
func (t *TelegramAdapter) SendVideo(ctx context.Context, chatID string, videoPath string, caption string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"chat_id": chatID,
		"video":   videoPath,
		"caption": caption,
	}
	if opts != nil && opts.ReplyToMsgID != "" {
		body["reply_to_message_id"] = opts.ReplyToMsgID
	}
	return t.doPost(ctx, "/sendVideo", body)
}

// SendDocument 发送文件。
func (t *TelegramAdapter) SendDocument(ctx context.Context, chatID string, filePath string, caption string, opts *SendOptions) (*SendResult, error) {
	body := map[string]any{
		"chat_id":  chatID,
		"document": filePath,
		"caption":  caption,
	}
	if opts != nil && opts.ReplyToMsgID != "" {
		body["reply_to_message_id"] = opts.ReplyToMsgID
	}
	return t.doPost(ctx, "/sendDocument", body)
}

// ───────────────────────────── 内部方法 ─────────────────────────────

// pollLoop 长轮询循环。
func (t *TelegramAdapter) pollLoop(ctx context.Context) {
	defer t.closeOnce.Do(func() { close(t.msgCh) }) // pollLoop 退出时安全关闭 channel

	offset := 0
	retryDelay := time.Second

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		updates, err := t.getUpdates(ctx, offset)
		if err != nil {
			slog.Warn("telegram getUpdates failed", "err", err)
			if retryDelay < 30*time.Second {
				retryDelay = retryDelay * 2
			}
			jitter := time.Duration(rand.Int63n(500)) * time.Millisecond
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryDelay + jitter):
			}
			continue
		}
		retryDelay = time.Second

		for _, upd := range updates {
			if upd.UpdateID >= offset {
				offset = upd.UpdateID + 1
			}
			msg := t.parseMessage(&upd)
			if msg != nil {
				select {
				case <-ctx.Done():
					return
				case t.msgCh <- msg:
				}
			}
		}
	}
}

// getUpdates 获取更新。
func (t *TelegramAdapter) getUpdates(ctx context.Context, offset int) ([]update, error) {
	body := map[string]any{
		"offset":  offset,
		"timeout": 30,
	}
	resp, err := t.doRequest(ctx, "POST", "/getUpdates", body)
	if err != nil {
		return nil, err
	}

	var result struct {
		OK     bool     `json:"ok"`
		Result []update `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(bytes.NewReader(resp), 10<<20)).Decode(&result); err != nil {
		return nil, err
	}
	return result.Result, nil
}

// parseMessage 将 Telegram Update 转换为 MessageEvent。
func (t *TelegramAdapter) parseMessage(upd *update) *MessageEvent {
	msg := upd.Message
	if msg == nil {
		return nil
	}

	source := &SessionSource{
		Platform: PlatformTelegram,
		ChatID:   formatInt(msg.Chat.ID),
		UserName: msg.From.Username,
		UserID:   formatInt(msg.From.ID),
		// 标记发送者是否为机器人
	}

	// 过滤机器人自身消息
	if msg.From.IsBot {
		return nil
	}

	switch msg.Chat.Type {
	case "private":
		source.ChatType = "dm"
	case "group":
		source.ChatType = "group"
	case "supergroup":
		source.ChatType = "group"
	case "channel":
		source.ChatType = "channel"
	}
	if msg.Chat.Title != "" {
		source.ChatName = msg.Chat.Title
	} else {
		source.ChatName = msg.From.FirstName
	}

	event := &MessageEvent{
		Source:    source,
		MessageID: formatInt(msg.MessageID),
		Timestamp: time.Unix(msg.Date, 0),
	}

	// 解析消息类型和内容
	// 过滤 /start 命令和空消息
	if msg.Text != "" {
		if msg.Text == "/start" || msg.Text == "/start@" + msg.From.Username {
			return nil
		}
		event.Text = msg.Text
		event.MessageType = MsgText
	} else if msg.Photo != nil && len(msg.Photo) > 0 {
		event.MessageType = MsgPhoto
		event.Text = msg.Caption
	} else if msg.Voice != nil {
		event.MessageType = MsgVoice
	} else if msg.Video != nil {
		event.MessageType = MsgVideo
		event.Text = msg.Caption
	} else if msg.Document != nil {
		event.MessageType = MsgDocument
		event.Text = msg.Caption
	} else if msg.Sticker != nil {
		event.MessageType = MsgSticker
	}

	if msg.ReplyToMessage != nil && msg.ReplyToMessage.Text != "" {
		event.ReplyToMsgID = formatInt(msg.ReplyToMessage.MessageID)
		event.ReplyToText = msg.ReplyToMessage.Text
	}

	return event
}

// doPost 发送 POST 请求并解析响应。
func (t *TelegramAdapter) doPost(ctx context.Context, method string, body map[string]any) (*SendResult, error) {
	resp, err := t.doRequest(ctx, "POST", method, body)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error(), Retryable: true}, err
	}
	return t.parseSendResponse(resp)
}

// doRequest 发送 HTTP 请求。
func (t *TelegramAdapter) doRequest(ctx context.Context, method string, path string, body any) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, t.baseURL+path, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseSize))
}

// parseSendResponse 解析发送/编辑响应。
func (t *TelegramAdapter) parseSendResponse(raw []byte) (*SendResult, error) {
	var resp struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      struct {
			MessageID int `json:"message_id"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return &SendResult{Success: false, Error: err.Error()}, err
	}
	if !resp.OK {
		return &SendResult{Success: false, Error: resp.Description, Retryable: true}, nil
	}
	return &SendResult{
		Success:   true,
		MessageID: formatInt(resp.Result.MessageID),
	}, nil
}

// ───────────────────────────── Telegram API 类型 ─────────────────────────────

type update struct {
	UpdateID int              `json:"update_id"`
	Message  *telegramMessage `json:"message"`
}

type telegramMessage struct {
	MessageID      int              `json:"message_id"`
	Date           int64            `json:"date"`
	Text           string           `json:"text"`
	Caption        string           `json:"caption"`
	From           telegramUser     `json:"from"`
	Chat           telegramChat     `json:"chat"`
	Photo          []telegramPhoto  `json:"photo"`
	Voice          *telegramVoice   `json:"voice"`
	Video          *telegramVideo   `json:"video"`
	Document       *telegramDoc     `json:"document"`
	Sticker        *telegramSticker `json:"sticker"`
	ReplyToMessage *telegramMessage `json:"reply_to_message"`
}

type telegramUser struct {
	ID        int    `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	IsBot     bool   `json:"is_bot"`
}

type telegramChat struct {
	ID    int    `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
}

type telegramPhoto struct {
	FileID string `json:"file_id"`
}

type telegramVoice struct {
	FileID string `json:"file_id"`
}

type telegramVideo struct {
	FileID string `json:"file_id"`
}

type telegramDoc struct {
	FileID string `json:"file_id"`
}

type telegramSticker struct {
	FileID string `json:"file_id"`
}

// ───────────────────────────── 自注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&AdapterEntry{
		Platform: PlatformTelegram,
		Name:     "Telegram",
		Factory:  func() PlatformAdapter { return &TelegramAdapter{} },
	})
}

// Configure 注入 Telegram 平台配置。
// settings 必须包含 "token" 键。
func (t *TelegramAdapter) Configure(settings map[string]any) error {
	token, _ := settings["token"].(string)
	if token == "" {
		return fmt.Errorf("telegram 平台缺少 token 配置")
	}
	t.token = token
	t.client = &http.Client{Timeout: 30 * time.Second}
	t.baseURL = "https://api.telegram.org/bot" + token
	t.msgCh = make(chan *MessageEvent, 128)
	t.statusMsgIDs = make(map[string]string)
	return nil
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

// formatInt 将整数格式化为字符串。
func formatInt(n int) string {
	if n == 0 {
		return "0"
	}
	return fmt.Sprintf("%d", n)
}
