package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// ───────────────────────────── 飞书云盘客户端 ─────────────────────────────

// FeishuDriveClient 飞书云盘 API 客户端接口。
type FeishuDriveClient interface {
	// Request 执行 API 请求。
	Request(ctx context.Context, method, uri string, body io.Reader, paths, queries map[string]string) ([]byte, error)
}

// 全局飞书云盘客户端
var (
	globalFeishuDriveClient   FeishuDriveClient
	globalFeishuDriveClientMu sync.RWMutex
)

// SetFeishuDriveClient 设置全局飞书云盘客户端。
func SetFeishuDriveClient(client FeishuDriveClient) {
	globalFeishuDriveClientMu.Lock()
	globalFeishuDriveClient = client
	globalFeishuDriveClientMu.Unlock()
}

// GetFeishuDriveClient 获取当前飞书云盘客户端。
func GetFeishuDriveClient() FeishuDriveClient {
	globalFeishuDriveClientMu.RLock()
	defer globalFeishuDriveClientMu.RUnlock()
	return globalFeishuDriveClient
}

// DefaultFeishuDriveClient 默认飞书云盘客户端实现。
type DefaultFeishuDriveClient struct {
	appID       string
	appSecret   string
	baseURL     string
	httpClient  *http.Client
	tokenCache  string
	tokenExpiry time.Time
	mu          sync.Mutex
}

// NewDefaultFeishuDriveClient 创建默认飞书云盘客户端。
func NewDefaultFeishuDriveClient(appID, appSecret string) *DefaultFeishuDriveClient {
	return &DefaultFeishuDriveClient{
		appID:      appID,
		appSecret:  appSecret,
		baseURL:    "https://open.feishu.cn",
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// GetTenantAccessToken 获取 tenant_access_token。
func (c *DefaultFeishuDriveClient) getTenantAccessToken(ctx context.Context) (string, error) {
	// 检查缓存
	c.mu.Lock()
	cached := c.tokenCache
	expiry := c.tokenExpiry
	c.mu.Unlock()
	if cached != "" && time.Now().Before(expiry) {
		return cached, nil
	}

	body := map[string]any{
		"app_id":     c.appID,
		"app_secret": c.appSecret,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("序列化请求体失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		c.baseURL+"/open-apis/auth/v3/tenant_access_token/internal",
		bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseSize))
	if err != nil {
		return "", err
	}

	var tokenResp struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"`
	}
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		return "", fmt.Errorf("解析响应失败: %w", err)
	}
	if tokenResp.Code != 0 {
		return "", fmt.Errorf("飞书 Token 错误: %s", tokenResp.Msg)
	}

	c.mu.Lock()
	c.tokenCache = tokenResp.TenantAccessToken
	expire := tokenResp.Expire - 300
	if expire < 60 {
		expire = 60
	}
	c.tokenExpiry = time.Now().Add(time.Duration(expire) * time.Second)
	c.mu.Unlock()

	return c.tokenCache, nil
}

// Request 执行 API 请求。
func (c *DefaultFeishuDriveClient) Request(ctx context.Context, method, uri string, body io.Reader, paths, queries map[string]string) ([]byte, error) {
	// 替换路径参数
	finalURI := uri
	for key, value := range paths {
		finalURI = strings.ReplaceAll(finalURI, ":"+key, value)
	}

	// 构建查询参数
	if len(queries) > 0 {
		params := url.Values{}
		for key, value := range queries {
			params.Add(key, value)
		}
		finalURI = finalURI + "?" + params.Encode()
	}

	token, err := c.getTenantAccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取 Token 失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+finalURI, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("飞书 API 错误 (HTTP %d)", resp.StatusCode)
	}

	return respBody, nil
}

// ───────────────────────────── 工具可用性检查 ─────────────────────────────

func checkFeishuDrive() bool {
	if GetFeishuDriveClient() != nil {
		return true
	}
	return os.Getenv("FEISHU_APP_ID") != "" && os.Getenv("FEISHU_APP_SECRET") != ""
}

// getClient 获取或创建飞书客户端。
func getDriveClient(ctx context.Context) (FeishuDriveClient, error) {
	client := GetFeishuDriveClient()
	if client != nil {
		return client, nil
	}

	appID := os.Getenv("FEISHU_APP_ID")
	appSecret := os.Getenv("FEISHU_APP_SECRET")
	if appID != "" && appSecret != "" {
		return NewDefaultFeishuDriveClient(appID, appSecret), nil
	}

	return nil, fmt.Errorf("飞书客户端不可用")
}

func validateFeishuToken(s string) error {
	if strings.ContainsAny(s, "/\\?#") || strings.Contains(s, "..") {
		return fmt.Errorf("包含非法字符")
	}
	return nil
}
