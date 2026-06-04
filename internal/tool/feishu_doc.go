// Package tool 提供飞书文档工具。
// 支持读取飞书/Lark 文档内容。
package tool

import (
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
)

// ───────────────────────────── 飞书客户端 ─────────────────────────────

// FeishuClient 飞书/Lark API 客户端接口。
type FeishuClient interface {
	// GetTenantAccessToken 获取 tenant_access_token。
	GetTenantAccessToken(ctx context.Context) (string, error)

	// Request 执行 API 请求。
	Request(ctx context.Context, method, uri string, body io.Reader, paths map[string]string) ([]byte, error)
}

// 全局飞书客户端
var (
	globalFeishuClient FeishuClient
	feishuClientMu     sync.RWMutex
)

// SetFeishuClient 设置全局飞书客户端。
func SetFeishuClient(client FeishuClient) {
	feishuClientMu.Lock()
	defer feishuClientMu.Unlock()
	globalFeishuClient = client
}

// GetFeishuClient 获取当前飞书客户端。
func GetFeishuClient() FeishuClient {
	feishuClientMu.RLock()
	defer feishuClientMu.RUnlock()
	return globalFeishuClient
}

// ───────────────────────────── 默认 HTTP 客户端 ─────────────────────────────

// DefaultFeishuClient 默认飞书客户端实现。
type DefaultFeishuClient struct {
	appID       string
	appSecret   string
	baseURL     string
	httpClient  *http.Client

	tokenMu     sync.Mutex
	tokenCache  string
	tokenExpiry time.Time
}

// NewDefaultFeishuClient 创建默认飞书客户端。
func NewDefaultFeishuClient(appID, appSecret string) *DefaultFeishuClient {
	return &DefaultFeishuClient{
		appID:      appID,
		appSecret:  appSecret,
		baseURL:    "https://open.feishu.cn",
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// GetTenantAccessToken 获取 tenant_access_token。
func (c *DefaultFeishuClient) GetTenantAccessToken(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	// 检查缓存的 token 是否有效
	if c.tokenCache != "" && time.Now().Before(c.tokenExpiry) {
		return c.tokenCache, nil
	}

	body := map[string]any{
		"app_id":     c.appID,
		"app_secret": c.appSecret,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
	return "", fmt.Errorf("序列化请求体失败: %w", err)
	}

	url := c.baseURL + "/open-apis/auth/v3/tenant_access_token/internal"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytesReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

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

	// 缓存 token（提前 5 分钟过期）
	expire := tokenResp.Expire - 300
	if expire < 60 {
		expire = 60
	}
	c.tokenCache = tokenResp.TenantAccessToken
	c.tokenExpiry = time.Now().Add(time.Duration(expire) * time.Second)

	return c.tokenCache, nil
}

// Request 执行 API 请求。
func (c *DefaultFeishuClient) Request(ctx context.Context, method, uri string, body io.Reader, paths map[string]string) ([]byte, error) {
	// 替换路径参数
	finalURI := uri
	for key, value := range paths {
		finalURI = strings.ReplaceAll(finalURI, ":"+key, value)
	}

	token, err := c.GetTenantAccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取 Token 失败: %w", err)
	}

	url := c.baseURL + finalURI
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024)) // 最大 1MB
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("飞书 API 错误 (HTTP %d)", resp.StatusCode)
	}

	return respBody, nil
}

// bytesReader 创建 bytes Reader。
func bytesReader(data []byte) *strings.Reader {
	return strings.NewReader(string(data))
}

// ───────────────────────────── 飞书文档读取工具 ─────────────────────────────

// FeishuDocReadTool 飞书文档读取工具。
type FeishuDocReadTool struct{}

func (t *FeishuDocReadTool) Name() string { return "feishu_doc_read" }
func (t *FeishuDocReadTool) Toolset() string { return "feishu_doc" }
func (t *FeishuDocReadTool) Emoji() string { return "📄" }
func (t *FeishuDocReadTool) MaxResultChars() int { return 50000 }

func (t *FeishuDocReadTool) Description() string {
	return "读取飞书/Lark 文档的完整内容为纯文本。当评论引用文本不足以提供上下文时使用。"
}

func (t *FeishuDocReadTool) IsAvailable() bool {
	// 检查全局客户端或环境变量
	if GetFeishuClient() != nil {
		return true
	}
	return os.Getenv("FEISHU_APP_ID") != "" && os.Getenv("FEISHU_APP_SECRET") != ""
}

func (t *FeishuDocReadTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "feishu_doc_read",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"doc_token": map[string]any{
					"type":        "string",
					"description": "文档 token（从文档 URL 或评论上下文获取）。",
				},
			},
			"required": []string{"doc_token"},
		},
	}
}

func (t *FeishuDocReadTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	docToken, ok := args["doc_token"].(string)
	if !ok || strings.TrimSpace(docToken) == "" {
		return ToolError("参数 doc_token 是必填项。"), nil
	}
	docToken = strings.TrimSpace(docToken)
	if strings.ContainsAny(docToken, "/\\?#") || strings.Contains(docToken, "..") {
		return ToolError("doc_token 包含非法字符。"), nil
	}

	client := GetFeishuClient()
	if client == nil {
		// 尝试从环境变量创建默认客户端
		appID := os.Getenv("FEISHU_APP_ID")
		appSecret := os.Getenv("FEISHU_APP_SECRET")
		if appID != "" && appSecret != "" {
			client = NewDefaultFeishuClient(appID, appSecret)
		} else {
			return ToolError("飞书客户端不可用（未配置 FEISHU_APP_ID/FEISHU_APP_SECRET）。"), nil
		}
	}

	// 调用飞书文档 API
	uri := "/open-apis/docx/v1/documents/:document_id/raw_content"
	paths := map[string]string{"document_id": docToken}

	respBody, err := client.Request(ctx, "GET", uri, nil, paths)
	if err != nil {
		slog.Error("feishu document read failed", "doc_token", docToken, "err", err)
		return ToolError(fmt.Sprintf("读取文档失败: %v", err)), nil
	}

	// 解析响应
	var apiResp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Content string `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return ToolError(fmt.Sprintf("解析响应失败: %v", err)), nil
	}
	if apiResp.Code != 0 {
		return ToolError(fmt.Sprintf("飞书 API 错误: code=%d msg=%s", apiResp.Code, apiResp.Msg)), nil
	}

	slog.Info("feishu document read succeeded", "doc_token", docToken, "content_len", len(apiResp.Data.Content))
	return ToolResult(map[string]any{
		"success": true,
		"content": apiResp.Data.Content,
	}), nil
}

// ───────────────────────────── init 注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&FeishuDocReadTool{})
}