package platforms

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ───────────────────────────── 常量 ─────────────────────────────

const (
	qqbotDefaultAPIURL     = "https://api.sgroup.qq.com"
	qqbotTokenURL          = "https://bots.qq.com/app/getAppAccessToken"
	qqbotGatewayURL        = "https://bots.qq.com/gateway"
	qqbotHeartbeatInterval = 30 * time.Second
	qqbotConnectTimeout    = 20 * time.Second
	qqbotRequestTimeout    = 15 * time.Second
	qqbotDedupMaxSize      = 1000
)

// WebSocket OpCode
const (
	qqbotOpDispatch       = 0
	qqbotOpHeartbeat      = 1
	qqbotOpIdentify       = 2
	qqbotOpResume         = 6
	qqbotOpReconnect      = 7
	qqbotOpInvalidSession = 9
	qqbotOpHello          = 10
	qqbotOpHeartbeatAck   = 11
)

// QQ Bot intents 精确位掩码
const (
	qqbotIntentC2CGroupAtMessages  = 1 << 25
	qqbotIntentPublicGuildMessages = 1 << 30
	qqbotIntentDirectMessage       = 1 << 12
	qqbotIntentInteraction         = 1 << 26
)

// 致命 close code: 不可恢复，停止重连
var qqbotFatalCloseCodes = map[int]bool{
	4001: true, 4002: true, 4010: true, 4011: true,
	4012: true, 4013: true, 4014: true,
}

// 会话清除 close code: 清空 session 后重连
var qqbotSessionClearCodes = map[int]bool{
	4006: true, 4007: true,
}

// 消息类型
const (
	qqbotMsgTypeText        = 0
	qqbotMsgTypeMarkdown    = 2
	qqbotMsgTypeMedia       = 7
	qqbotMsgTypeInputNotify = 6
)

// 媒体类型
const (
	qqbotMediaTypeImage = 1
	qqbotMediaTypeVideo = 2
	qqbotMediaTypeVoice = 3
	qqbotMediaTypeFile  = 4
)

// ───────────────────────────── QQBotAdapter ─────────────────────────────

// QQBotAdapter QQ Bot 平台适配器。
type QQBotAdapter struct {
	appID          string
	appSecret      string
	apiURL         string
	dmPolicy       string
	groupPolicy    string
	allowFrom      []string
	groupAllowFrom []string
	messageHandler func(*MessageEvent)

	// 认证信息
	accessToken string
	botOpenID   string

	// WebSocket 连接
	conn      *websocket.Conn
	sessionID string
	running   bool
	connected bool

	// 并发控制
	mu              sync.Mutex
	writeMu         sync.Mutex
	closeOnce       sync.Once
	dedup           *qqbotDeduplicator
	seqNo           int64
	lastHeartbeat   time.Time
	heartbeatCancel context.CancelFunc

	// HTTP 客户端
	httpClient *http.Client
}

// qqbotDeduplicator 消息去重器。
type qqbotDeduplicator struct {
	mu      sync.Mutex
	msgIDs  map[string]time.Time
	maxSize int
}

func newQQBotDeduplicator(maxSize int) *qqbotDeduplicator {
	return &qqbotDeduplicator{
		msgIDs:  make(map[string]time.Time),
		maxSize: maxSize,
	}
}

func (d *qqbotDeduplicator) isDuplicate(msgID string) bool {
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

// NewQQBotAdapter 创建 QQ Bot 适配器。
func NewQQBotAdapter(messageHandler func(*MessageEvent)) *QQBotAdapter {
	appID := os.Getenv("QQBOT_APP_ID")
	appSecret := os.Getenv("QQBOT_APP_SECRET")

	apiURL := os.Getenv("QQBOT_API_URL")
	if apiURL == "" {
		apiURL = qqbotDefaultAPIURL
	}

	dmPolicy := os.Getenv("QQBOT_DM_POLICY")
	if dmPolicy == "" {
		dmPolicy = "open"
	}

	groupPolicy := os.Getenv("QQBOT_GROUP_POLICY")
	if groupPolicy == "" {
		groupPolicy = "open"
	}

	return &QQBotAdapter{
		appID:          appID,
		appSecret:      appSecret,
		apiURL:         apiURL,
		dmPolicy:       dmPolicy,
		groupPolicy:    groupPolicy,
		allowFrom:      getEnvList("QQBOT_ALLOW_FROM"),
		groupAllowFrom: getEnvList("QQBOT_GROUP_ALLOW_FROM"),
		messageHandler: messageHandler,
		dedup:          newQQBotDeduplicator(qqbotDedupMaxSize),
		httpClient:     &http.Client{Timeout: qqbotRequestTimeout},
	}
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

func generateMsgID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", b)
}

// validateChatID 验证 chatID/groupOpenID 只含安全字符，防止路径注入。
func validateChatID(id string) error {
	for _, c := range id {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' && c != '-' && c != ':' {
			return fmt.Errorf("无效的 chat ID")
		}
	}
	return nil
}
