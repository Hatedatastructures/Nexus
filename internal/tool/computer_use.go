// Package tool 提供桌面控制工具集。
// 本文件包含工具类型定义、Schema 和 Execute 方法。
package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ───────────────────────────── 截屏工具 ─────────────────────────────

// ScreenshotTool 截取屏幕截图。
type ScreenshotTool struct{}

func (t *ScreenshotTool) Name() string        { return "computer_screenshot" }
func (t *ScreenshotTool) Description() string { return "截取屏幕截图并保存到指定路径" }
func (t *ScreenshotTool) Toolset() string     { return "computer_use" }
func (t *ScreenshotTool) Emoji() string       { return "screenshot" }
func (t *ScreenshotTool) MaxResultChars() int { return 10000 }
func (t *ScreenshotTool) IsAvailable() bool   { return screenshotAvail() }
func (t *ScreenshotTool) Schema() *ToolSchema {
	return &ToolSchema{Name: t.Name(), Description: t.Description(), Parameters: map[string]any{
		"type": "object", "properties": map[string]any{
			"path": map[string]any{"type": "string", "description": "截图保存路径，默认临时目录"},
		},
	}}
}
func (t *ScreenshotTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	path := getStringFromArgs(args, "path")
	if path == "" {
		path = fmt.Sprintf("%s/screenshot_%d.png", os.TempDir(), os.Getpid())
	}
	var err error
	switch cuOS() {
	case "windows":
		cleanPath := filepath.Clean(path)
		ps := `$s=[System.Windows.Forms.Screen]::PrimaryScreen.Bounds;$b=New-Object System.Drawing.Bitmap($s.Width,$s.Height);$g=[System.Drawing.Graphics]::FromImage($b);$g.CopyFromScreen($s.Location,[System.Drawing.Point]::Empty,$s.Size);$b.Save($args[0]);$g.Dispose();$b.Dispose()`
		_, err = cuRun(ctx, "powershell", "-NoProfile", "-Command", ps, "-args", cleanPath)
	case "darwin":
		_, err = cuRun(ctx, "screencapture", "-x", path)
	case "linux":
		switch {
		case cuLook("gnome-screenshot"):
			_, err = cuRun(ctx, "gnome-screenshot", "-f", path)
		case cuLook("scrot"):
			_, err = cuRun(ctx, "scrot", path)
		case cuLook("import"):
			_, err = cuRun(ctx, "import", "-window", "root", path)
		default:
			return ToolError("未找到截图工具 (需要 gnome-screenshot/scrot/imagemagick)"), nil
		}
	default:
		return ToolError(fmt.Sprintf("不支持的操作系统: %s", cuOS())), nil
	}
	if err != nil {
		return ToolError(fmt.Sprintf("截图失败: %v", err)), nil
	}
	return ToolResult(map[string]any{"success": true, "path": path}), nil
}

// ───────────────────────────── 鼠标点击工具 ─────────────────────────────

// MouseClickTool 在指定坐标执行鼠标点击。
type MouseClickTool struct{}

func (t *MouseClickTool) Name() string        { return "computer_mouse_click" }
func (t *MouseClickTool) Description() string { return "在指定坐标执行鼠标点击操作" }
func (t *MouseClickTool) Toolset() string     { return "computer_use" }
func (t *MouseClickTool) Emoji() string       { return "mouse" }
func (t *MouseClickTool) MaxResultChars() int { return 5000 }
func (t *MouseClickTool) IsAvailable() bool   { return cuAvailMouse() }
func (t *MouseClickTool) Schema() *ToolSchema {
	return &ToolSchema{Name: t.Name(), Description: t.Description(), Parameters: map[string]any{
		"type": "object", "properties": map[string]any{
			"x":      map[string]any{"type": "integer", "description": "X 坐标"},
			"y":      map[string]any{"type": "integer", "description": "Y 坐标"},
			"button": map[string]any{"type": "string", "description": "鼠标按钮: left 或 right，默认 left", "enum": []string{"left", "right"}},
		}, "required": []string{"x", "y"},
	}}
}
func (t *MouseClickTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	x, y := int(cuFloat(args, "x")), int(cuFloat(args, "y"))
	button := getStringFromArgs(args, "button")
	if button == "" {
		button = "left"
	}
	if x < 0 || y < 0 {
		return ToolError("坐标不能为负数"), nil
	}
	err := mouseClickExec(ctx, x, y, button)
	if err != nil {
		return ToolError(fmt.Sprintf("鼠标点击失败: %v", err)), nil
	}
	return ToolResult(map[string]any{"success": true, "x": x, "y": y, "button": button}), nil
}

