// Package tool 提供浏览器对话框处理工具。
// 处理页面中的 alert/confirm/prompt 原生对话框。
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/gorilla/websocket"
)

// ───────────────────────────── 对话框状态 ─────────────────────────────

// dialogInfo 存储当前待处理的对话框信息。
type dialogInfo struct {
	Type         string // 对话框类型: alert, confirm, prompt
	Message      string // 对话框文本
	DefaultPrompt string // prompt 默认值
	URL          string // 触发对话框的页面 URL
}

// dialogState 管理对话框状态，并发安全。
type dialogState struct {
	mu           sync.RWMutex
	pending      *dialogInfo
	listenerSet  bool
	listenerCancel func()
}

var globalDialogState = &dialogState{}

func (d *dialogState) setPending(info *dialogInfo) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pending = info
}

func (d *dialogState) getPending() *dialogInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.pending
}

func (d *dialogState) clearPending() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pending = nil
}

// ───────────────────────────── 对话框处理工具 ─────────────────────────────

// BrowserHandleDialogTool 处理浏览器原生对话框。
type BrowserHandleDialogTool struct{}

func (t *BrowserHandleDialogTool) Name() string { return "browser_handle_dialog" }

func (t *BrowserHandleDialogTool) Description() string {
	return "处理页面中的 alert/confirm/prompt 对话框。支持接受、拒绝、或向 prompt 输入文本。"
}

func (t *BrowserHandleDialogTool) Toolset() string { return "browser" }
func (t *BrowserHandleDialogTool) Emoji() string { return "⚠️" }
func (t *BrowserHandleDialogTool) IsAvailable() bool { return true }
func (t *BrowserHandleDialogTool) MaxResultChars() int { return 5000 }

func (t *BrowserHandleDialogTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "browser_handle_dialog",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "操作类型: accept (接受), dismiss (拒绝), input (输入文本)",
					"enum":        []string{"accept", "dismiss", "input"},
				},
				"text": map[string]any{
					"type":        "string",
					"description": "当 action 为 input 时，要输入的文本内容",
				},
			},
			"required": []string{"action"},
		},
	}
}

func (t *BrowserHandleDialogTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	action, ok := args["action"].(string)
	if !ok {
		return ToolError("参数 action 是必填项 (accept/dismiss/input)"), nil
	}
	if action != "accept" && action != "dismiss" && action != "input" {
		return ToolError("参数 action 必须是 accept、dismiss 或 input 之一"), nil
	}

	inputText, _ := args["text"].(string)
	if action == "input" && inputText == "" {
		return ToolError("当 action 为 input 时，必须提供 text 参数"), nil
	}

	// 获取待处理的对话框信息
	info := globalDialogState.getPending()
	if info == nil {
		// 尝试等待对话框出现
		slog.Info("no pending dialog, waiting 3 seconds")
		select {
		case <-time.After(3 * time.Second):
		case <-ctx.Done():
			return ToolError("等待对话框超时"), nil
		}
		info = globalDialogState.getPending()
		if info == nil {
			return ToolError("等待 3 秒后仍未检测到对话框"), nil
		}
	}

	slog.Info("handling browser dialog",
		"type", info.Type,
		"message", info.Message,
		"action", action,
	)

	// 通过 CDP 处理对话框
	ctrlURL := getBrowserControlURL()
	if ctrlURL == "" {
		return ToolError("浏览器控制 URL 不可用，请确保浏览器已启动"), nil
	}

	accept := action == "accept" || action == "input"
	promptText := ""
	if action == "input" {
		promptText = inputText
	} else if action == "accept" && info.Type == "prompt" {
		promptText = info.DefaultPrompt
	}

	// 清除待处理状态
	globalDialogState.clearPending()

	// 发送 CDP 命令
	if err := handleDialogViaCDP(ctx, ctrlURL, accept, promptText); err != nil {
		return ToolError(fmt.Sprintf("处理对话框失败: %v", err)), nil
	}

	result, err := json.Marshal(map[string]any{
		"output":      fmt.Sprintf("已处理对话框: %s", action),
		"type":        info.Type,
		"message":     info.Message,
		"action":      action,
		"prompt_text": promptText,
	})
	if err != nil {
		return ToolError(fmt.Sprintf("序列化结果失败: %v", err)), nil
	}
	return string(result), nil
}

// ───────────────────────────── CDP 对话框处理 ─────────────────────────────

func getBrowserControlURL() string {
	browserMu.Lock()
	defer browserMu.Unlock()
	return browserControlURL
}

func handleDialogViaCDP(ctx context.Context, ctrlURL string, accept bool, promptText string) error {
	wsURL := ctrlURL
	ws, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("CDP WebSocket 连接失败: %w", err)
	}
	defer ws.Close()

	id := nextCDPID()
	params := map[string]any{
		"id":     id,
		"method": "Page.handleJavaScriptDialog",
		"params": map[string]any{
			"accept":     accept,
			"promptText": promptText,
		},
	}

	data, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("序列化 CDP 参数失败: %w", err)
	}
	if err := ws.WriteMessage(websocket.TextMessage, data); err != nil {
		return fmt.Errorf("发送 CDP 命令失败: %w", err)
	}

	_, resp, err := ws.ReadMessage()
	if err != nil {
		return fmt.Errorf("读取 CDP 响应失败: %w", err)
	}

	var result struct {
		ID    int    `json:"id"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return fmt.Errorf("解析 CDP 响应失败: %w", err)
	}

	if result.Error != nil {
		return fmt.Errorf("CDP 错误: %s", result.Error.Message)
	}

	return nil
}

var cdpIDCounter int64

func nextCDPID() int64 {
	return atomic.AddInt64(&cdpIDCounter, 1)
}

// ───────────────────────────── 对话框监听器设置 ─────────────────────────────

// SetupDialogListener 为页面设置对话框事件监听器。
func SetupDialogListener(page *rod.Page) {
	globalDialogState.mu.Lock()
	if globalDialogState.listenerSet {
		globalDialogState.mu.Unlock()
		return
	}

	cancel := page.EachEvent(func(e *proto.PageJavascriptDialogOpening) {
		slog.Info("browser dialog detected",
			"type", string(e.Type),
			"message", e.Message,
		)
		globalDialogState.setPending(&dialogInfo{
			Type:         string(e.Type),
			Message:      e.Message,
			DefaultPrompt: e.DefaultPrompt,
			URL:          e.URL,
		})
	})

	globalDialogState.listenerSet = true
	globalDialogState.listenerCancel = cancel
	globalDialogState.mu.Unlock()

	slog.Info("dialog listener started")
}

// ───────────────────────────── init 注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&BrowserHandleDialogTool{})
}
