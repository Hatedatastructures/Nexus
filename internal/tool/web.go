// Package tool 提供网页搜索和内容提取工具。
// 支持多后端提供商 (Exa, Firecrawl, Tavily 等)，
// 通过环境变量或配置文件选择后端。
package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// ───────────────────────────── 网页搜索工具 ─────────────────────────────

// WebSearchTool 实现网页搜索功能。
// 支持通过环境变量选择搜索后端。
type WebSearchTool struct {
	client *http.Client
}

// Name 返回工具名称。
func (t *WebSearchTool) Name() string { return "web_search" }

// Description 返回工具描述。
func (t *WebSearchTool) Description() string {
	return "在互联网上搜索信息。返回相关网页的标题、URL 和摘要。"
}

// Toolset 返回工具所属工具集。
func (t *WebSearchTool) Toolset() string { return "web" }

// Emoji 返回工具图标。
func (t *WebSearchTool) Emoji() string { return "🌐" }

// IsAvailable 检查是否有可用的搜索后端。
func (t *WebSearchTool) IsAvailable() bool {
	return os.Getenv("TAVILY_API_KEY") != "" ||
		os.Getenv("EXA_API_KEY") != "" ||
		os.Getenv("FIRECRAWL_API_KEY") != ""
}

// MaxResultChars 返回结果最大字符数。
func (t *WebSearchTool) MaxResultChars() int { return 50000 }

// Schema 返回工具的 JSON Schema。
func (t *WebSearchTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "web_search",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "搜索查询字符串",
				},
				"num_results": map[string]any{
					"type":        "integer",
					"description": "返回结果数量，默认 5",
				},
			},
			"required": []string{"query"},
		},
	}
}

// Execute 执行网页搜索。
// 自动检测可用的搜索后端，优先使用 Tavily。
func (t *WebSearchTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	query, ok := args["query"].(string)
	if !ok || query == "" {
		return ToolError("参数 query 是必填项且必须为字符串"), nil
	}

	numResults := 5
	if v, ok := args["num_results"].(float64); ok && v > 0 {
		numResults = int(v)
		if numResults > 20 {
			numResults = 20
		}
	}

	if t.client == nil {
		t.client = &http.Client{Timeout: 30 * time.Second}
	}

	// 尝试搜索后端 (按优先级)
	var results []map[string]any
	var err error

	if apiKey := os.Getenv("TAVILY_API_KEY"); apiKey != "" {
		results, err = t.searchTavily(ctx, apiKey, query, numResults)
	} else if apiKey := os.Getenv("EXA_API_KEY"); apiKey != "" {
		results, err = t.searchExa(ctx, apiKey, query, numResults)
	} else {
		return ToolError("未配置搜索后端。请设置 TAVILY_API_KEY 或 EXA_API_KEY 环境变量。"), nil
	}

	if err != nil {
		slog.Error("网页搜索失败", "query", query, "err", err)
		return ToolError(fmt.Sprintf("搜索失败: %v", err)), nil
	}

	result, _ := json.Marshal(map[string]any{
		"query":   query,
		"results": results,
		"count":   len(results),
	})

	return string(result), nil
}

