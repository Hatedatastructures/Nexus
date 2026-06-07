// Package tool 提供基于 rod 库的浏览器自动化工具。
// 支持页面导航、截图、点击、输入等操作。
// rod 是纯 Go 的浏览器自动化库，直接通过 Chrome DevTools Protocol 控制浏览器。
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// ───────────────────────────── 浏览器管理 ─────────────────────────────

var (
	browserInstance   *rod.Browser
	pageInstance      *rod.Page
	browserMu         sync.Mutex
	browserReady      bool
	browserControlURL string // CDP WebSocket 地址，供 CDP 工具使用
)

// ensureBrowserLocked 确保浏览器实例已启动（调用方持有 browserMu）。
func ensureBrowserLocked(ctx context.Context) error {
	if browserReady && browserInstance != nil {
		return nil
	}

	slog.Info("launching browser instance")

	// 优先使用 Browserbase 云浏览器（如果配置了环境变量）
	if cfg, ok := loadBrowserbaseConfig(); ok {
		slog.Info("Browserbase config detected, using cloud browser")
		sess, err := NewBrowserbaseSession(ctx, cfg)
		if err != nil {
			return fmt.Errorf("创建 Browserbase 会话失败: %w", err)
		}
		cdpURL := sess.CDPWebSocketURL()
		browserControlURL = cdpURL
		browserInstance = rod.New().ControlURL(cdpURL).MustConnect()
		browserReady = true
		slog.Info("Browserbase cloud browser connected", "session_id", sess.SessionID())
		return nil
	}

	// 自动查找 Chrome/Chromium 浏览器路径
	path, _ := launcher.LookPath()
	if path == "" {
		// 尝试常见路径
		for _, p := range []string{
			"google-chrome", "chromium", "chromium-browser",
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		} {
			if _, err := exec.LookPath(p); err == nil {
				path = p
				break
			}
		}
	}

	if path == "" {
		return fmt.Errorf("未找到 Chrome/Chromium 浏览器。请安装 Google Chrome 或 Chromium，或设置 ROD_BROWSER_BIN 环境变量")
	}

	l := launcher.New().
		Headless(true).
		NoSandbox(true).
		Set("disable-gpu").
		Set("disable-dev-shm-usage")

	if bin := os.Getenv("ROD_BROWSER_BIN"); bin != "" {
		l.Bin(bin)
	}

	url, err := l.Launch()
	if err != nil {
		return fmt.Errorf("浏览器启动失败: %w", err)
	}

	browserControlURL = url
	browserInstance = rod.New().ControlURL(url).MustConnect()
	browserReady = true

	slog.Info("browser launched successfully")
	return nil
}

// ensureBrowser 确保浏览器实例已启动并可用。
func ensureBrowser(ctx context.Context) error {
	browserMu.Lock()
	defer browserMu.Unlock()
	return ensureBrowserLocked(ctx)
}

// getPage 返回当前页面实例，如果不存在则创建新页面。
func getPage(ctx context.Context) (*rod.Page, error) {
	browserMu.Lock()
	defer browserMu.Unlock()

	if err := ensureBrowserLocked(ctx); err != nil {
		return nil, err
	}

	if pageInstance != nil {
		// 检查页面是否仍然有效
		_, evalErr := pageInstance.Timeout(2 * time.Second).Eval(`() => document.title`)
		if evalErr == nil {
			return pageInstance, nil
		}
		// 页面无效，重新创建
		pageInstance = nil
	}

	pageInstance = browserInstance.MustPage()
	return pageInstance, nil
}

// ───────────────────────────── 浏览器导航工具 ─────────────────────────────

// BrowserNavigateTool 实现浏览器页面导航。
type BrowserNavigateTool struct{}

// Name 返回工具名称。
func (t *BrowserNavigateTool) Name() string { return "browser_navigate" }

// Description 返回工具描述。
func (t *BrowserNavigateTool) Description() string {
	return "导航到指定 URL 页面。返回页面的可访问性树快照。"
}

// Toolset 返回工具所属工具集。
func (t *BrowserNavigateTool) Toolset() string { return "browser" }

// Emoji 返回工具图标。
func (t *BrowserNavigateTool) Emoji() string { return "🌍" }

// IsAvailable 检查浏览器是否可用（本地 Chrome 或 Browserbase 云浏览器）。
func (t *BrowserNavigateTool) IsAvailable() bool {
	return browserCheck()
}

// MaxResultChars 返回结果最大字符数。
func (t *BrowserNavigateTool) MaxResultChars() int { return 100000 }

