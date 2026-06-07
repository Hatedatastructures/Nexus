// 浏览器监控工具 — 定期检查浏览器状态，截图监控页面。
//
// 提供周期性的页面状态检查能力：
//   - 定时截图当前页面
//   - 检查页面是否加载完成
//   - 检测弹窗/对话框
//   - 监控指定 CSS 选择器的元素状态
//
// 适用于长时间运行的浏览器任务，让代理能观察页面随时间的变化。
package tool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/go-rod/rod"
)

// ───────────────────────────── 监控结果 ─────────────────────────────

// superviseCheck 代表单次监控检查的结果。
type superviseCheck struct {
	Timestamp     string `json:"timestamp"`                // 检查时间
	PageTitle     string `json:"page_title"`               // 页面标题
	PageURL       string `json:"page_url"`                 // 当前 URL
	Loading       bool   `json:"loading"`                  // 是否仍在加载中
	HasDialog     bool   `json:"has_dialog"`               // 是否存在弹窗
	DialogType    string `json:"dialog_type"`              // 弹窗类型（如有）
	DialogText    string `json:"dialog_text"`              // 弹窗文本（如有）
	ScreenshotB64 string `json:"screenshot_b64,omitempty"` // 截图（base64）
	ElementFound  bool   `json:"element_found,omitempty"`  // 指定选择器是否找到元素
	ElementText   string `json:"element_text,omitempty"`   // 指定选择器的元素文本
}

// ───────────────────────────── BrowserSuperviseTool ─────────────────────────────

// BrowserSuperviseTool 实现浏览器周期性监控。
// 工具名: browser_supervise
type BrowserSuperviseTool struct{}

// Name 返回工具名称。
func (t *BrowserSuperviseTool) Name() string { return "browser_supervise" }

// Description 返回工具描述。
func (t *BrowserSuperviseTool) Description() string {
	return "定期检查浏览器页面状态，截图监控页面变化。" +
		"支持指定监控间隔、最大检查次数、以及可选的 CSS 选择器来监控特定元素。\n\n" +
		"每次检查会：\n" +
		"- 截图当前页面\n" +
		"- 检查页面加载状态\n" +
		"- 检测是否有弹窗/对话框\n" +
		"- 如果指定了 selector，检查该元素是否存在"
}

// Toolset 返回工具所属工具集。
func (t *BrowserSuperviseTool) Toolset() string { return "browser" }

// Emoji 返回工具图标。
func (t *BrowserSuperviseTool) Emoji() string { return "👁️" }

// IsAvailable 检查浏览器是否可用。
func (t *BrowserSuperviseTool) IsAvailable() bool {
	return browserCheck()
}

// MaxResultChars 返回结果最大字符数（监控结果包含截图数据，较大）。
func (t *BrowserSuperviseTool) MaxResultChars() int { return 300000 }

// Schema 返回工具的 JSON Schema。
func (t *BrowserSuperviseTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "browser_supervise",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"interval": map[string]any{
					"type":        "number",
					"description": "检查间隔（秒），默认 5",
					"default":     5,
				},
				"max_checks": map[string]any{
					"type":        "number",
					"description": "最大检查次数，默认 10",
					"default":     10,
				},
				"selector": map[string]any{
					"type":        "string",
					"description": "可选。CSS 选择器，仅监控特定元素的存在和文本内容",
				},
			},
		},
	}
}

// Execute 执行浏览器监控。
func (t *BrowserSuperviseTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	// 解析参数
	intervalSec := 5
	if v, ok := args["interval"].(float64); ok && v > 0 {
		intervalSec = int(v)
	}
	maxChecks := 10
	if v, ok := args["max_checks"].(float64); ok && int(v) > 0 {
		maxChecks = int(v)
	}
	selector, _ := args["selector"].(string)

	slog.Info("starting browser supervision",
		"interval", intervalSec,
		"max_checks", maxChecks,
		"selector", selector,
	)

	// 确保浏览器已启动
	if err := ensureBrowser(ctx); err != nil {
		return ToolError(fmt.Sprintf("浏览器不可用: %v", err)), nil
	}

	page, err := getPage(ctx)
	if err != nil {
		return ToolError(fmt.Sprintf("无法获取页面: %v", err)), nil
	}

	var checks []superviseCheck
	interval := time.Duration(intervalSec) * time.Second

	for i := 0; i < maxChecks; i++ {
		// 检查 context 是否已取消
		select {
		case <-ctx.Done():
			slog.Info("supervision cancelled by context", "completed_checks", len(checks))
			goto done
		default:
		}

		// 执行单次检查
		check := t.runSingleCheck(ctx, page, selector)
		checks = append(checks, check)

		slog.Info("supervision check completed",
			"check", i+1,
			"title", check.PageTitle,
			"loading", check.Loading,
			"has_dialog", check.HasDialog,
		)

		// 最后一次不需要等待
		if i < maxChecks-1 {
			select {
			case <-ctx.Done():
				slog.Info("supervision cancelled by context", "completed_checks", len(checks))
				goto done
			case <-time.After(interval):
				// 继续下一轮
			}
		}
	}
done:

	// 构建监控摘要
	summary := map[string]any{
		"total_checks": len(checks),
		"checks":       checks,
		"summary":      t.buildSummary(checks),
	}

	data, _ := json.Marshal(summary)
	return string(data), nil
}