// searchTavily 使用 Tavily API 进行搜索。
func (t *WebSearchTool) searchTavily(ctx context.Context, apiKey, query string, numResults int) ([]map[string]any, error) {
	reqBody := map[string]any{
		"api_key":        apiKey,
		"query":          query,
		"max_results":    numResults,
		"search_depth":   "basic",
		"include_answer": true,
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.tavily.com/search", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Answer  string `json:"answer"`
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	var results []map[string]any
	if result.Answer != "" {
		results = append(results, map[string]any{
			"title":   "AI 摘要",
			"url":     "",
			"content": result.Answer,
		})
	}
	for _, r := range result.Results {
		results = append(results, map[string]any{
			"title":   r.Title,
			"url":     r.URL,
			"content": r.Content,
		})
	}
	return results, nil
}

// searchExa 使用 Exa API 进行搜索。
func (t *WebSearchTool) searchExa(ctx context.Context, apiKey, query string, numResults int) ([]map[string]any, error) {
	reqBody := map[string]any{
		"query":    query,
		"numResults": numResults,
		"contents": map[string]any{
			"text": map[string]any{
				"maxCharacters": 1000,
			},
		},
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.exa.ai/search", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Text    string `json:"text"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	var results []map[string]any
	for _, r := range result.Results {
		results = append(results, map[string]any{
			"title":   r.Title,
			"url":     r.URL,
			"content": r.Text,
		})
	}
	return results, nil
}

// ───────────────────────────── 网页提取工具 ─────────────────────────────

// WebExtractTool 实现网页内容提取功能。
// 从指定 URL 提取内容，支持 HTML 和 Markdown 格式。
// 标记为异步工具 (is_async=true) 以支持长时间运行的提取操作。
type WebExtractTool struct {
	client *http.Client
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

	format := "markdown"
	if f, ok := args["format"].(string); ok && f == "html" {
		format = "html"
	}

	if t.client == nil {
		t.client = &http.Client{Timeout: 30 * time.Second}
	}

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

		content, extractErr := t.extractURL(ctx, targetURL)
		if extractErr != nil {
			slog.Warn("网页提取失败", "url", targetURL, "err", extractErr)
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

	result, _ := json.Marshal(map[string]any{
		"extracted": len(results),
		"total":     len(urls),
		"format":    format,
		"results":   results,
	})

	return string(result), nil
}

// extractURL 从指定 URL 提取内容。
func (t *WebExtractTool) extractURL(ctx context.Context, targetURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", targetURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "NexusAgent/1.0 (Web Extract)")

	resp, err := t.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20)) // 限制读取 5MB
	if err != nil {
		return "", err
	}

	content := string(body)

	// 简单的 HTML 到文本转换
	content = stripHTMLTags(content)
	content = strings.TrimSpace(content)

	return content, nil
}

// ───────────────────────────── HTML 剥离 ─────────────────────────────

// stripHTMLTags 移除 HTML 标签，保留纯文本。
// 简化实现，用于提取网页正文。
func stripHTMLTags(html string) string {
	var result strings.Builder
	inTag := false
	inScript := false
	inStyle := false

	for i := 0; i < len(html); i++ {
		ch := html[i]

		if inScript {
			if i+8 < len(html) && strings.ToLower(html[i:i+9]) == "</script>" {
				inScript = false
				i += 8
			}
			continue
		}
		if inStyle {
			if i+7 < len(html) && strings.ToLower(html[i:i+8]) == "</style>" {
				inStyle = false
				i += 7
			}
			continue
		}

		if ch == '<' {
			inTag = true
			// 检测 script/style 标签
			remaining := html[i:]
			if len(remaining) > 7 && strings.ToLower(remaining[0:7]) == "<script" {
				inScript = true
			}
			if len(remaining) > 6 && strings.ToLower(remaining[0:6]) == "<style" {
				inStyle = true
			}
			continue
		}
		if ch == '>' {
			inTag = false
			result.WriteByte(' ')
			continue
		}
		if !inTag {
			result.WriteByte(ch)
		}
	}

	// 清理多余空白
	text := result.String()
	lines := strings.Split(text, "\n")
	var cleaned []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	return strings.Join(cleaned, "\n")
}

// truncateString 截断字符串到指定长度。
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "\n...[内容已截断]"
}

// ───────────────────────────── init 注册 ─────────────────────────────

func init() {
	reg := GetRegistry()
	reg.Register(&WebSearchTool{})

	// web_extract 注册并标记为异步工具
	reg.Register(&WebExtractTool{})
	if entry := reg.GetEntry("web_extract"); entry != nil {
		entry.IsAsync = true
	}
}