// Schema 返回工具的 JSON Schema。
func (t *BrowserNavigateTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "browser_navigate",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "要导航到的 URL",
				},
			},
			"required": []string{"url"},
		},
	}
}

// Execute 执行浏览器导航。
func (t *BrowserNavigateTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	page, err := getPage(ctx)
	if err != nil {
		return ToolError(fmt.Sprintf("浏览器不可用: %v", err)), nil
	}

	url, ok := args["url"].(string)
	if !ok || url == "" {
		return ToolError("参数 url 是必填项且必须为字符串"), nil
	}

	// URL 安全检查: 拦截 SSRF 风险地址
	if safe, reason := CheckURLSafety(url); !safe {
		slog.Warn("browser_navigate: URL safety check failed", "url", url, "reason", reason)
		return ToolError(fmt.Sprintf("URL 安全检查未通过: %s", reason)), nil
	}

	slog.Info("browser navigate", "url", url)

	if err := page.Timeout(30 * time.Second).Navigate(url); err != nil {
		slog.Error("browser navigation failed", "url", url, "err", err)
		return ToolError(fmt.Sprintf("导航失败: %v", err)), nil
	}

	// 等待页面加载
	_ = page.Timeout(10 * time.Second).WaitLoad()

	title := page.MustInfo().Title
	body, _ := page.Element("body")
	text := ""
	if body != nil {
		text, _ = body.Text()
	}

	if len(text) > 30000 {
		text = text[:30000] + "\n...[页面内容已截断]"
	}

	result, err := json.Marshal(map[string]any{
		"output": fmt.Sprintf("已导航到 %s", url),
		"url":    url,
		"title":  title,
		"text":   text,
	})
	if err != nil {
		return ToolError(fmt.Sprintf("序列化结果失败: %v", err)), nil
	}
	return string(result), nil
}

// ───────────────────────────── 浏览器截图工具 ─────────────────────────────

// BrowserScreenshotTool 实现页面截图功能。
type BrowserScreenshotTool struct{}

// Name 返回工具名称。
func (t *BrowserScreenshotTool) Name() string { return "browser_screenshot" }

// Description 返回工具描述。
func (t *BrowserScreenshotTool) Description() string {
	return "对当前页面或指定元素进行截图。返回 base64 编码的图片数据。"
}

// Toolset 返回工具所属工具集。
func (t *BrowserScreenshotTool) Toolset() string { return "browser" }

// Emoji 返回工具图标。
func (t *BrowserScreenshotTool) Emoji() string { return "📸" }

// IsAvailable 检查浏览器是否可用（本地 Chrome 或 Browserbase 云浏览器）。
func (t *BrowserScreenshotTool) IsAvailable() bool {
	return browserCheck()
}

// MaxResultChars 返回结果最大字符数 (截图结果较大)。
func (t *BrowserScreenshotTool) MaxResultChars() int { return 200000 }

// Schema 返回工具的 JSON Schema。
func (t *BrowserScreenshotTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "browser_screenshot",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"selector": map[string]any{
					"type":        "string",
					"description": "CSS 选择器，对指定元素截图。为空则截取整个页面。",
				},
			},
		},
	}
}

// Execute 执行浏览器截图。
func (t *BrowserScreenshotTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	page, err := getPage(ctx)
	if err != nil {
		return ToolError(fmt.Sprintf("浏览器不可用: %v", err)), nil
	}

	var screenshot []byte

	if selector, ok := args["selector"].(string); ok && selector != "" {
		el, elErr := page.Timeout(10 * time.Second).Element(selector)
		if elErr != nil {
			return ToolError(fmt.Sprintf("未找到元素 %s: %v", selector, elErr)), nil
		}
		screenshot, err = el.Screenshot(proto.PageCaptureScreenshotFormatPng, 90)
	} else {
		screenshot, err = page.Screenshot(true, nil)
	}

	if err != nil {
		slog.Error("screenshot failed", "err", err)
		return ToolError(fmt.Sprintf("截图失败: %v", err)), nil
	}

	result, err := json.Marshal(map[string]any{
		"output": fmt.Sprintf("截图成功 (%d 字节)", len(screenshot)),
		"image":  fmt.Sprintf("[base64 图片数据, %d 字节]", len(screenshot)),
		"size":   len(screenshot),
	})
	if err != nil {
		return ToolError(fmt.Sprintf("序列化结果失败: %v", err)), nil
	}
	return string(result), nil
}
