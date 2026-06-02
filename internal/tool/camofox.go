// Package tool 提供CamoFox 浏览器后端。
// CamoFox 是一个自托管的 Node.js 服务器，封装了Camoufox (Firefox分支)。
// 通过 REST API 提供浏览器操作接口。
package tool

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	nurl "net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ───────────────────────────── 配置 ─────────────────────────────

const (
	camofoxDefaultTimeout = 30 * time.Second
	camofoxSnapshotMaxChars = 80000
)

// 全局 VNC URL 缓存
var (
	camofoxVNCURL      string
	camofoxVNCURLChecked bool
	camofoxVNCURLMu    sync.Mutex
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
	defer resp.Body.Close()

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
	defer resp.Body.Close()

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
	defer resp.Body.Close()

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
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("CamoFox 错误 (HTTP %d)", resp.StatusCode)
	}

	return respBody, nil
}

// ───────────────────────────── CamoFox 导航工具 ─────────────────────────────

// CamofoxNavigateTool CamoFox 导航工具。
type CamofoxNavigateTool struct{}

func (t *CamofoxNavigateTool) Name() string { return "browser_navigate" }
func (t *CamofoxNavigateTool) Toolset() string { return "browser" }
func (t *CamofoxNavigateTool) Emoji() string { return "🌐" }
func (t *CamofoxNavigateTool) MaxResultChars() int { return 80000 }

func (t *CamofoxNavigateTool) Description() string {
	return "通过 CamoFox 浏览器导航到指定 URL。"
}

func (t *CamofoxNavigateTool) IsAvailable() bool { return IsCamofoxMode() }

func (t *CamofoxNavigateTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "browser_navigate",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "目标 URL。",
				},
			},
			"required": []string{"url"},
		},
	}
}

func (t *CamofoxNavigateTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	url, ok := args["url"].(string)
	if !ok || strings.TrimSpace(url) == "" {
		return ToolError("参数 url 是必填项。"), nil
	}
	url = strings.TrimSpace(url)

	taskID, _ := args["task_id"].(string)

	baseURL := GetCamofoxURL()
	if baseURL == "" {
		return ToolError("CAMOFOX_URL 未配置。"), nil
	}

	session := getCamofoxSession(taskID)
	var data map[string]any

	if session.TabID == "" {
		// 创建新标签页
		newSession, err := ensureCamofoxTab(ctx, taskID, url)
		if err != nil {
			return ToolError(fmt.Sprintf("创建标签页失败: %v", err)), nil
		}
		// 更新外部 session
		session = newSession
		data = map[string]any{"ok": true, "url": url}
	} else {
		// 导航现有标签页
		body := map[string]any{
			"userId": session.UserID,
			"url":    url,
		}
		resp, err := camofoxPost(ctx, baseURL+"/tabs/"+session.TabID+"/navigate", body, 60*time.Second)
		if err != nil {
			return ToolError(fmt.Sprintf("导航失败: %v", err)), nil
		}
		if err := json.Unmarshal(resp, &data); err != nil {
			return ToolError(fmt.Sprintf("解析响应失败: %v", err)), nil
		}
	}

	result := map[string]any{
		"success": true,
		"url":     data["url"],
		"title":   data["title"],
	}

	// 获取快照
	snapshotParams := map[string]string{"userId": session.UserID}
	snapResp, err := camofoxGet(ctx, baseURL+"/tabs/"+session.TabID+"/snapshot", snapshotParams, camofoxDefaultTimeout)
	if err == nil {
		var snapData struct {
			Snapshot string `json:"snapshot"`
			RefsCount int   `json:"refsCount"`
		}
		if json.Unmarshal(snapResp, &snapData) == nil {
			if len(snapData.Snapshot) > camofoxSnapshotMaxChars {
				snapData.Snapshot = snapData.Snapshot[:camofoxSnapshotMaxChars]
			}
			result["snapshot"] = snapData.Snapshot
			result["element_count"] = snapData.RefsCount
		}
	}

	return ToolResult(result), nil
}

// ───────────────────────────── CamoFox 快照工具 ─────────────────────────────

// CamofoxSnapshotTool CamoFox 快照工具。
type CamofoxSnapshotTool struct{}

func (t *CamofoxSnapshotTool) Name() string { return "browser_snapshot" }
func (t *CamofoxSnapshotTool) Toolset() string { return "browser" }
func (t *CamofoxSnapshotTool) Emoji() string { return "📸" }
func (t *CamofoxSnapshotTool) MaxResultChars() int { return 80000 }

func (t *CamofoxSnapshotTool) Description() string {
	return "获取 CamoFox 浏览器的可访问性树快照。"
}

func (t *CamofoxSnapshotTool) IsAvailable() bool { return IsCamofoxMode() }

