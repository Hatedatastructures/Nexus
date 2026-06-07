// Package tool 提供桌面控制工具集。
// 本文件包含辅助函数、键映射和可用性检测。
package tool

import (
	"context"
	"fmt"
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

// cuRun 执行命令并返回输出。
// 安全说明: 使用 exec.CommandContext 参数分离方式传递参数，参数不会经过 shell 解释，
// 因此 shell 注入风险已消除。不应改用 sh -c 形式调用。
func cuRun(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(out), err
}
func cuFloat(args map[string]any, k string) float64 {
	v := args[k]
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
	"f1":    {"{F1}", ".f1", "F1"}, "f2": {"{F2}", ".f2", "F2"}, "f3": {"{F3}", ".f3", "F3"},
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

// ───────────────────────────── ScreenshotTool (辅助) ─────────────────────────────

// IsAvailable for ScreenshotTool checks for platform screenshot utilities.
func screenshotAvail() bool {
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

// mouseClickExec performs the mouse click per-platform (extracted from MouseClickTool.Execute).
func mouseClickExec(ctx context.Context, x, y int, button string) error {
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
		_, err = cuRun(ctx, "powershell", "-NoProfile", "-Command", ps)
	case "darwin":
		if cuLook("cliclick") {
			flag := "-c"
			if button == "right" {
				flag = "-rc"
			}
			_, err = cuRun(ctx, "cliclick", flag, fmt.Sprintf("%d,%d", x, y))
		} else {
			verb := "click"
			if button == "right" {
				verb = "right click"
			}
			_, err = cuRun(ctx, "osascript", "-e", fmt.Sprintf(`tell application "System Events" to %s at {%d, %d}`, verb, x, y))
		}
	case "linux":
		btn := "1"
		if button == "right" {
			btn = "3"
		}
		_, _ = cuRun(ctx, "xdotool", "mousemove", strconv.Itoa(x), strconv.Itoa(y))
		_, err = cuRun(ctx, "xdotool", "click", btn)
	default:
		return fmt.Errorf("不支持的操作系统: %s", cuOS())
	}
	return err
}

// mouseMoveExec performs mouse move per-platform.
func mouseMoveExec(ctx context.Context, x, y int) (string, error) {
	var err error
	switch cuOS() {
	case "windows":
		_, err = cuRun(ctx, "powershell", "-NoProfile", "-Command",
			fmt.Sprintf(`Add-Type -AssemblyName System.Windows.Forms;[System.Windows.Forms.Cursor]::Position=New-Object System.Drawing.Point(%d,%d)`, x, y))
	case "darwin":
		if cuLook("cliclick") {
			_, err = cuRun(ctx, "cliclick", "m", fmt.Sprintf("%d,%d", x, y))
		} else {
			_, err = cuRun(ctx, "osascript", "-e", fmt.Sprintf(`tell application "System Events" to set cursor to {%d, %d}`, x, y))
		}
	case "linux":
		_, err = cuRun(ctx, "xdotool", "mousemove", strconv.Itoa(x), strconv.Itoa(y))
	default:
		return ToolError(fmt.Sprintf("不支持的操作系统: %s", cuOS())), nil
	}
	if err != nil {
		return ToolError(fmt.Sprintf("鼠标移动失败: %v", err)), nil
	}
	return ToolResult(map[string]any{"success": true, "x": x, "y": y}), nil
}

// typeTextExec performs text input per-platform.
func typeTextExec(ctx context.Context, text string) error {
	var err error
	switch cuOS() {
	case "windows":
		_, err = cuRun(ctx, "powershell", "-NoProfile", "-Command",
			fmt.Sprintf(`Add-Type -AssemblyName System.Windows.Forms;[System.Windows.Forms.SendKeys]::SendWait('%s')`, cuEsc(text)))
	case "darwin":
		esc := strings.ReplaceAll(strings.ReplaceAll(text, `\`, `\\`), `"`, `\"`)
		_, err = cuRun(ctx, "osascript", "-e", fmt.Sprintf(`tell application "System Events" to keystroke "%s"`, esc))
	case "linux":
		_, err = cuRun(ctx, "xdotool", "type", "--clearmodifiers", cuEsc(text))
	default:
		return fmt.Errorf("不支持的操作系统: %s", cuOS())
	}
	return err
}

// keyPressExec performs key press per-platform.
func keyPressExec(ctx context.Context, parts []string, rawKey string) error {
	var err error
	switch cuOS() {
	case "windows":
		_, err = cuRun(ctx, "powershell", "-NoProfile", "-Command",
			fmt.Sprintf(`Add-Type -AssemblyName System.Windows.Forms;[System.Windows.Forms.SendKeys]::SendWait('%s')`, cuWinKey(parts)))
	case "darwin":
		_, err = cuRun(ctx, "osascript", "-e", fmt.Sprintf(
			`tell application "System Events" to keystroke "%s"%s`,
			cuMacKey(parts[len(parts)-1]), cuMacMods(parts[:len(parts)-1])))
	case "linux":
		_, err = cuRun(ctx, "xdotool", "key", cuXdoSeq(parts))
	default:
		return fmt.Errorf("不支持的操作系统: %s", cuOS())
	}
	return err
}