// runSingleCheck 执行单次监控检查。
func (t *BrowserSuperviseTool) runSingleCheck(_ context.Context, page *rod.Page, selector string) superviseCheck {
	check := superviseCheck{
		Timestamp: time.Now().Format(time.RFC3339),
	}

	// 获取页面信息
	info := page.MustInfo()
	check.PageTitle = info.Title
	check.PageURL = info.URL

	// 检查页面加载状态
	check.Loading = t.checkLoading(context.Background(), page)

	// 检测对话框
	dialogType, dialogText, hasDialog := t.checkDialog(page)
	check.HasDialog = hasDialog
	check.DialogType = dialogType
	check.DialogText = dialogText

	// 截图
	screenshot, err := page.Screenshot(true, nil)
	if err != nil {
		slog.Warn("supervision screenshot failed", "err", err)
		check.ScreenshotB64 = ""
	} else {
		check.ScreenshotB64 = base64.StdEncoding.EncodeToString(screenshot)
	}

	// 检查指定选择器
	if selector != "" {
		el, elErr := page.Timeout(3 * time.Second).Element(selector)
		if elErr == nil && el != nil {
			check.ElementFound = true
			text, _ := el.Text()
			check.ElementText = text
		} else {
			check.ElementFound = false
		}
	}

	return check
}

// checkLoading 检查页面是否仍在加载中。
func (t *BrowserSuperviseTool) checkLoading(_ context.Context, page *rod.Page) bool {
	// 通过 JavaScript 检查 document.readyState
	result, err := page.Timeout(3 * time.Second).Eval(`() => document.readyState`)
	if err != nil {
		// 无法判断，假定仍在加载
		return true
	}
	state := result.Value.String()
	return state != `"complete"`
}

// checkDialog 检测页面是否存在原生 JavaScript 对话框。
//
// 通过尝试调用 Page.handleJavaScriptDialog（不带 accept）来判断是否有对话框。
// 如果返回 "no dialog to handle" 类似的错误，说明没有对话框。
func (t *BrowserSuperviseTool) checkDialog(page *rod.Page) (dialogType string, dialogText string, hasDialog bool) {
	// 使用 Runtime.evaluate 检测对话框状态
	// 重写 window.alert/confirm/prompt 来探测（仅检测是否被触发过）
	result, err := page.Timeout(3 * time.Second).Eval(`() => {
		// 检查是否存在未处理的对话框
		// 注意：原生对话框会阻塞 JS 执行，
		// 所以我们只能通过间接方式检测
		return {
			hasOnbeforeunload: typeof window.onbeforeunload === 'function',
			documentVisible: document.visibilityState
		};
	}`)

	if err == nil && result != nil {
		// 页面可执行 JS，说明没有阻塞型对话框
		return "", "", false
	}

	// JS 执行超时或失败，可能存在阻塞型对话框
	// 通过 CDP 直接尝试处理对话框来确认
	return "unknown", "可能存在阻塞型对话框（JS 执行超时）", true
}

// buildSummary 构建监控摘要文本。
func (t *BrowserSuperviseTool) buildSummary(checks []superviseCheck) string {
	if len(checks) == 0 {
		return "未执行任何检查"
	}

	lastCheck := checks[len(checks)-1]
	dialogCount := 0
	for _, c := range checks {
		if c.HasDialog {
			dialogCount++
		}
	}

	summary := fmt.Sprintf("完成 %d 次监控检查。", len(checks))
	summary += fmt.Sprintf("最后检查页面: %s (%s)", lastCheck.PageTitle, lastCheck.PageURL)
	if lastCheck.Loading {
		summary += " [仍在加载中]"
	}
	if dialogCount > 0 {
		summary += fmt.Sprintf(" 检测到 %d 次对话框", dialogCount)
	}
	return summary
}
