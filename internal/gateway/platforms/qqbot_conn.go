package platforms

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
)

// ───────────────────────────── 连接生命周期 ─────────────────────────────

// Connect 连接到 QQ Bot Gateway。
func (a *QQBotAdapter) Connect(ctx context.Context) (<-chan *MessageEvent, error) {
	if a.appID == "" || a.appSecret == "" {
		return nil, fmt.Errorf("QQBOT_APP_ID 和 QQBOT_APP_SECRET 是必填项")
	}

	// 获取 access token
	if err := a.getAccessToken(ctx); err != nil {
		return nil, fmt.Errorf("获取 access token 失败: %w", err)
	}

	// 获取 gateway URL
	gatewayURL, err := a.getGatewayURL(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取 gateway URL 失败: %w", err)
	}

	a.mu.Lock()
	a.running = true
	a.mu.Unlock()

	// 创建消息通道
	msgCh := make(chan *MessageEvent, 100)

	// 连接 WebSocket
	go a.connectWebSocket(ctx, gatewayURL, msgCh)

	slog.Info("[QQBot] connected", "app_id", a.appID)
	return msgCh, nil
}

// Disconnect 断开连接。
func (a *QQBotAdapter) Disconnect(ctx context.Context) error {
	a.mu.Lock()
	a.running = false
	a.connected = false
	conn := a.conn
	a.conn = nil
	a.mu.Unlock()

	if conn != nil {
		_ = conn.Close()
	}

	slog.Info("[QQBot] disconnected")
	return nil
}

// getAccessToken 获取 access token。
// 仅在写入 accessToken 时持锁，HTTP 调用在锁外执行。
func (a *QQBotAdapter) getAccessToken(ctx context.Context) error {
	body := map[string]any{
		"app_id":     a.appID,
		"app_secret": a.appSecret,
	}

	resp, err := a.callExternalAPI(ctx, qqbotTokenURL, body)
	if err != nil {
		return err
	}

	token := getString(resp, "access_token", "")
	if token == "" {
		return fmt.Errorf("access_token 未返回")
	}

	a.mu.Lock()
	a.accessToken = token
	a.mu.Unlock()

	return nil
}

// getGatewayURL 获取 WebSocket gateway URL。
func (a *QQBotAdapter) getGatewayURL(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", qqbotGatewayURL, nil)
	if err != nil {
		return "", err
	}

	a.mu.Lock()
	token := a.accessToken
	a.mu.Unlock()

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-App-Id", a.appID)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", err
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("解析响应失败: %w", err)
	}

	url := getString(result, "url", "")
	if url == "" {
		return "", fmt.Errorf("gateway URL 未返回")
	}

	return url, nil
}

// ───────────────────────────── WebSocket 连接与协议 ─────────────────────────────
// (见 qqbot_ws.go)
