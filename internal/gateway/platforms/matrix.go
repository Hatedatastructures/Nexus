// Matrix 平台适配器 — 通过 HTTP REST API 与 Matrix 服务器通信。
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
	"time"
)

// ───────────────────────────── Matrix 适配器 ─────────────────────────────

// MatrixAdapter 实现 Matrix 平台适配器。
type MatrixAdapter struct {
	homeServer   string
	accessToken  string
	userID       string
	client       *http.Client
	msgCh        chan *MessageEvent
	syncToken    string
	shutdown     chan struct{}
	shutdownOnce sync.Once
	closeOnce    sync.Once
	stateMu      sync.RWMutex
}

// NewMatrixAdapter 创建 Matrix 适配器实例。
func NewMatrixAdapter(homeServer, accessToken, userID string) *MatrixAdapter {
	return &MatrixAdapter{
		homeServer:  strings.TrimRight(homeServer, "/"),
		accessToken: accessToken,
		userID:      userID,
		client:      &http.Client{Timeout: 30 * time.Second},
		msgCh:       make(chan *MessageEvent, 128),
		shutdown:    make(chan struct{}),
	}
}

// Configure 注入 Matrix 平台配置。
// settings 应包含 "home_server"、"access_token" 和 "user_id" 键。
func (m *MatrixAdapter) Configure(settings map[string]any) error {
	homeServer, _ := settings["home_server"].(string)
	accessToken, _ := settings["access_token"].(string)
	userID, _ := settings["user_id"].(string)
	if homeServer == "" || accessToken == "" {
		return fmt.Errorf("matrix 平台缺少 home_server 或 access_token 配置")
	}
	m.homeServer = strings.TrimRight(homeServer, "/")
	m.accessToken = accessToken
	m.userID = userID
	m.client = &http.Client{Timeout: 30 * time.Second}
	m.msgCh = make(chan *MessageEvent, 128)
	m.shutdown = make(chan struct{})
	return nil
}

func (m *MatrixAdapter) Name() string            { return "Matrix" }
func (m *MatrixAdapter) PlatformType() Platform  { return PlatformMatrix }
func (m *MatrixAdapter) MaxMessageLength() int   { return 4096 }
func (m *MatrixAdapter) SupportsStreaming() bool { return false }

// Connect 连接到 Matrix 服务器并启动同步循环。
func (m *MatrixAdapter) Connect(ctx context.Context) (<-chan *MessageEvent, error) {
	// 获取初始 sync token
	if _, err := m.doSync(ctx, "", 0); err != nil {
		slog.Warn("matrix initial sync failed, will continue polling", "err", err)
	}

	go m.syncLoop(ctx)
	slog.Info("matrix adapter connected")
	return m.msgCh, nil
}

// Disconnect 停止同步循环。
func (m *MatrixAdapter) Disconnect(ctx context.Context) error {
	m.shutdownOnce.Do(func() { close(m.shutdown) })
	// syncLoop 退出时负责关闭 msgCh，这里不再重复关闭
	slog.Info("matrix adapter disconnected")
	return nil
}

// ───────────────────────────── 内部类型 ─────────────────────────────

// matrixAPIResponse 通用 API 响应。
type matrixAPIResponse struct {
	EventID string `json:"event_id"`
	TXNID   string `json:"txn_id"`
}

// syncResponse Matrix /sync 响应。
type syncResponse struct {
	NextBatch string `json:"next_batch"`
	Rooms     struct {
		Join map[string]struct {
			Timeline struct {
				Events []json.RawMessage `json:"events"`
			} `json:"timeline"`
		} `json:"join"`
	} `json:"rooms"`
}

// matrixEvent 解析后的 Matrix 事件。
type matrixEvent struct {
	Type           string          `json:"type"`
	Sender         string          `json:"sender"`
	EventID        string          `json:"event_id"`
	StateKey       *string         `json:"state_key"`
	Content        json.RawMessage `json:"content"`
	OriginServerTS int64           `json:"origin_server_ts"`
}

// ───────────────────────────── 内部方法 ─────────────────────────────

// doAPI 发送 Matrix API 请求。
func (m *MatrixAdapter) doAPI(ctx context.Context, method, path string, body any) (*matrixAPIResponse, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(data)
	}

	reqURL := m.homeServer + path
	req, err := http.NewRequestWithContext(ctx, method, reqURL, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.accessToken)

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseSize))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("matrix API error %d", resp.StatusCode)
	}

	var apiResp matrixAPIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("解析 API 响应失败: %w", err)
	}

	return &apiResp, nil
}
