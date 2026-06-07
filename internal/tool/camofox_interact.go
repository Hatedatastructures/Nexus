// Package tool CamoFox 交互相关工具（点击、输入、滚动、关闭）。
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
)

// ───────────────────────────── CamoFox 点击工具 ─────────────────────────────

// CamofoxClickTool CamoFox 点击工具。
type CamofoxClickTool struct{}

func (t *CamofoxClickTool) Name() string        { return "browser_click" }
func (t *CamofoxClickTool) Toolset() string     { return "browser" }
func (t *CamofoxClickTool) Emoji() string       { return "👆" }
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
	_ = json.Unmarshal(resp, &data) // 忽略解析错误，点击成功即可

	return ToolResult(map[string]any{
		"success": true,
		"clicked": ref,
		"url":     data.URL,
	}), nil
}

// ───────────────────────────── CamoFox 输入工具 ─────────────────────────────

// CamofoxTypeTool CamoFox 输入工具。
type CamofoxTypeTool struct{}

func (t *CamofoxTypeTool) Name() string        { return "browser_type" }
func (t *CamofoxTypeTool) Toolset() string     { return "browser" }
func (t *CamofoxTypeTool) Emoji() string       { return "⌨️" }
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

func (t *CamofoxScrollTool) Name() string        { return "browser_scroll" }
func (t *CamofoxScrollTool) Toolset() string     { return "browser" }
func (t *CamofoxScrollTool) Emoji() string       { return "📜" }
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

func (t *CamofoxCloseTool) Name() string        { return "browser_close" }
func (t *CamofoxCloseTool) Toolset() string     { return "browser" }
func (t *CamofoxCloseTool) Emoji() string       { return "🚪" }
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