// ───────────────────────────── 鼠标移动工具 ─────────────────────────────

// MouseMoveTool 移动鼠标到指定坐标。
type MouseMoveTool struct{}

func (t *MouseMoveTool) Name() string        { return "computer_mouse_move" }
func (t *MouseMoveTool) Description() string { return "移动鼠标到指定坐标" }
func (t *MouseMoveTool) Toolset() string     { return "computer_use" }
func (t *MouseMoveTool) Emoji() string       { return "mouse" }
func (t *MouseMoveTool) MaxResultChars() int { return 5000 }
func (t *MouseMoveTool) IsAvailable() bool   { return cuAvailMouse() }
func (t *MouseMoveTool) Schema() *ToolSchema {
	return &ToolSchema{Name: t.Name(), Description: t.Description(), Parameters: map[string]any{
		"type": "object", "properties": map[string]any{
			"x": map[string]any{"type": "integer", "description": "目标 X 坐标"},
			"y": map[string]any{"type": "integer", "description": "目标 Y 坐标"},
		}, "required": []string{"x", "y"},
	}}
}
func (t *MouseMoveTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	x, y := int(cuFloat(args, "x")), int(cuFloat(args, "y"))
	if x < 0 || y < 0 {
		return ToolError("坐标不能为负数"), nil
	}
	return mouseMoveExec(ctx, x, y)
}

// ───────────────────────────── 文本输入工具 ─────────────────────────────

// TypeTextTool 通过键盘输入文本。
type TypeTextTool struct{}

func (t *TypeTextTool) Name() string        { return "computer_type_text" }
func (t *TypeTextTool) Description() string { return "通过键盘输入指定文本" }
func (t *TypeTextTool) Toolset() string     { return "computer_use" }
func (t *TypeTextTool) Emoji() string       { return "keyboard" }
func (t *TypeTextTool) MaxResultChars() int { return 5000 }
func (t *TypeTextTool) IsAvailable() bool   { return cuAvailKbd() }
func (t *TypeTextTool) Schema() *ToolSchema {
	return &ToolSchema{Name: t.Name(), Description: t.Description(), Parameters: map[string]any{
		"type": "object", "properties": map[string]any{
			"text": map[string]any{"type": "string", "description": "要输入的文本内容"},
		}, "required": []string{"text"},
	}}
}
func (t *TypeTextTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	text := getStringFromArgs(args, "text")
	if text == "" {
		return ToolError("参数 text 是必填项且不能为空"), nil
	}
	err := typeTextExec(ctx, text)
	if err != nil {
		return ToolError(fmt.Sprintf("文本输入失败: %v", err)), nil
	}
	return ToolResult(map[string]any{"success": true, "text": text}), nil
}

// ───────────────────────────── 按键工具 ─────────────────────────────

// KeyPressTool 发送按键组合。
type KeyPressTool struct{}

func (t *KeyPressTool) Name() string { return "computer_key_press" }
func (t *KeyPressTool) Description() string {
	return "发送按键组合，如 ctrl+c、alt+f4、Return 等"
}
func (t *KeyPressTool) Toolset() string     { return "computer_use" }
func (t *KeyPressTool) Emoji() string       { return "keyboard" }
func (t *KeyPressTool) MaxResultChars() int { return 5000 }
func (t *KeyPressTool) IsAvailable() bool   { return cuAvailKbd() }
func (t *KeyPressTool) Schema() *ToolSchema {
	return &ToolSchema{Name: t.Name(), Description: t.Description(), Parameters: map[string]any{
		"type": "object", "properties": map[string]any{
			"key": map[string]any{"type": "string", "description": "按键组合，例如 ctrl+c、alt+f4、Return"},
		}, "required": []string{"key"},
	}}
}
func (t *KeyPressTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	key := getStringFromArgs(args, "key")
	if key == "" {
		return ToolError("参数 key 是必填项"), nil
	}
	parts := strings.Split(strings.ToLower(key), "+")
	err := keyPressExec(ctx, parts, key)
	if err != nil {
		return ToolError(fmt.Sprintf("按键失败: %v", err)), nil
	}
	return ToolResult(map[string]any{"success": true, "key": key}), nil
}
