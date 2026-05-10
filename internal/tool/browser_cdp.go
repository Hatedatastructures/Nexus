// CDP 直接控制工具 — 通过 WebSocket 向浏览器发送原始 Chrome DevTools Protocol 命令。
//
// 这是浏览器操作的"逃生舱"，可以执行 browser_navigate、browser_click 等
// 高层工具无法覆盖的底层操作：原生对话框处理、iframe 内求值、Cookie/网络控制、
// 低层级标签管理等。
//
// CDP 方法参考: https://chromedevtools.github.io/devtools-protocol/
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/go-rod/rod/lib/launcher"
	"github.com/gorilla/websocket"
)

// ───────────────────────────── CDP 调用核心 ─────────────────────────────

// cdpNextID 全局自增 CDP 请求 ID 计数器。
var cdpNextID atomic.Int64

// cdpCall 通过 WebSocket 向 CDP 端点发送单个命令并等待响应。
//
// ws: 已连接的 WebSocket 客户端
// method: CDP 方法名，如 "Target.getTargets"、"Runtime.evaluate"
// params: 方法参数（可为 nil）
// sessionID: 可选会话 ID，用于页面级方法（绑定到特定标签页）
// timeout: 超时时间
//
// 返回解析后的 result 对象。如果响应中包含 "error" 字段则返回错误。
func cdpCall(ws *websocket.Conn, method string, params map[string]any, sessionID string, timeout time.Duration) (map[string]any, error) {
	callID := cdpNextID.Add(1)

	// 构建 CDP 请求
	req := map[string]any{
		"id":     callID,
		"method": method,
	}
	if len(params) > 0 {
		req["params"] = params
	}
	if sessionID != "" {
		req["sessionId"] = sessionID
	}

	if err := ws.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return nil, fmt.Errorf("设置写超时失败: %w", err)
	}
	if err := ws.WriteJSON(req); err != nil {
		return nil, fmt.Errorf("发送 CDP 命令失败: %w", err)
	}

	// 读取响应，忽略中间到达的事件消息
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, fmt.Errorf("等待 CDP 响应超时 (%s): %s", timeout, method)
		}

		if err := ws.SetReadDeadline(time.Now().Add(remaining)); err != nil {
			return nil, fmt.Errorf("设置读超时失败: %w", err)
		}

		var raw json.RawMessage
		if err := ws.ReadJSON(&raw); err != nil {
			return nil, fmt.Errorf("读取 CDP 响应失败: %w", err)
		}

		var msg map[string]any
		if err := json.Unmarshal(raw, &msg); err != nil {
			slog.Debug("CDP 响应非 JSON，跳过", "raw", string(raw))
			continue
		}

		// 只关注与本次请求 ID 匹配的响应
		if id, ok := msg["id"].(float64); ok && int64(id) == callID {
			if errObj, hasErr := msg["error"]; hasErr {
				return nil, fmt.Errorf("CDP 错误: %v", errObj)
			}
			if result, ok := msg["result"].(map[string]any); ok {
				return result, nil
			}
			return map[string]any{}, nil
		}
		// 不匹配的消息（事件或其他响应），继续等待
	}
}

// cdpCallWithTarget 通过 WebSocket 发送 CDP 命令，可选先附加到目标。
//
// 当 targetID 非空时，先调用 Target.attachToTarget 获取 sessionId，
// 再用该 sessionId 发送真正的命令。适用于页面级方法（Page.*、Runtime.* 等）。
// 当 targetID 为空时，直接在浏览器级别发送命令，适用于全局方法（Target.*、Browser.* 等）。
func cdpCallWithTarget(ws *websocket.Conn, method string, params map[string]any, targetID string, timeout time.Duration) (map[string]any, error) {
	sessionID := ""

	// 如果需要附加到特定目标，先建立会话
	if targetID != "" {
		attachResult, err := cdpCall(ws, "Target.attachToTarget", map[string]any{
			"targetId": targetID,
			"flatten":  true,
		}, "", timeout)
		if err != nil {
			return nil, fmt.Errorf("附加到目标 %s 失败: %w", targetID, err)
		}
		if sid, ok := attachResult["sessionId"].(string); ok && sid != "" {
			sessionID = sid
		} else {
			return nil, fmt.Errorf("Target.attachToTarget 未返回 sessionId")
		}
	}

	return cdpCall(ws, method, params, sessionID, timeout)
}

// ───────────────────────────── BrowserCDPTool ─────────────────────────────

// BrowserCDPTool 实现通过 Chrome DevTools Protocol 直接控制浏览器。
// 工具名: browser_cdp
type BrowserCDPTool struct{}

// Name 返回工具名称。
func (t *BrowserCDPTool) Name() string { return "browser_cdp" }

