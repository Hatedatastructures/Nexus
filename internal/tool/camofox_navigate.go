// Package tool CamoFox 导航相关工具。
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// ───────────────────────────── CamoFox 导航工具 ─────────────────────────────

// CamofoxNavigateTool CamoFox 导航工具。
type CamofoxNavigateTool struct{}

func (t *CamofoxNavigateTool) Name() string        { return "browser_navigate" }
func (t *CamofoxNavigateTool) Toolset() string     { return "browser" }
func (t *CamofoxNavigateTool) Emoji() string       { return "🌐" }
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

	// URL 安全检查: 拦截 SSRF 风险地址 (与 rod 版本 browser_navigate 保持一致)
	if safe, reason := CheckURLSafety(url); !safe {
		slog.Warn("camofox navigate: URL safety check failed", "url", url, "reason", reason)
		return ToolError(fmt.Sprintf("URL 安全检查未通过: %s", reason)), nil
	}

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
			Snapshot  string `json:"snapshot"`
			RefsCount int    `json:"refsCount"`
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

func (t *CamofoxSnapshotTool) Name() string        { return "browser_snapshot" }
func (t *CamofoxSnapshotTool) Toolset() string     { return "browser" }
func (t *CamofoxSnapshotTool) Emoji() string       { return "📸" }
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
		Snapshot  string `json:"snapshot"`
		RefsCount int    `json:"refsCount"`
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
