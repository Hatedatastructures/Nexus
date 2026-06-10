// Package tool CamoFox 视觉分析工具与回调。
package tool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

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

// ───────────────────────────── CamoFox 视觉分析工具 ─────────────────────────────

// CamofoxVisionTool CamoFox 视觉分析工具。
type CamofoxVisionTool struct{}

func (t *CamofoxVisionTool) Name() string        { return "browser_vision" }
func (t *CamofoxVisionTool) Toolset() string     { return "browser" }
func (t *CamofoxVisionTool) Emoji() string       { return "👁️" }
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
	if err := os.WriteFile(screenshotPath, screenshot, 0600); err != nil {
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
				limit := len(snapData.Snapshot)
					if limit > 3000 {
						limit = 3000
					}
					annotationContext = "\n\n可访问性树（元素引用用于交互）:\n" + snapData.Snapshot[:limit]
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
			"success":         true,
			"analysis":        analysis,
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
