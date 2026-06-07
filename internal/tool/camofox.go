// Package tool 提供CamoFox 浏览器后端。
// CamoFox 是一个自托管的 Node.js 服务器，封装了Camoufox (Firefox分支)。
// 通过 REST API 提供浏览器操作接口。
package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	nurl "net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ───────────────────────────── 配置 ─────────────────────────────

const (
	camofoxDefaultTimeout   = 30 * time.Second
	camofoxSnapshotMaxChars = 80000
)

// GetCamofoxURL 获取 CamoFox 服务器 URL。
func GetCamofoxURL() string {
	return strings.TrimSuffix(os.Getenv("CAMOFOX_URL"), "/")
}

// IsCamofoxMode 检查是否使用 CamoFox 后端。
func IsCamofoxMode() bool {
	if strings.TrimSpace(os.Getenv("BROWSER_CDP_URL")) != "" {
		return false // CDP 连接优先
	}
	return GetCamofoxURL() != ""
}

// ───────────────────────────── 会话管理 ─────────────────────────────

// CamofoxSession CamoFox 会话信息。
type CamofoxSession struct {
	UserID     string `json:"user_id"`
	TabID      string `json:"tab_id"`
	SessionKey string `json:"session_key"`
	Managed    bool   `json:"managed"`
}

var (
	camofoxSessions   = make(map[string]*CamofoxSession)
	camofoxSessionsMu sync.Mutex
)

// getCamofoxSession 获取或创建 CamoFox 会话。
func getCamofoxSession(taskID string) *CamofoxSession {
	if taskID == "" {
		taskID = "default"
	}

	camofoxSessionsMu.Lock()
	defer camofoxSessionsMu.Unlock()

	if session, ok := camofoxSessions[taskID]; ok {
		return session
	}

	// 创建新会话
	suffix := taskID
	if len(suffix) > 16 {
		suffix = suffix[:16]
	}
	session := &CamofoxSession{
		UserID:     fmt.Sprintf("nexus_%s", uuid.New().String()[:10]),
		SessionKey: fmt.Sprintf("task_%s", suffix),
		Managed:    false,
	}
	camofoxSessions[taskID] = session
	return session
}

// ensureCamofoxTab 确保 CamoFox 会话有标签页。
func ensureCamofoxTab(ctx context.Context, taskID, url string) (*CamofoxSession, error) {
	session := getCamofoxSession(taskID)
	if session.TabID != "" {
		return session, nil
	}

	baseURL := GetCamofoxURL()
	body := map[string]any{
		"userId":     session.UserID,
		"sessionKey": session.SessionKey,
		"url":        url,
	}

	resp, err := camofoxPost(ctx, baseURL+"/tabs", body, camofoxDefaultTimeout)
	if err != nil {
		return nil, err
	}

	var data struct {
		TabID string `json:"tabId"`
	}
	if err := json.Unmarshal(resp, &data); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	session.TabID = data.TabID
	return session, nil
}

// dropCamofoxSession 移除会话。
func dropCamofoxSession(taskID string) *CamofoxSession {
	if taskID == "" {
		taskID = "default"
	}

	camofoxSessionsMu.Lock()
	defer camofoxSessionsMu.Unlock()

	session := camofoxSessions[taskID]
	delete(camofoxSessions, taskID)
	return session
}

// ───────────────────────────── HTTP 辅助函数 ─────────────────────────────

// camofoxClient HTTP 客户端。
var camofoxClient = &http.Client{Timeout: 60 * time.Second}

// camofoxPost POST 请求。
func camofoxPost(ctx context.Context, url string, body map[string]any, timeout time.Duration) ([]byte, error) {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := camofoxClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		slog.Warn("CamoFox API error", "status", resp.StatusCode, "body", string(respBody))
		return nil, fmt.Errorf("CamoFox 错误 (HTTP %d)", resp.StatusCode)
	}

	return respBody, nil
}

// camofoxGet GET 请求。
func camofoxGet(ctx context.Context, url string, params map[string]string, timeout time.Duration) ([]byte, error) {
	// 构建查询参数
	if len(params) > 0 {
		vals := nurl.Values{}
		for key, value := range params {
			vals.Set(key, value)
		}
		url = url + "?" + vals.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := camofoxClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024)) // 最大 5MB
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("CamoFox 错误 (HTTP %d)", resp.StatusCode)
	}

	return respBody, nil
}

// camofoxDelete DELETE 请求。
func camofoxDelete(ctx context.Context, url string, timeout time.Duration) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := camofoxClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("CamoFox 错误 (HTTP %d)", resp.StatusCode)
	}

	return respBody, nil
}

// camofoxGetRaw GET 请求返回原始二进制响应 (5MB 限制)。
// 不在错误信息中包含响应体，避免泄露敏感数据。
func camofoxGetRaw(ctx context.Context, url string, params map[string]string, timeout time.Duration) ([]byte, error) {
	if len(params) > 0 {
		vals := nurl.Values{}
		for key, value := range params {
			vals.Set(key, value)
		}
		url = url + "?" + vals.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := camofoxClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("CamoFox 错误 (HTTP %d)", resp.StatusCode)
	}

	return respBody, nil
}
