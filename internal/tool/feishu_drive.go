// Package tool 提供飞书云盘工具。
// 支持文档评论操作：列出、回复、添加评论。
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"bytes"
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
		bytesReader2(bodyBytes))
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
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, err
	}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			slog.Warn("feishu drive API error response", "status", resp.StatusCode, "body", string(respBody))
			return nil, fmt.Errorf("飞书 API 错误 (HTTP %d)", resp.StatusCode)
		}

	return respBody, nil
}

// bytesReader2 创建 bytes Reader。
func bytesReader2(data []byte) io.Reader {
	return bytes.NewReader(data)
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

// ───────────────────────────── 列出评论工具 ─────────────────────────────

// FeishuDriveListCommentsTool 列出文档评论。
type FeishuDriveListCommentsTool struct{}

func (t *FeishuDriveListCommentsTool) Name() string { return "feishu_drive_list_comments" }
func (t *FeishuDriveListCommentsTool) Toolset() string { return "feishu_drive" }
func (t *FeishuDriveListCommentsTool) Emoji() string { return "💬" }
func (t *FeishuDriveListCommentsTool) MaxResultChars() int { return 50000 }

func (t *FeishuDriveListCommentsTool) Description() string {
	return "列出飞书文档上的评论。使用 is_whole=true 仅列出整文档评论。"
}

func (t *FeishuDriveListCommentsTool) IsAvailable() bool { return checkFeishuDrive() }

func (t *FeishuDriveListCommentsTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "feishu_drive_list_comments",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_token": map[string]any{
					"type":        "string",
					"description": "文档 file token。",
				},
				"file_type": map[string]any{
					"type":        "string",
					"description": "文件类型（默认 docx）。",
				},
				"is_whole": map[string]any{
					"type":        "boolean",
					"description": "为 true 时仅返回整文档评论。",
				},
				"page_size": map[string]any{
					"type":        "integer",
					"description": "每页评论数（最大 100）。",
				},
				"page_token": map[string]any{
					"type":        "string",
					"description": "分页 token。",
				},
			},
			"required": []string{"file_token"},
		},
	}
}

func validateFeishuToken(s string) error {
	if strings.ContainsAny(s, "/\\?#") || strings.Contains(s, "..") {
		return fmt.Errorf("包含非法字符")
	}
	return nil
}

func (t *FeishuDriveListCommentsTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	fileToken, ok := args["file_token"].(string)
	if !ok || strings.TrimSpace(fileToken) == "" {
		return ToolError("参数 file_token 是必填项。"), nil
	}
	if err := validateFeishuToken(fileToken); err != nil {
		return ToolError(fmt.Sprintf("file_token %s", err)), nil
	}

	fileType, _ := args["file_type"].(string)
	if fileType == "" {
		fileType = "docx"
	}

	isWhole, _ := args["is_whole"].(bool)
	pageSize := 100
		if v, ok := args["page_size"].(float64); ok && v > 0 && v <= 100 {
			pageSize = int(v)
		}
	pageToken, _ := args["page_token"].(string)

	client, err := getDriveClient(ctx)
	if err != nil {
		return ToolError("飞书客户端不可用。"), nil
	}

	queries := map[string]string{
		"file_type":   fileType,
		"user_id_type": "open_id",
		"page_size":   fmt.Sprintf("%d", pageSize),
	}
	if isWhole {
		queries["is_whole"] = "true"
	}
	if pageToken != "" {
		queries["page_token"] = pageToken
	}

	respBody, err := client.Request(ctx, "GET",
		"/open-apis/drive/v1/files/:file_token/comments",
		nil,
		map[string]string{"file_token": fileToken},
		queries)
	if err != nil {
		return ToolError(fmt.Sprintf("列出评论失败: %v", err)), nil
	}

	var apiResp struct {
		Code int            `json:"code"`
		Msg  string         `json:"msg"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return ToolError(fmt.Sprintf("解析响应失败: %v", err)), nil
	}
	if apiResp.Code != 0 {
		return ToolError(fmt.Sprintf("飞书 API 错误: code=%d msg=%s", apiResp.Code, apiResp.Msg)), nil
	}

	return ToolResult(apiResp.Data), nil
}

// ───────────────────────────── 列出评论回复工具 ─────────────────────────────

// FeishuDriveListRepliesTool 列出评论回复。
type FeishuDriveListRepliesTool struct{}

func (t *FeishuDriveListRepliesTool) Name() string { return "feishu_drive_list_comment_replies" }
func (t *FeishuDriveListRepliesTool) Toolset() string { return "feishu_drive" }
func (t *FeishuDriveListRepliesTool) Emoji() string { return "💬" }
func (t *FeishuDriveListRepliesTool) MaxResultChars() int { return 50000 }

func (t *FeishuDriveListRepliesTool) Description() string {
	return "列出飞书文档评论线程中的所有回复。"
}

func (t *FeishuDriveListRepliesTool) IsAvailable() bool { return checkFeishuDrive() }

func (t *FeishuDriveListRepliesTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "feishu_drive_list_comment_replies",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_token": map[string]any{
					"type":        "string",
					"description": "文档 file token。",
				},
				"comment_id": map[string]any{
					"type":        "string",
					"description": "评论 ID。",
				},
				"file_type": map[string]any{
					"type":        "string",
					"description": "文件类型（默认 docx）。",
				},
				"page_size": map[string]any{
					"type":        "integer",
					"description": "每页回复数（最大 100）。",
				},
				"page_token": map[string]any{
					"type":        "string",
					"description": "分页 token。",
				},
			},
			"required": []string{"file_token", "comment_id"},
		},
	}
}

func (t *FeishuDriveListRepliesTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	fileToken, ok := args["file_token"].(string)
	if !ok || strings.TrimSpace(fileToken) == "" {
		return ToolError("参数 file_token 是必填项。"), nil
	}
	if err := validateFeishuToken(fileToken); err != nil {
		return ToolError(fmt.Sprintf("file_token %s", err)), nil
	}
	commentID, ok := args["comment_id"].(string)
	if !ok || strings.TrimSpace(commentID) == "" {
		return ToolError("参数 comment_id 是必填项。"), nil
	}
	if err := validateFeishuToken(commentID); err != nil {
		return ToolError(fmt.Sprintf("comment_id %s", err)), nil
	}

	fileType, _ := args["file_type"].(string)
	if fileType == "" {
		fileType = "docx"
	}
		pageSize := 100
		if v, ok := args["page_size"].(float64); ok && v > 0 && v <= 100 {
			pageSize = int(v)
		}
	pageToken, _ := args["page_token"].(string)

	client, err := getDriveClient(ctx)
	if err != nil {
		return ToolError("飞书客户端不可用。"), nil
	}

	queries := map[string]string{
		"file_type":   fileType,
		"user_id_type": "open_id",
		"page_size":   fmt.Sprintf("%d", pageSize),
	}
	if pageToken != "" {
		queries["page_token"] = pageToken
	}

	respBody, err := client.Request(ctx, "GET",
		"/open-apis/drive/v1/files/:file_token/comments/:comment_id/replies",
		nil,
		map[string]string{"file_token": fileToken, "comment_id": commentID},
		queries)
	if err != nil {
		return ToolError(fmt.Sprintf("列出回复失败: %v", err)), nil
	}

	var apiResp struct {
		Code int            `json:"code"`
		Msg  string         `json:"msg"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return ToolError(fmt.Sprintf("解析响应失败: %v", err)), nil
	}
	if apiResp.Code != 0 {
		return ToolError(fmt.Sprintf("飞书 API 错误: code=%d msg=%s", apiResp.Code, apiResp.Msg)), nil
	}

	return ToolResult(apiResp.Data), nil
}

