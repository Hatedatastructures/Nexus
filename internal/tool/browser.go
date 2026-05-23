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
	browserInstance     *rod.Browser
	pageInstance        *rod.Page
	browserMu           sync.Mutex
	browserReady        bool
	browserControlURL   string            // CDP WebSocket 地址，供 CDP 工具使用
	browserbaseSession  *BrowserbaseSession // Browserbase 云浏览器会话（nil 表示使用本地浏览器）
)

// ensureBrowser 确保浏览器实例已启动并可用。
// 首次调用时启动浏览器，后续调用复用已有实例。
func ensureBrowser() error {
	browserMu.Lock()
	defer browserMu.Unlock()

	if browserReady && browserInstance != nil {
		return nil
	}

	slog.Info("launching browser instance")

	// 优先使用 Browserbase 云浏览器（如果配置了环境变量）
	if cfg, ok := loadBrowserbaseConfig(); ok {
		slog.Info("Browserbase config detected, using cloud browser")
		sess, err := NewBrowserbaseSession(context.Background(), cfg)
		if err != nil {
			return fmt.Errorf("创建 Browserbase 会话失败: %w", err)
		}
		browserbaseSession = sess
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

// getBrowser 返回浏览器实例。
func getBrowser() (*rod.Browser, error) {
	if err := ensureBrowser(); err != nil {
		return nil, err
	}
	return browserInstance, nil
}

// getPage 返回当前页面实例，如果不存在则创建新页面。
func getPage() (*rod.Page, error) {
	browser, err := getBrowser()
	if err != nil {
		return nil, err
	}

	if pageInstance != nil {
		// 检查页面是否仍然有效
		_, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, evalErr := pageInstance.Eval(`() => document.title`)
		if evalErr == nil {
			return pageInstance, nil
		}
		// 页面无效，重新创建
		pageInstance = nil
	}

	pageInstance = browser.MustPage()
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
	page, err := getPage()
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
	page.Timeout(10 * time.Second).WaitLoad()

	title := page.MustInfo().Title
	body, _ := page.Element("body")
	text := ""
	if body != nil {
		text, _ = body.Text()
	}

	if len(text) > 30000 {
		text = text[:30000] + "\n...[页面内容已截断]"
	}

	result, _ := json.Marshal(map[string]any{
		"output": fmt.Sprintf("已导航到 %s", url),
		"url":    url,
		"title":  title,
		"text":   text,
	})

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
	page, err := getPage()
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

	result, _ := json.Marshal(map[string]any{
		"output": fmt.Sprintf("截图成功 (%d 字节)", len(screenshot)),
		"image":  fmt.Sprintf("[base64 图片数据, %d 字节]", len(screenshot)),
		"size":   len(screenshot),
	})

	return string(result), nil
}

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
	page, err := getPage()
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

	if err := el.Timeout(5 * time.Second).Click(proto.InputMouseButtonLeft, 1); err != nil {
		slog.Error("click element failed", "selector", selector, "err", err)
		return ToolError(fmt.Sprintf("点击失败: %v", err)), nil
	}

	// 等待可能的页面变化
	page.Timeout(3 * time.Second).WaitLoad()

	result, _ := json.Marshal(map[string]any{
		"output":   fmt.Sprintf("已点击元素: %s", selector),
		"selector": selector,
	})

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
	page, err := getPage()
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

	result, _ := json.Marshal(map[string]any{
		"output":   fmt.Sprintf("已在 %s 中输入文本 (%d 字符)", selector, len(text)),
		"selector": selector,
		"length":   len(text),
	})

	return string(result), nil
}

// ───────────────────────────── init 注册 ─────────────────────────────

func init() {
	reg := GetRegistry()
	reg.Register(&BrowserNavigateTool{})
	reg.Register(&BrowserScreenshotTool{})
	reg.Register(&BrowserClickTool{})
	reg.Register(&BrowserTypeTool{})
}