func (t *CamofoxSnapshotTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "browser_snapshot",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"full": map[string]any{
					"type":        "boolean",
					"description": "返回完整快照（可能很大）。",
				},
			},
			"required": []string{},
		},
	}
}

func (t *CamofoxSnapshotTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	taskID, _ := args["task_id"].(string)
	session := getCamofoxSession(taskID)

	if session.TabID == "" {
		return ToolError("没有浏览器会话。请先调用 browser_navigate。"), nil
	}

	baseURL := GetCamofoxURL()
	params := map[string]string{"userId": session.UserID}

	resp, err := camofoxGet(ctx, baseURL+"/tabs/"+session.TabID+"/snapshot", params, camofoxDefaultTimeout)
	if err != nil {
		return ToolError(fmt.Sprintf("获取快照失败: %v", err)), nil
	}

	var data struct {
		Snapshot string `json:"snapshot"`
		RefsCount int   `json:"refsCount"`
	}
	if err := json.Unmarshal(resp, &data); err != nil {
		return ToolError(fmt.Sprintf("解析响应失败: %v", err)), nil
	}

	// 截断快照
	snapshot := data.Snapshot
	if len(snapshot) > camofoxSnapshotMaxChars {
		snapshot = snapshot[:camofoxSnapshotMaxChars]
	}

	return ToolResult(map[string]any{
		"success":       true,
		"snapshot":      snapshot,
		"element_count": data.RefsCount,
	}), nil
}

// ───────────────────────────── CamoFox 点击工具 ─────────────────────────────

// CamofoxClickTool CamoFox 点击工具。
type CamofoxClickTool struct{}

func (t *CamofoxClickTool) Name() string { return "browser_click" }
func (t *CamofoxClickTool) Toolset() string { return "browser" }
func (t *CamofoxClickTool) Emoji() string { return "👆" }
func (t *CamofoxClickTool) MaxResultChars() int { return 5000 }

func (t *CamofoxClickTool) Description() string {
	return "通过元素引用点击 CamoFox 浏览器中的元素。"
}

func (t *CamofoxClickTool) IsAvailable() bool { return IsCamofoxMode() }

func (t *CamofoxClickTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "browser_click",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"ref": map[string]any{
					"type":        "string",
					"description": "元素引用（从快照获取，可带 @ 前缀）。",
				},
			},
			"required": []string{"ref"},
		},
	}
}

func (t *CamofoxClickTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	ref, ok := args["ref"].(string)
	if !ok || strings.TrimSpace(ref) == "" {
		return ToolError("参数 ref 是必填项。"), nil
	}

	// 移除 @ 前缀
	ref = strings.TrimPrefix(ref, "@")

	taskID, _ := args["task_id"].(string)
	session := getCamofoxSession(taskID)

	if session.TabID == "" {
		return ToolError("没有浏览器会话。请先调用 browser_navigate。"), nil
	}

	baseURL := GetCamofoxURL()
	body := map[string]any{
		"userId": session.UserID,
		"ref":    ref,
	}

	resp, err := camofoxPost(ctx, baseURL+"/tabs/"+session.TabID+"/click", body, camofoxDefaultTimeout)
	if err != nil {
		return ToolError(fmt.Sprintf("点击失败: %v", err)), nil
	}

	var data struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(resp, &data); err != nil {
		// 忽略解析错误，点击成功即可
	}

	return ToolResult(map[string]any{
		"success": true,
		"clicked": ref,
		"url":     data.URL,
	}), nil
}

// ───────────────────────────── CamoFox 输入工具 ─────────────────────────────

// CamofoxTypeTool CamoFox 输入工具。
type CamofoxTypeTool struct{}

func (t *CamofoxTypeTool) Name() string { return "browser_type" }
func (t *CamofoxTypeTool) Toolset() string { return "browser" }
func (t *CamofoxTypeTool) Emoji() string { return "⌨️" }
func (t *CamofoxTypeTool) MaxResultChars() int { return 5000 }

func (t *CamofoxTypeTool) Description() string {
	return "在 CamoFox 浏览器中的元素内输入文本。"
}

func (t *CamofoxTypeTool) IsAvailable() bool { return IsCamofoxMode() }

func (t *CamofoxTypeTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "browser_type",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"ref": map[string]any{
					"type":        "string",
					"description": "元素引用。",
				},
				"text": map[string]any{
					"type":        "string",
					"description": "要输入的文本。",
				},
			},
			"required": []string{"ref", "text"},
		},
	}
}

