// Package tool 提供网页内容提取和爬取工具。
// WebExtractTool 从指定 URL 提取页面内容，
// WebCrawlTool 支持带指令的深度网页爬取。
package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"
)

// ───────────────────────────── 网页提取工具 ─────────────────────────────

// WebExtractTool 实现网页内容提取功能。
// 从指定 URL 提取内容，支持 HTML 和 Markdown 格式。
// 标记为异步工具 (is_async=true) 以支持长时间运行的提取操作。
type WebExtractTool struct {
	client *http.Client
	once   sync.Once
}

// Name 返回工具名称。
func (t *WebExtractTool) Name() string { return "web_extract" }

// Description 返回工具描述。
func (t *WebExtractTool) Description() string {
	return "从指定的网页 URL 提取内容。支持多个 URL 批量提取，输出格式可选 HTML 或 Markdown。"
}

// Toolset 返回工具所属工具集。
func (t *WebExtractTool) Toolset() string { return "web" }

// Emoji 返回工具图标。
func (t *WebExtractTool) Emoji() string { return "📄" }

// IsAvailable 始终可用 (直接 HTTP 请求)。
func (t *WebExtractTool) IsAvailable() bool { return true }

// MaxResultChars 返回结果最大字符数。
func (t *WebExtractTool) MaxResultChars() int { return 100000 }

// Schema 返回工具的 JSON Schema。
func (t *WebExtractTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "web_extract",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"urls": map[string]any{
					"type":        "array",
					"description": "要提取的 URL 列表",
					"items": map[string]any{
						"type": "string",
					},
				},
				"format": map[string]any{
					"type":        "string",
					"description": "输出格式: html 或 markdown，默认 markdown",
					"enum":        []string{"html", "markdown"},
				},
			},
			"required": []string{"urls"},
		},
	}
}

// Execute 执行网页提取。
// 对每个 URL 发起 HTTP GET 请求并提取内容。
func (t *WebExtractTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	urlsRaw, ok := args["urls"]
	if !ok {
		return ToolError("参数 urls 是必填项"), nil
	}

	var urls []string
	switch v := urlsRaw.(type) {
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				urls = append(urls, s)
			}
		}
	case []string:
		urls = v
	default:
		return ToolError("参数 urls 必须是字符串数组"), nil
	}

	if len(urls) == 0 {
		return ToolError("参数 urls 不能为空数组"), nil
	}

	const maxExtractURLs = 10
	if len(urls) > maxExtractURLs {
		urls = urls[:maxExtractURLs]
	}

	format := "markdown"
	if f, ok := args["format"].(string); ok && f == "html" {
		format = "html"
	}

	// 确保 HTTP 客户端已初始化（并发安全）
	t.once.Do(func() {
		t.client = newSafeHTTPClient(30 * time.Second)
	})

	var results []map[string]any
	for _, targetURL := range urls {
		// 验证 URL
		parsedURL, err := url.Parse(targetURL)
		if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
			results = append(results, map[string]any{
				"url":   targetURL,
				"error": fmt.Sprintf("无效的 URL: %v", err),
			})
			continue
		}

		// URL 安全检查: 拦截 SSRF 风险地址
		if safe, reason := CheckURLSafety(targetURL); !safe {
			slog.Warn("web_extract: URL safety check failed", "url", targetURL, "reason", reason)
			results = append(results, map[string]any{
				"url":   targetURL,
				"error": fmt.Sprintf("URL 安全检查未通过: %s", reason),
			})
			continue
		}

		content, extractErr := t.extractURL(ctx, targetURL)
		if extractErr != nil {
			slog.Warn("web page extraction failed", "url", targetURL, "err", extractErr)
			results = append(results, map[string]any{
				"url":   targetURL,
				"error": extractErr.Error(),
			})
			continue
		}

		results = append(results, map[string]any{
			"url":     targetURL,
			"content": truncateString(content, 50000),
			"format":  format,
		})
	}

	result, err := json.Marshal(map[string]any{
		"extracted": len(results),
		"total":     len(urls),
		"format":    format,
		"results":   results,
	})
	if err != nil {
		return ToolError(fmt.Sprintf("序列化结果失败: %v", err)), nil
	}

	return string(result), nil
}

// ───────────────────────────── 网页爬取工具 ─────────────────────────────

// WebCrawlTool 实现带指令的网页爬取功能。
type WebCrawlTool struct {
	client *http.Client
	once   sync.Once
}

// Name 返回工具名称。
func (t *WebCrawlTool) Name() string { return "web_crawl" }

// Description 返回工具描述。
func (t *WebCrawlTool) Description() string {
	return "爬取指定网站并按指令提取信息。支持深度爬取和自定义提取规则。"
}

func (t *WebCrawlTool) Toolset() string     { return "web" }
func (t *WebCrawlTool) Emoji() string       { return "🕷️" }
func (t *WebCrawlTool) MaxResultChars() int { return 100000 }

// IsAvailable 检查是否有可用的爬取后端。
func (t *WebCrawlTool) IsAvailable() bool {
	return os.Getenv("FIRECRAWL_API_KEY") != ""
}

// Schema 返回工具的 JSON Schema。
func (t *WebCrawlTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "web_crawl",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "要爬取的起始 URL",
				},
				"instructions": map[string]any{
					"type":        "string",
					"description": "提取指令，描述需要从页面中提取什么信息",
				},
				"max_pages": map[string]any{
					"type":        "integer",
					"description": "最大爬取页面数，默认 5",
				},
			},
			"required": []string{"url", "instructions"},
		},
	}
}

// Execute 执行网页爬取。
func (t *WebCrawlTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	targetURL, _ := args["url"].(string)
	if targetURL == "" {
		return ToolError("参数 url 是必填项"), nil
	}

	instructions, _ := args["instructions"].(string)
	if instructions == "" {
		return ToolError("参数 instructions 是必填项"), nil
	}

	maxPages := 5
	if v, ok := args["max_pages"].(float64); ok && v > 0 {
		maxPages = int(v)
		if maxPages > 20 {
			maxPages = 20
		}
	}

	// URL 安全检查: 拦截 SSRF 风险地址
	if safe, reason := CheckURLSafety(targetURL); !safe {
		slog.Warn("web_crawl: URL safety check failed", "url", targetURL, "reason", reason)
		return ToolError(fmt.Sprintf("URL 安全检查未通过: %s", reason)), nil
	}

	apiKey := os.Getenv("FIRECRAWL_API_KEY")
	if apiKey == "" {
		return ToolError("爬取功能需要 FIRECRAWL_API_KEY 环境变量"), nil
	}

	// 确保 HTTP 客户端已初始化（并发安全）
	t.once.Do(func() {
		t.client = newSafeHTTPClient(120 * time.Second)
	})

	// 使用 Firecrawl 的 crawl 端点
	reqBody := map[string]any{
		"url":   targetURL,
		"limit": maxPages,
		"scrapeOptions": map[string]any{
			"formats":         []string{"markdown"},
			"onlyMainContent": true,
		},
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return ToolError(fmt.Sprintf("序列化请求体失败: %v", err)), nil
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.firecrawl.dev/v1/crawl", bytes.NewReader(bodyBytes))
	if err != nil {
		return ToolError(fmt.Sprintf("创建请求失败: %v", err)), nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := t.client.Do(req)
	if err != nil {
		return ToolError(fmt.Sprintf("爬取请求失败: %v", err)), nil
	}
	defer func() { _ = resp.Body.Close() }()

	return parseCrawlResponse(resp, targetURL, instructions)
}
