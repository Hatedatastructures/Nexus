// Package tool 提供基于 rod 库的浏览器自动化工具。
// 本文件包含浏览器交互操作工具：点击、输入等。
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/go-rod/rod/lib/proto"
)

// ───────────────────────────── 浏览器点击工具 ─────────────────────────────

// BrowserClickTool 实现元素点击功能。
type BrowserClickTool struct{}

// Name 返回工具名称。
func (t *BrowserClickTool) Name() string { return "browser_click" }

// Description 返回工具描述。
func (t *BrowserClickTool) Description() string {
	return "点击页面上的指定元素。提供 CSS 选择器定位元素。"
}

// Toolset 返回工具所属工具集。
func (t *BrowserClickTool) Toolset() string { return "browser" }

// Emoji 返回工具图标。
func (t *BrowserClickTool) Emoji() string { return "👆" }

// IsAvailable 检查浏览器是否可用（本地 Chrome 或 Browserbase 云浏览器）。
func (t *BrowserClickTool) IsAvailable() bool {
	return browserCheck()
}

// MaxResultChars 返回结果最大字符数。
func (t *BrowserClickTool) MaxResultChars() int { return 5000 }

// Schema 返回工具的 JSON Schema。
func (t *BrowserClickTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "browser_click",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"selector": map[string]any{
					"type":        "string",
					"description": "CSS 选择器，用于定位要点击的元素",
				},
			},
			"required": []string{"selector"},
		},
	}
}

// Execute 执行浏览器点击。
func (t *BrowserClickTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	page, err := getPage(ctx)
	if err != nil {
		return ToolError(fmt.Sprintf("浏览器不可用: %v", err)), nil
	}

	selector, ok := args["selector"].(string)
	if !ok || selector == "" {
		return ToolError("参数 selector 是必填项且必须为字符串"), nil
	}

	el, elErr := page.Timeout(10 * time.Second).Element(selector)
	if elErr != nil {
		return ToolError(fmt.Sprintf("未找到元素 %s: %v", selector, elErr)), nil
	}

	if err := el.Timeout(5*time.Second).Click(proto.InputMouseButtonLeft, 1); err != nil {
		slog.Error("click element failed", "selector", selector, "err", err)
		return ToolError(fmt.Sprintf("点击失败: %v", err)), nil
	}

	// 等待可能的页面变化
	_ = page.Timeout(3 * time.Second).WaitLoad()

	result, err := json.Marshal(map[string]any{
		"output":   fmt.Sprintf("已点击元素: %s", selector),
		"selector": selector,
	})
	if err != nil {
		return ToolError(fmt.Sprintf("序列化结果失败: %v", err)), nil
	}
	return string(result), nil
}

// ───────────────────────────── 浏览器输入工具 ─────────────────────────────

// BrowserTypeTool 实现元素输入功能。
type BrowserTypeTool struct{}

// Name 返回工具名称。
func (t *BrowserTypeTool) Name() string { return "browser_type" }

// Description 返回工具描述。
func (t *BrowserTypeTool) Description() string {
	return "在输入框中输入文本。先清空原有内容，再输入新文本。"
}

// Toolset 返回工具所属工具集。
func (t *BrowserTypeTool) Toolset() string { return "browser" }

// Emoji 返回工具图标。
func (t *BrowserTypeTool) Emoji() string { return "⌨️" }

// IsAvailable 检查浏览器是否可用（本地 Chrome 或 Browserbase 云浏览器）。
func (t *BrowserTypeTool) IsAvailable() bool {
	return browserCheck()
}

// MaxResultChars 返回结果最大字符数。
func (t *BrowserTypeTool) MaxResultChars() int { return 5000 }

// Schema 返回工具的 JSON Schema。
func (t *BrowserTypeTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "browser_type",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"selector": map[string]any{
					"type":        "string",
					"description": "CSS 选择器，定位输入框元素",
				},
				"text": map[string]any{
					"type":        "string",
					"description": "要输入的文本内容",
				},
			},
			"required": []string{"selector", "text"},
		},
	}
}

// Execute 执行浏览器输入。
func (t *BrowserTypeTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	page, err := getPage(ctx)
	if err != nil {
		return ToolError(fmt.Sprintf("浏览器不可用: %v", err)), nil
	}

	selector, ok := args["selector"].(string)
	if !ok || selector == "" {
		return ToolError("参数 selector 是必填项且必须为字符串"), nil
	}
	text, ok := args["text"].(string)
	if !ok {
		return ToolError("参数 text 是必填项且必须为字符串"), nil
	}

	el, elErr := page.Timeout(10 * time.Second).Element(selector)
	if elErr != nil {
		return ToolError(fmt.Sprintf("未找到元素 %s: %v", selector, elErr)), nil
	}

	// 清空输入框并输入新文本
	if err := el.Timeout(5 * time.Second).Input(text); err != nil {
		slog.Error("input text failed", "selector", selector, "err", err)
		return ToolError(fmt.Sprintf("输入失败: %v", err)), nil
	}

	result, err := json.Marshal(map[string]any{
		"output":   fmt.Sprintf("已在 %s 中输入文本 (%d 字符)", selector, len(text)),
		"selector": selector,
		"length":   len(text),
	})
	if err != nil {
		return ToolError(fmt.Sprintf("序列化结果失败: %v", err)), nil
	}
	return string(result), nil
}
