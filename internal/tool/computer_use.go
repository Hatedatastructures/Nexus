// Package tool 提供桌面控制工具集。
// 通过平台特定命令实现截屏、鼠标、键盘等桌面自动化操作。
package tool

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// ───────────────────────────── 辅助函数 ─────────────────────────────

func cuOS() string { return runtime.GOOS }
func cuLook(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
func cuRun(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}
func cuFloat(args map[string]any, k string) float64 {
	v, _ := args[k]
	f, _ := v.(float64)
	return f
}

// cuEsc 转义 SendKeys 特殊字符
func cuEsc(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`+^%~()`, r) {
			fmt.Fprintf(&b, "{%c}", r)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// cuKM 特殊键映射 [win, mac, linux]
var cuKM = map[string][3]string{
	"enter": {"~", " ", "Return"}, "return": {"~", " ", "Return"},
	"tab": {"{TAB}", "\t", "Tab"}, "escape": {"{ESC}", ".escape", "Escape"}, "esc": {"{ESC}", ".escape", "Escape"},
	"backspace": {"{BS}", ".backspace", "BackSpace"}, "delete": {"{DEL}", ".delete", "Delete"},
	"up": {"{UP}", ".up", "Up"}, "down": {"{DOWN}", ".down", "Down"},
	"left": {"{LEFT}", ".left", "Left"}, "right": {"{RIGHT}", ".right", "Right"},
	"home": {"{HOME}", ".home", "Home"}, "end": {"{END}", ".end", "End"},
	"pageup": {"{PGUP}", ".pageup", "Prior"}, "pagedown": {"{PGDN}", ".pagedown", "Next"},
	"space": {" ", " ", "space"},
	"f1": {"{F1}", ".f1", "F1"}, "f2": {"{F2}", ".f2", "F2"}, "f3": {"{F3}", ".f3", "F3"},
	"f4": {"{F4}", ".f4", "F4"}, "f5": {"{F5}", ".f5", "F5"}, "f6": {"{F6}", ".f6", "F6"},
	"f7": {"{F7}", ".f7", "F7"}, "f8": {"{F8}", ".f8", "F8"}, "f9": {"{F9}", ".f9", "F9"},
	"f10": {"{F10}", ".f10", "F10"}, "f11": {"{F11}", ".f11", "F11"}, "f12": {"{F12}", ".f12", "F12"},
}

// 按键转换: Windows
func cuWinKey(parts []string) string {
	if len(parts) == 1 {
		if m, ok := cuKM[parts[0]]; ok {
			return m[0]
		}
		return parts[0]
	}
	mod := ""
	for _, p := range parts[:len(parts)-1] {
		switch p {
		case "ctrl", "control":
			mod += "^"
		case "alt":
			mod += "%"
		case "shift":
			mod += "+"
		}
	}
	k := parts[len(parts)-1]
	if m, ok := cuKM[k]; ok {
		return mod + "(" + m[0] + ")"
	}
	return mod + "(" + k + ")"
}

// 按键转换: macOS
func cuMacKey(key string) string {
	if m, ok := cuKM[key]; ok {
		return m[1]
	}
	return key
}
func cuMacMods(mods []string) string {
	var ps []string
	for _, m := range mods {
		switch m {
		case "ctrl", "control":
			ps = append(ps, "control down")
		case "alt", "option":
			ps = append(ps, "option down")
		case "shift":
			ps = append(ps, "shift down")
		case "cmd", "command":
			ps = append(ps, "command down")
		}
	}
	if len(ps) == 0 {
		return ""
	}
	return " using {" + strings.Join(ps, ", ") + "}"
}

// 按键转换: Linux xdotool
func cuXdoSeq(parts []string) string {
	var ms []string
	for _, p := range parts {
		if m, ok := cuKM[p]; ok {
			ms = append(ms, m[2])
		} else {
			ms = append(ms, p)
		}
	}
	return strings.Join(ms, "+")
}

// cuAvailMouse 鼠标工具可用性
func cuAvailMouse() bool {
	switch cuOS() {
	case "windows":
		return true
	case "darwin":
		return cuLook("cliclick") || cuLook("osascript")
	case "linux":
		return cuLook("xdotool")
	}
	return false
}

// cuAvailKbd 键盘工具可用性
func cuAvailKbd() bool {
	switch cuOS() {
	case "windows":
		return true
	case "darwin":
		return cuLook("osascript")
	case "linux":
		return cuLook("xdotool")
	}
	return false
}

// ───────────────────────────── 截屏工具 ─────────────────────────────

// ScreenshotTool 截取屏幕截图。
type ScreenshotTool struct{}

func (t *ScreenshotTool) Name() string       { return "computer_screenshot" }
func (t *ScreenshotTool) Description() string { return "截取屏幕截图并保存到指定路径" }
func (t *ScreenshotTool) Toolset() string     { return "computer_use" }
func (t *ScreenshotTool) Emoji() string       { return "screenshot" }
func (t *ScreenshotTool) MaxResultChars() int { return 10000 }
func (t *ScreenshotTool) IsAvailable() bool {
	switch cuOS() {
	case "windows":
		return true
	case "darwin":
		return cuLook("screencapture")
	case "linux":
		return cuLook("gnome-screenshot") || cuLook("scrot") || cuLook("import")
	}
	return false
}
func (t *ScreenshotTool) Schema() *ToolSchema {
	return &ToolSchema{Name: t.Name(), Description: t.Description(), Parameters: map[string]any{
		"type": "object", "properties": map[string]any{
			"path": map[string]any{"type": "string", "description": "截图保存路径，默认临时目录"},
		},
	}}
}
func (t *ScreenshotTool) Execute(_ context.Context, args map[string]any) (string, error) {
	path := getStringFromArgs(args, "path")
	if path == "" {
		path = fmt.Sprintf("%s/screenshot_%d.png", os.TempDir(), os.Getpid())
	}
	var out string
	var err error
	switch cuOS() {
	case "windows":
		safePath := strings.ReplaceAll(path, "'", "''")
		ps := fmt.Sprintf(
			`Add-Type -AssemblyName System.Windows.Forms;`+
				`$s=[System.Windows.Forms.Screen]::PrimaryScreen.Bounds;`+
				`$b=New-Object System.Drawing.Bitmap($s.Width,$s.Height);`+
				`$g=[System.Drawing.Graphics]::FromImage($b);`+
				`$g.CopyFromScreen($s.Location,[System.Drawing.Point]::Empty,$s.Size);`+
				`$b.Save('%s');$g.Dispose();$b.Dispose()`, safePath)
		_, err = cuRun("powershell", "-NoProfile", "-Command", ps)
	case "darwin":
		_, err = cuRun("screencapture", "-x", path)
	case "linux":
		switch {
		case cuLook("gnome-screenshot"):
			_, err = cuRun("gnome-screenshot", "-f", path)
		case cuLook("scrot"):
			_, err = cuRun("scrot", path)
		case cuLook("import"):
			_, err = cuRun("import", "-window", "root", path)
		default:
			return ToolError("未找到截图工具 (需要 gnome-screenshot/scrot/imagemagick)"), nil
		}
	default:
		return ToolError(fmt.Sprintf("不支持的操作系统: %s", cuOS())), nil
	}
	if err != nil {
		return ToolError(fmt.Sprintf("截图失败: %v, 输出: %s", err, strings.TrimSpace(out))), nil
	}
	return ToolResult(map[string]any{"success": true, "path": path}), nil
}

// ───────────────────────────── 鼠标点击工具 ─────────────────────────────

// MouseClickTool 在指定坐标执行鼠标点击。
type MouseClickTool struct{}

func (t *MouseClickTool) Name() string       { return "computer_mouse_click" }
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
func (t *MouseClickTool) Execute(_ context.Context, args map[string]any) (string, error) {
	x, y := int(cuFloat(args, "x")), int(cuFloat(args, "y"))
	button := getStringFromArgs(args, "button")
	if button == "" {
		button = "left"
	}
	if x < 0 || y < 0 {
		return ToolError("坐标不能为负数"), nil
	}
	var err error
	switch cuOS() {
	case "windows":
		down, up := "0x0002", "0x0004"
		if button == "right" {
			down, up = "0x0008", "0x0010"
		}
		ps := fmt.Sprintf(
			`Add-Type -AssemblyName System.Windows.Forms;`+
				`[System.Windows.Forms.Cursor]::Position=New-Object System.Drawing.Point(%d,%d);`+
				`Add-Type -TypeDefinition 'using System;using System.Runtime.InteropServices;`+
				`public class M{[DllImport("user32.dll")]public static extern void mouse_event(int f,int a,int b,int c,int d);}';`+
				`[M]::mouse_event(%s,0,0,0,0);[M]::mouse_event(%s,0,0,0,0)`, x, y, down, up)
		_, err = cuRun("powershell", "-NoProfile", "-Command", ps)
	case "darwin":
		if cuLook("cliclick") {
			flag := "-c"
			if button == "right" {
				flag = "-rc"
			}
			_, err = cuRun("cliclick", flag, fmt.Sprintf("%d,%d", x, y))
		} else {
			verb := "click"
			if button == "right" {
				verb = "right click"
			}
			_, err = cuRun("osascript", "-e", fmt.Sprintf(`tell application "System Events" to %s at {%d, %d}`, verb, x, y))
		}
	case "linux":
		btn := "1"
		if button == "right" {
			btn = "3"
		}
		_, _ = cuRun("xdotool", "mousemove", strconv.Itoa(x), strconv.Itoa(y))
		_, err = cuRun("xdotool", "click", btn)
	default:
		return ToolError(fmt.Sprintf("不支持的操作系统: %s", cuOS())), nil
	}
	if err != nil {
		return ToolError(fmt.Sprintf("鼠标点击失败: %v", err)), nil
	}
	return ToolResult(map[string]any{"success": true, "x": x, "y": y, "button": button}), nil
}

// ───────────────────────────── 鼠标移动工具 ─────────────────────────────

// MouseMoveTool 移动鼠标到指定坐标。
type MouseMoveTool struct{}

func (t *MouseMoveTool) Name() string       { return "computer_mouse_move" }
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
func (t *MouseMoveTool) Execute(_ context.Context, args map[string]any) (string, error) {
	x, y := int(cuFloat(args, "x")), int(cuFloat(args, "y"))
	if x < 0 || y < 0 {
		return ToolError("坐标不能为负数"), nil
	}
	var err error
	switch cuOS() {
	case "windows":
		_, err = cuRun("powershell", "-NoProfile", "-Command",
			fmt.Sprintf(`Add-Type -AssemblyName System.Windows.Forms;[System.Windows.Forms.Cursor]::Position=New-Object System.Drawing.Point(%d,%d)`, x, y))
	case "darwin":
		if cuLook("cliclick") {
			_, err = cuRun("cliclick", "m", fmt.Sprintf("%d,%d", x, y))
		} else {
			_, err = cuRun("osascript", "-e", fmt.Sprintf(`tell application "System Events" to set cursor to {%d, %d}`, x, y))
		}
	case "linux":
		_, err = cuRun("xdotool", "mousemove", strconv.Itoa(x), strconv.Itoa(y))
	default:
		return ToolError(fmt.Sprintf("不支持的操作系统: %s", cuOS())), nil
	}
	if err != nil {
		return ToolError(fmt.Sprintf("鼠标移动失败: %v", err)), nil
	}
	return ToolResult(map[string]any{"success": true, "x": x, "y": y}), nil
}

// ───────────────────────────── 文本输入工具 ─────────────────────────────

// TypeTextTool 通过键盘输入文本。
type TypeTextTool struct{}

func (t *TypeTextTool) Name() string       { return "computer_type_text" }
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
func (t *TypeTextTool) Execute(_ context.Context, args map[string]any) (string, error) {
	text := getStringFromArgs(args, "text")
	if text == "" {
		return ToolError("参数 text 是必填项且不能为空"), nil
	}
	var err error
	switch cuOS() {
	case "windows":
		_, err = cuRun("powershell", "-NoProfile", "-Command",
			fmt.Sprintf(`Add-Type -AssemblyName System.Windows.Forms;[System.Windows.Forms.SendKeys]::SendWait('%s')`, cuEsc(text)))
	case "darwin":
		esc := strings.ReplaceAll(strings.ReplaceAll(text, `\`, `\\`), `"`, `\"`)
		_, err = cuRun("osascript", "-e", fmt.Sprintf(`tell application "System Events" to keystroke "%s"`, esc))
	case "linux":
		_, err = cuRun("xdotool", "type", "--clearmodifiers", cuEsc(text))
	default:
		return ToolError(fmt.Sprintf("不支持的操作系统: %s", cuOS())), nil
	}
	if err != nil {
		return ToolError(fmt.Sprintf("文本输入失败: %v", err)), nil
	}
	return ToolResult(map[string]any{"success": true, "text": text}), nil
}

// ───────────────────────────── 按键工具 ─────────────────────────────

// KeyPressTool 发送按键组合。
type KeyPressTool struct{}

func (t *KeyPressTool) Name() string       { return "computer_key_press" }
func (t *KeyPressTool) Description() string { return "发送按键组合，如 ctrl+c、alt+f4、Return 等" }
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
func (t *KeyPressTool) Execute(_ context.Context, args map[string]any) (string, error) {
	key := getStringFromArgs(args, "key")
	if key == "" {
		return ToolError("参数 key 是必填项"), nil
	}
	parts := strings.Split(strings.ToLower(key), "+")
	var err error
	switch cuOS() {
	case "windows":
		_, err = cuRun("powershell", "-NoProfile", "-Command",
			fmt.Sprintf(`Add-Type -AssemblyName System.Windows.Forms;[System.Windows.Forms.SendKeys]::SendWait('%s')`, cuWinKey(parts)))
	case "darwin":
		_, err = cuRun("osascript", "-e", fmt.Sprintf(
			`tell application "System Events" to keystroke "%s"%s`,
			cuMacKey(parts[len(parts)-1]), cuMacMods(parts[:len(parts)-1])))
	case "linux":
		_, err = cuRun("xdotool", "key", cuXdoSeq(parts))
	default:
		return ToolError(fmt.Sprintf("不支持的操作系统: %s", cuOS())), nil
	}
	if err != nil {
		return ToolError(fmt.Sprintf("按键失败: %v", err)), nil
	}
	return ToolResult(map[string]any{"success": true, "key": key}), nil
}

// ───────────────────────────── init 注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&ScreenshotTool{})
	GetRegistry().Register(&MouseClickTool{})
	GetRegistry().Register(&MouseMoveTool{})
	GetRegistry().Register(&TypeTextTool{})
	GetRegistry().Register(&KeyPressTool{})
}