// Description 返回工具描述。
func (t *BrowserCDPTool) Description() string {
	return "发送原始 Chrome DevTools Protocol (CDP) 命令直接控制浏览器。" +
		"适用于高层浏览器工具（browser_navigate、browser_click 等）无法覆盖的底层操作，" +
		"如原生对话框处理、iframe 内求值、Cookie/网络控制、低层级标签管理等。\n\n" +
		"CDP 方法参考: https://chromedevtools.github.io/devtools-protocol/\n\n" +
		"常用示例:\n" +
		"- 列出标签页: method='Target.getTargets', params={}\n" +
		"- 获取所有 Cookie: method='Network.getAllCookies', params={}\n" +
		"- 在特定标签页求值: method='Runtime.evaluate', params={'expression': 'document.title'}, target_id=<tabId>\n" +
		"- 处理原生 JS 对话框: method='Page.handleJavaScriptDialog', params={'accept': true}, target_id=<tabId>"
}

// Toolset 返回工具所属工具集。
func (t *BrowserCDPTool) Toolset() string { return "browser" }

// Emoji 返回工具图标。
func (t *BrowserCDPTool) Emoji() string { return "🧪" }

// IsAvailable 检查浏览器是否可用。
func (t *BrowserCDPTool) IsAvailable() bool {
	return browserCheck()
}

// MaxResultChars 返回结果最大字符数。
func (t *BrowserCDPTool) MaxResultChars() int { return 100000 }

// Schema 返回工具的 JSON Schema。
func (t *BrowserCDPTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "browser_cdp",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"method": map[string]any{
					"type":        "string",
					"description": "CDP 方法名，如 'Target.getTargets'、'Runtime.evaluate'、'Page.handleJavaScriptDialog'",
				},
				"params": map[string]any{
					"type":        "object",
					"description": "方法特定的参数（JSON 对象）。不需要参数的方法可省略或传 {}",
				},
				"target_id": map[string]any{
					"type":        "string",
					"description": "可选。来自 Target.getTargets 结果中的 targetId，用于页面级方法",
				},
				"timeout": map[string]any{
					"type":        "number",
					"description": "超时时间（秒），默认 30，最大 300",
					"default":     30,
				},
			},
			"required": []string{"method"},
		},
	}
}

// Execute 执行 CDP 命令。
func (t *BrowserCDPTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	// 解析参数
	method, ok := args["method"].(string)
	if !ok || method == "" {
		return ToolError("参数 'method' 是必填项 (如 'Target.getTargets')"), nil
	}

	params, _ := args["params"].(map[string]any)
	targetID, _ := args["target_id"].(string)

	timeoutSec := 30.0
	if sec, ok := args["timeout"].(float64); ok && sec > 0 {
		timeoutSec = sec
	}
	if timeoutSec < 1 {
		timeoutSec = 1
	}
	if timeoutSec > 300 {
		timeoutSec = 300
	}
	timeout := time.Duration(timeoutSec * float64(time.Second))

	// 确保浏览器已启动
	if err := ensureBrowser(); err != nil {
		return ToolError(fmt.Sprintf("浏览器不可用: %v", err)), nil
	}

	// 获取控制 URL（CDP WebSocket 地址）
	ctrlURL := browserControlURL
	if ctrlURL == "" {
		return ToolError("浏览器控制 URL 不可用"), nil
	}

	slog.Info("CDP 命令", "method", method, "target_id", targetID)

	// 建立 WebSocket 连接
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}
	ws, _, err := dialer.DialContext(ctx, ctrlURL, nil)
	if err != nil {
		slog.Error("CDP WebSocket 连接失败", "url", ctrlURL, "err", err)
		return ToolError(fmt.Sprintf("CDP 连接失败: %v", err)), nil
	}
	defer ws.Close()

	// 发送 CDP 命令
	result, err := cdpCallWithTarget(ws, method, params, targetID, timeout)
	if err != nil {
		slog.Error("CDP 命令执行失败", "method", method, "err", err)
		return ToolError(fmt.Sprintf("CDP 命令失败: %v", err)), nil
	}

	payload := map[string]any{
		"success": true,
		"method":  method,
		"result":  result,
	}
	if targetID != "" {
		payload["target_id"] = targetID
	}

	data, _ := json.Marshal(payload)
	return string(data), nil
}

// browserCheck 检查浏览器是否可用。
// 优先检查 Browserbase 云浏览器配置，其次检查本地 Chrome/Chromium。
func browserCheck() bool {
	// 如果配置了 Browserbase API Key，视为可用
	if isBrowserbaseConfigured() {
		return true
	}
	_, ok := launcher.LookPath()
	return ok
}

func init() {
	reg := GetRegistry()
	reg.Register(&BrowserCDPTool{})
}