// ───────────────────────────── 回复评论工具 ─────────────────────────────

// FeishuDriveReplyCommentTool 回复评论。
type FeishuDriveReplyCommentTool struct{}

func (t *FeishuDriveReplyCommentTool) Name() string { return "feishu_drive_reply_comment" }
func (t *FeishuDriveReplyCommentTool) Toolset() string { return "feishu_drive" }
func (t *FeishuDriveReplyCommentTool) Emoji() string { return "✉️" }
func (t *FeishuDriveReplyCommentTool) MaxResultChars() int { return 5000 }

func (t *FeishuDriveReplyCommentTool) Description() string {
	return `回复飞书文档上的局部评论线程。用于局部（引用文本）评论。
整文档评论请使用 feishu_drive_add_comment。`
}

func (t *FeishuDriveReplyCommentTool) IsAvailable() bool { return checkFeishuDrive() }

func (t *FeishuDriveReplyCommentTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "feishu_drive_reply_comment",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_token": map[string]any{
					"type":        "string",
					"description": "文档 file token。",
				},
				"comment_id": map[string]any{
					"type":        "string",
					"description": "要回复的评论 ID。",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "回复文本内容（纯文本，不支持 markdown）。",
				},
				"file_type": map[string]any{
					"type":        "string",
					"description": "文件类型（默认 docx）。",
				},
			},
			"required": []string{"file_token", "comment_id", "content"},
		},
	}
}

func (t *FeishuDriveReplyCommentTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	fileToken, ok := args["file_token"].(string)
	if !ok || strings.TrimSpace(fileToken) == "" {
		return ToolError("参数 file_token 是必填项。"), nil
	}
	if err := validateFeishuToken(fileToken); err != nil {
		return ToolError(fmt.Sprintf("file_token %s", err)), nil
	}
	commentID, ok := args["comment_id"].(string)
	if !ok || strings.TrimSpace(commentID) == "" {
		return ToolError("参数 comment_id 是必填项。"), nil
	}
	if err := validateFeishuToken(commentID); err != nil {
		return ToolError(fmt.Sprintf("comment_id %s", err)), nil
	}
	content, ok := args["content"].(string)
	if !ok || strings.TrimSpace(content) == "" {
		return ToolError("参数 content 是必填项。"), nil
	}

	fileType, _ := args["file_type"].(string)
	if fileType == "" {
		fileType = "docx"
	}

	client, err := getDriveClient(ctx)
	if err != nil {
		return ToolError("飞书客户端不可用。"), nil
	}

	// 构建请求体
	reqBody := map[string]any{
		"content": map[string]any{
			"elements": []map[string]any{
				{
					"type": "text_run",
					"text_run": map[string]any{
						"text": strings.TrimSpace(content),
					},
				},
			},
		},
	}
	bodyBytes, err := json.Marshal(reqBody)
		if err != nil {
			return ToolError(fmt.Sprintf("序列化请求体失败: %v", err)), nil
		}

	respBody, err := client.Request(ctx, "POST",
		"/open-apis/drive/v1/files/:file_token/comments/:comment_id/replies",
		bytesReader2(bodyBytes),
		map[string]string{"file_token": fileToken, "comment_id": commentID},
		map[string]string{"file_type": fileType})
	if err != nil {
		return ToolError(fmt.Sprintf("回复评论失败: %v", err)), nil
	}

	var apiResp struct {
		Code int            `json:"code"`
		Msg  string         `json:"msg"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return ToolError(fmt.Sprintf("解析响应失败: %v", err)), nil
	}
	if apiResp.Code != 0 {
		return ToolError(fmt.Sprintf("飞书 API 错误: code=%d msg=%s", apiResp.Code, apiResp.Msg)), nil
	}

	slog.Info("feishu comment reply succeeded", "file_token", fileToken, "comment_id", commentID)
	return ToolResult(map[string]any{
		"success": true,
		"data":    apiResp.Data,
	}), nil
}

// ───────────────────────────── 添加评论工具 ─────────────────────────────

// FeishuDriveAddCommentTool 添加整文档评论。
type FeishuDriveAddCommentTool struct{}

func (t *FeishuDriveAddCommentTool) Name() string { return "feishu_drive_add_comment" }
func (t *FeishuDriveAddCommentTool) Toolset() string { return "feishu_drive" }
func (t *FeishuDriveAddCommentTool) Emoji() string { return "✉️" }
func (t *FeishuDriveAddCommentTool) MaxResultChars() int { return 5000 }

func (t *FeishuDriveAddCommentTool) Description() string {
	return `在飞书文档上添加新的整文档评论。
用于整文档评论，或 reply_comment 失败（错误码 1069302）时的备选方案。`
}

func (t *FeishuDriveAddCommentTool) IsAvailable() bool { return checkFeishuDrive() }

func (t *FeishuDriveAddCommentTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "feishu_drive_add_comment",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_token": map[string]any{
					"type":        "string",
					"description": "文档 file token。",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "评论文本内容（纯文本，不支持 markdown）。",
				},
				"file_type": map[string]any{
					"type":        "string",
					"description": "文件类型（默认 docx）。",
				},
			},
			"required": []string{"file_token", "content"},
		},
	}
}

func (t *FeishuDriveAddCommentTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	fileToken, ok := args["file_token"].(string)
	if !ok || strings.TrimSpace(fileToken) == "" {
		return ToolError("参数 file_token 是必填项。"), nil
	}
	if err := validateFeishuToken(fileToken); err != nil {
		return ToolError(fmt.Sprintf("file_token %s", err)), nil
	}
	content, ok := args["content"].(string)
	if !ok || strings.TrimSpace(content) == "" {
		return ToolError("参数 content 是必填项。"), nil
	}

	fileType, _ := args["file_type"].(string)
	if fileType == "" {
		fileType = "docx"
	}

	client, err := getDriveClient(ctx)
	if err != nil {
		return ToolError("飞书客户端不可用。"), nil
	}

	// 构建请求体
	reqBody := map[string]any{
		"file_type": fileType,
		"reply_elements": []map[string]any{
			{
				"type": "text",
				"text": strings.TrimSpace(content),
			},
		},
	}
	bodyBytes, err := json.Marshal(reqBody)
		if err != nil {
			return ToolError(fmt.Sprintf("序列化请求体失败: %v", err)), nil
		}

	respBody, err := client.Request(ctx, "POST",
		"/open-apis/drive/v1/files/:file_token/new_comments",
		bytesReader2(bodyBytes),
		map[string]string{"file_token": fileToken},
		nil)
	if err != nil {
		return ToolError(fmt.Sprintf("添加评论失败: %v", err)), nil
	}

	var apiResp struct {
		Code int            `json:"code"`
		Msg  string         `json:"msg"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return ToolError(fmt.Sprintf("解析响应失败: %v", err)), nil
	}
	if apiResp.Code != 0 {
		return ToolError(fmt.Sprintf("飞书 API 错误: code=%d msg=%s", apiResp.Code, apiResp.Msg)), nil
	}

	slog.Info("feishu comment added successfully", "file_token", fileToken)
	return ToolResult(map[string]any{
		"success": true,
		"data":    apiResp.Data,
	}), nil
}

// ───────────────────────────── init 注册 ─────────────────────────────

func init() {
	r := GetRegistry()
	r.Register(&FeishuDriveListCommentsTool{})
	r.Register(&FeishuDriveListRepliesTool{})
	r.Register(&FeishuDriveReplyCommentTool{})
	r.Register(&FeishuDriveAddCommentTool{})
}