func (t *CamofoxTypeTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	ref, ok := args["ref"].(string)
	if !ok || strings.TrimSpace(ref) == "" {
		return ToolError("参数 ref 是必填项。"), nil
	}
	text, ok := args["text"].(string)
	if !ok {
		return ToolError("参数 text 是必填项。"), nil
	}

	ref = strings.TrimPrefix(ref, "@")

	taskID, _ := args["task_id"].(string)
	session := getCamofoxSession(taskID)

	if session.TabID == "" {
		return ToolError("没有浏览器会话。请先调用 browser_navigate。"), nil
	}

	baseURL := GetCamofoxURL()
	body := map[string]any{
		"userId": session.UserID,
		"ref":    ref,
		"text":   text,
	}

	_, err := camofoxPost(ctx, baseURL+"/tabs/"+session.TabID+"/type", body, camofoxDefaultTimeout)
	if err != nil {
		return ToolError(fmt.Sprintf("输入失败: %v", err)), nil
	}

	return ToolResult(map[string]any{
		"success": true,
		"typed":   text,
		"element": ref,
	}), nil
}

// ───────────────────────────── CamoFox 滚动工具 ─────────────────────────────

// CamofoxScrollTool CamoFox 滚动工具。
type CamofoxScrollTool struct{}

func (t *CamofoxScrollTool) Name() string { return "browser_scroll" }
func (t *CamofoxScrollTool) Toolset() string { return "browser" }
func (t *CamofoxScrollTool) Emoji() string { return "📜" }
func (t *CamofoxScrollTool) MaxResultChars() int { return 2000 }

func (t *CamofoxScrollTool) Description() string {
	return "在 CamoFox 浏览器中滚动页面。"
}

func (t *CamofoxScrollTool) IsAvailable() bool { return IsCamofoxMode() }

func (t *CamofoxScrollTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "browser_scroll",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"direction": map[string]any{
					"type":        "string",
					"description": "滚动方向：up, down。",
				},
			},
			"required": []string{"direction"},
		},
	}
}

func (t *CamofoxScrollTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	direction, ok := args["direction"].(string)
	if !ok || strings.TrimSpace(direction) == "" {
		return ToolError("参数 direction 是必填项。"), nil
	}

	taskID, _ := args["task_id"].(string)
	session := getCamofoxSession(taskID)

	if session.TabID == "" {
		return ToolError("没有浏览器会话。请先调用 browser_navigate。"), nil
	}

	baseURL := GetCamofoxURL()
	body := map[string]any{
		"userId":    session.UserID,
		"direction": direction,
	}

	_, err := camofoxPost(ctx, baseURL+"/tabs/"+session.TabID+"/scroll", body, camofoxDefaultTimeout)
	if err != nil {
		return ToolError(fmt.Sprintf("滚动失败: %v", err)), nil
	}

	return ToolResult(map[string]any{
		"success":  true,
		"scrolled": direction,
	}), nil
}

// ───────────────────────────── CamoFox 关闭工具 ─────────────────────────────

// CamofoxCloseTool CamoFox 关闭工具。
type CamofoxCloseTool struct{}

func (t *CamofoxCloseTool) Name() string { return "browser_close" }
func (t *CamofoxCloseTool) Toolset() string { return "browser" }
func (t *CamofoxCloseTool) Emoji() string { return "🚪" }
func (t *CamofoxCloseTool) MaxResultChars() int { return 2000 }

func (t *CamofoxCloseTool) Description() string {
	return "关闭 CamoFox 浏览器会话。"
}

func (t *CamofoxCloseTool) IsAvailable() bool { return IsCamofoxMode() }

func (t *CamofoxCloseTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "browser_close",
		Description: t.Description(),
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
			"required":   []string{},
		},
	}
}

func (t *CamofoxCloseTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	taskID, _ := args["task_id"].(string)
	session := dropCamofoxSession(taskID)

	if session == nil {
		return ToolResult(map[string]any{"success": true, "closed": true}), nil
	}

	baseURL := GetCamofoxURL()
	_, err := camofoxDelete(ctx, baseURL+"/sessions/"+session.UserID, camofoxDefaultTimeout)
	if err != nil {
		slog.Warn("failed to close CamoFox session", "err", err)
	}

	return ToolResult(map[string]any{"success": true, "closed": true}), nil
}

// ───────────────────────────── CamoFox 视觉分析工具 ─────────────────────────────

// CamofoxVisionTool CamoFox 视觉分析工具。
type CamofoxVisionTool struct{}

func (t *CamofoxVisionTool) Name() string { return "browser_vision" }
func (t *CamofoxVisionTool) Toolset() string { return "browser" }
func (t *CamofoxVisionTool) Emoji() string { return "👁️" }
func (t *CamofoxVisionTool) MaxResultChars() int { return 50000 }

func (t *CamofoxVisionTool) Description() string {
	return "截取 CamoFox 浏览器屏幕截图并进行视觉 AI 分析。"
}

