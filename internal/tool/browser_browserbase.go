// Browserbase 云浏览器后端 -- 通过 Browserbase API 管理远程浏览器会话。
//
// Browserbase 提供云端 Chrome 浏览器实例，通过 CDP WebSocket 连接控制。
// 适用于无本地 Chrome 环境、需要特定地理位置 IP、或需要并行多浏览器的场景。
//
// 配置方式（环境变量）:
//   - BROWSERBASE_API_KEY:    API 密钥（必填）
//   - BROWSERBASE_PROJECT_ID: 项目 ID（可选）
//   - BROWSERBASE_REGION:     部署区域，如 "us-west-2" / "eu-central-1"（可选）
//
// API 文档: https://docs.browserbase.com
package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"
)

// BrowserbaseConfig Browserbase 云浏览器配置。
type BrowserbaseConfig struct {
	APIKey    string // BROWSERBASE_API_KEY 环境变量
	ProjectID string // BROWSERBASE_PROJECT_ID 环境变量
	Region    string // 部署区域，如 "us-west-2" / "eu-central-1"
}

// BrowserbaseSession Browserbase 云浏览器会话。
// 封装了与 Browserbase API 的交互，提供 CDP WebSocket 连接地址。
type BrowserbaseSession struct {
	sessionID string           // Browserbase 会话 ID
	cdpURL    string           // CDP WebSocket URL，用于 rod 连接
	config    BrowserbaseConfig
	client    *http.Client
}

const (
	// browserbaseAPIBase Browserbase API 基础地址。
	browserbaseAPIBase = "https://api.browserbase.com/v1"
)

// NewBrowserbaseSession 创建 Browserbase 云浏览器会话。
//
// 通过 Browserbase API 创建远程浏览器实例，返回可用于 rod 连接的会话对象。
// 创建成功后可通过 CDPWebSocketURL() 获取 CDP 连接地址。
//
// 使用完毕后必须调用 Close() 释放云端资源。
func NewBrowserbaseSession(ctx context.Context, cfg BrowserbaseConfig) (*BrowserbaseSession, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("Browserbase API Key 不能为空")
	}

	client := &http.Client{Timeout: 30 * time.Second}

	// 构建创建会话请求体
	body := map[string]any{}
	if cfg.ProjectID != "" {
		body["projectId"] = cfg.ProjectID
	}
	if cfg.Region != "" {
		body["region"] = cfg.Region
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}

	// 调用 Browserbase 创建会话 API
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		browserbaseAPIBase+"/sessions", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("创建 HTTP 请求失败: %w", err)
	}
	req.Header.Set("X-BB-Api-Key", cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("调用 Browserbase 创建会话 API 失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 Browserbase 响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("Browserbase 创建会话失败 (HTTP %d): %s",
			resp.StatusCode, string(respBody))
	}

	// 解析响应，提取会话 ID 和 CDP 连接 URL
	var result struct {
		ID         string `json:"id"`
		ConnectURL string `json:"connectUrl"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("解析 Browserbase 响应失败: %w", err)
	}

	if result.ID == "" {
		return nil, fmt.Errorf("Browserbase 未返回会话 ID")
	}
	if result.ConnectURL == "" {
		return nil, fmt.Errorf("Browserbase 未返回 CDP 连接 URL")
	}

	slog.Info("Browserbase cloud browser session created",
		"session_id", result.ID,
		"region", cfg.Region,
	)

	return &BrowserbaseSession{
		sessionID: result.ID,
		cdpURL:    result.ConnectURL,
		config:    cfg,
		client:    client,
	}, nil
}

// CDPWebSocketURL 返回 CDP WebSocket URL，用于 rod 连接远程浏览器。
func (s *BrowserbaseSession) CDPWebSocketURL() string {
	return s.cdpURL
}

// SessionID 返回 Browserbase 会话 ID。
func (s *BrowserbaseSession) SessionID() string {
	return s.sessionID
}

// Close 关闭 Browserbase 云浏览器会话，释放云端资源。
func (s *BrowserbaseSession) Close(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		browserbaseAPIBase+"/sessions/"+s.sessionID, nil)
	if err != nil {
		return fmt.Errorf("创建关闭请求失败: %w", err)
	}
	req.Header.Set("X-BB-Api-Key", s.config.APIKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("调用 Browserbase 关闭会话 API 失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Browserbase 关闭会话失败 (HTTP %d): %s",
			resp.StatusCode, string(body))
	}

	slog.Info("Browserbase cloud browser session closed", "session_id", s.sessionID)
	return nil
}

// loadBrowserbaseConfig 从环境变量加载 Browserbase 配置。
// 如果 BROWSERBASE_API_KEY 存在则返回配置和 true，否则返回零值和 false。
func loadBrowserbaseConfig() (BrowserbaseConfig, bool) {
	apiKey := os.Getenv("BROWSERBASE_API_KEY")
	if apiKey == "" {
		return BrowserbaseConfig{}, false
	}
	return BrowserbaseConfig{
		APIKey:    apiKey,
		ProjectID: os.Getenv("BROWSERBASE_PROJECT_ID"),
		Region:    os.Getenv("BROWSERBASE_REGION"),
	}, true
}

// isBrowserbaseConfigured 检查是否配置了 Browserbase 环境变量。
func isBrowserbaseConfigured() bool {
	return os.Getenv("BROWSERBASE_API_KEY") != ""
}
