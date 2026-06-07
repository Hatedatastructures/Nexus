// Package tool 提供网页搜索工具。
// 支持多后端提供商 (Exa, Firecrawl, Tavily, Parallel 等)，
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
	"os"
	"sync"
	"time"
)

// ───────────────────────────── 网页搜索工具 ─────────────────────────────

// newSafeHTTPClient creates an HTTP client that validates redirect URLs for SSRF protection.
func newSafeHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if safe, _ := CheckURLSafety(req.URL.String()); !safe {
				return fmt.Errorf("redirect to unsafe URL blocked: %s", req.URL.String())
			}
			return nil
		},
	}
}

// WebSearchTool 实现网页搜索功能。
// 支持通过环境变量选择搜索后端。
type WebSearchTool struct {
	client *http.Client
	once   sync.Once
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
		os.Getenv("FIRECRAWL_API_KEY") != "" ||
		os.Getenv("PARALLEL_API_KEY") != ""
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

	// 确保 HTTP 客户端已初始化（并发安全）
	t.once.Do(func() {
		t.client = newSafeHTTPClient(30 * time.Second)
	})

	// 尝试搜索后端 (按优先级)
	var results []map[string]any
	var err error

	backend := os.Getenv("NEXUS_WEB_SEARCH_BACKEND")
	switch backend {
	case "firecrawl":
		if apiKey := os.Getenv("FIRECRAWL_API_KEY"); apiKey != "" {
			results, err = t.searchFirecrawl(ctx, apiKey, query, numResults)
		}
	case "exa":
		if apiKey := os.Getenv("EXA_API_KEY"); apiKey != "" {
			results, err = t.searchExa(ctx, apiKey, query, numResults)
		}
	case "parallel":
		if apiKey := os.Getenv("PARALLEL_API_KEY"); apiKey != "" {
			results, err = t.searchParallel(ctx, apiKey, query, numResults)
		}
	default:
		// 自动检测
		if apiKey := os.Getenv("TAVILY_API_KEY"); apiKey != "" {
			results, err = t.searchTavily(ctx, apiKey, query, numResults)
		} else if apiKey := os.Getenv("EXA_API_KEY"); apiKey != "" {
			results, err = t.searchExa(ctx, apiKey, query, numResults)
		} else if apiKey := os.Getenv("FIRECRAWL_API_KEY"); apiKey != "" {
			results, err = t.searchFirecrawl(ctx, apiKey, query, numResults)
		} else if apiKey := os.Getenv("PARALLEL_API_KEY"); apiKey != "" {
			results, err = t.searchParallel(ctx, apiKey, query, numResults)
		} else {
			return ToolError("未配置搜索后端。请设置 TAVILY_API_KEY / EXA_API_KEY / FIRECRAWL_API_KEY / PARALLEL_API_KEY 环境变量。"), nil
		}
	}

	if err != nil {
		slog.Error("web search failed", "query", query, "err", err)
		return ToolError(fmt.Sprintf("搜索失败: %v", err)), nil
	}

	result, err := json.Marshal(map[string]any{
		"query":   query,
		"results": results,
		"count":   len(results),
	})
	if err != nil {
		return ToolError(fmt.Sprintf("序列化结果失败: %v", err)), nil
	}
	return string(result), nil
}

// searchTavily 使用 Tavily API 进行搜索。
func (t *WebSearchTool) searchTavily(ctx context.Context, apiKey, query string, numResults int) ([]map[string]any, error) {
	reqBody := map[string]any{
		"query":          query,
		"max_results":    numResults,
		"search_depth":   "basic",
		"include_answer": true,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.tavily.com/search", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		slog.Warn("Tavily API error response", "status", resp.StatusCode, "body", string(body))
		return nil, fmt.Errorf("tavily API 返回错误 (HTTP %d)", resp.StatusCode)
	}

	var result struct {
		Answer  string `json:"answer"`
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&result); err != nil {
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
		"query":      query,
		"numResults": numResults,
		"contents": map[string]any{
			"text": map[string]any{
				"maxCharacters": 1000,
			},
		},
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}

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
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		slog.Warn("Exa API error response", "status", resp.StatusCode, "body", string(body))
		return nil, fmt.Errorf("exa API 返回错误 (HTTP %d)", resp.StatusCode)
	}

	var result struct {
		Results []struct {
			Title string `json:"title"`
			URL   string `json:"url"`
			Text  string `json:"text"`
		} `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&result); err != nil {
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

// searchFirecrawl 使用 Firecrawl API 进行搜索。
func (t *WebSearchTool) searchFirecrawl(ctx context.Context, apiKey, query string, numResults int) ([]map[string]any, error) {
	reqBody := map[string]any{
		"query":         query,
		"pageOptions":   map[string]any{"onlyMainContent": true},
		"searchOptions": map[string]any{"limit": numResults},
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.firecrawl.dev/v1/search", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		slog.Warn("Firecrawl API error response", "status", resp.StatusCode, "body", string(body))
		return nil, fmt.Errorf("firecrawl API 返回错误 (HTTP %d)", resp.StatusCode)
	}

	var result struct {
		Success bool `json:"success"`
		Data    []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&result); err != nil {
		return nil, err
	}

	var results []map[string]any
	for _, r := range result.Data {
		results = append(results, map[string]any{
			"title":   r.Title,
			"url":     r.URL,
			"content": r.Content,
		})
	}
	return results, nil
}

// searchParallel 使用 Parallel API 进行搜索。
func (t *WebSearchTool) searchParallel(ctx context.Context, apiKey, query string, numResults int) ([]map[string]any, error) {
	reqBody := map[string]any{
		"query": query,
		"limit": numResults,
		"depth": "basic",
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.parallel.ai/v1/search", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		slog.Warn("Parallel API error response", "status", resp.StatusCode, "body", string(body))
		return nil, fmt.Errorf("parallel API 返回错误 (HTTP %d)", resp.StatusCode)
	}

	var result struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&result); err != nil {
		return nil, err
	}

	var results []map[string]any
	for _, r := range result.Results {
		results = append(results, map[string]any{
			"title":   r.Title,
			"url":     r.URL,
			"content": r.Content,
		})
	}
	return results, nil
}

// truncateString 截断字符串到指定长度。
func truncateString(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "\n...[内容已截断]"
}