func (t *CamofoxVisionTool) IsAvailable() bool { return IsCamofoxMode() }

func (t *CamofoxVisionTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "browser_vision",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"question": map[string]any{
					"type":        "string",
					"description": "要分析的问题。",
				},
				"annotate": map[string]any{
					"type":        "boolean",
					"description": "是否包含可访问性树上下文。",
				},
			},
			"required": []string{"question"},
		},
	}
}

func (t *CamofoxVisionTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	question, ok := args["question"].(string)
	if !ok || strings.TrimSpace(question) == "" {
		return ToolError("参数 question 是必填项。"), nil
	}

	taskID, _ := args["task_id"].(string)
	session := getCamofoxSession(taskID)

	if session.TabID == "" {
		return ToolError("没有浏览器会话。请先调用 browser_navigate。"), nil
	}

	baseURL := GetCamofoxURL()
	params := map[string]string{"userId": session.UserID}

	// 获取截图
	screenshot, err := camofoxGetRaw(ctx, baseURL+"/tabs/"+session.TabID+"/screenshot", params, 60*time.Second)
	if err != nil {
		return ToolError(fmt.Sprintf("获取截图失败: %v", err)), nil
	}

	// 保存截图
	homeDir, _ := os.UserHomeDir()
	screenshotsDir := filepath.Join(homeDir, ".nexus", "browser_screenshots")
	if err := os.MkdirAll(screenshotsDir, 0755); err != nil {
		return ToolError(fmt.Sprintf("创建截图目录失败: %v", err)), nil
	}

	screenshotPath := filepath.Join(screenshotsDir, fmt.Sprintf("browser_screenshot_%s.png", uuid.New().String()[:8]))
	if err := os.WriteFile(screenshotPath, screenshot, 0644); err != nil {
		return ToolError(fmt.Sprintf("保存截图失败: %v", err)), nil
	}

	// 编码为 base64
	imgB64 := base64.StdEncoding.EncodeToString(screenshot)

	// 获取可访问性树上下文
	annotate, _ := args["annotate"].(bool)
	annotationContext := ""
	if annotate {
		snapResp, err := camofoxGet(ctx, baseURL+"/tabs/"+session.TabID+"/snapshot", params, camofoxDefaultTimeout)
		if err == nil {
			var snapData struct {
				Snapshot string `json:"snapshot"`
			}
			if json.Unmarshal(snapResp, &snapData) == nil && len(snapData.Snapshot) > 0 {
				annotationContext = "\n\n可访问性树（元素引用用于交互）:\n" + snapData.Snapshot[:3000]
			}
		}
	}

	// 调用视觉 LLM（需要 auxiliary client 支持）
	visionPrompt := fmt.Sprintf("分析此浏览器截图并回答: %s%s", question, annotationContext)

	// 如果有全局视觉回调，使用它
	if visionCallback := GetVisionCallback(); visionCallback != nil {
		analysis, err := visionCallback(ctx, visionPrompt, imgB64)
		if err != nil {
			return ToolError(fmt.Sprintf("视觉分析失败: %v", err)), nil
		}
		return ToolResult(map[string]any{
			"success":        true,
			"analysis":       analysis,
			"screenshot_path": screenshotPath,
		}), nil
	}

	// 无视觉回调，返回截图路径
	return ToolResult(map[string]any{
		"success":         true,
		"screenshot_path": screenshotPath,
		"note":            "视觉分析需要配置 VisionCallback。截图已保存。",
	}), nil
}

// ───────────────────────────── 视觉回调 ─────────────────────────────

// VisionCallback 视觉分析回调函数类型。
type VisionCallback func(ctx context.Context, prompt, imageBase64 string) (string, error)

var (
	globalVisionCallback VisionCallback
	visionCallbackMu     sync.RWMutex
)

// SetVisionCallback 设置全局视觉回调。
func SetVisionCallback(cb VisionCallback) {
	visionCallbackMu.Lock()
	defer visionCallbackMu.Unlock()
	globalVisionCallback = cb
}

// GetVisionCallback 获取当前视觉回调。
func GetVisionCallback() VisionCallback {
	visionCallbackMu.RLock()
	defer visionCallbackMu.RUnlock()
	return globalVisionCallback
}

// ───────────────────────────── init 注册 ─────────────────────────────

func init() {
	r := GetRegistry()
	r.Register(&CamofoxNavigateTool{})
	r.Register(&CamofoxSnapshotTool{})
	r.Register(&CamofoxClickTool{})
	r.Register(&CamofoxTypeTool{})
	r.Register(&CamofoxScrollTool{})
	r.Register(&CamofoxCloseTool{})
	r.Register(&CamofoxVisionTool{})
}