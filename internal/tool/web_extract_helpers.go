// Package tool 提供网页内容提取和爬取工具。
// 本文件包含 HTML 剥离、HTTP 客户端初始化等辅助函数。
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

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

// ───────────────────────────── HTTP 客户端 ─────────────────────────────
// newSafeHTTPClient is defined in web.go

// ───────────────────────────── WebExtractTool 辅助 ─────────────────────────────

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
	defer func() { _ = resp.Body.Close() }()

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

// ───────────────────────────── WebCrawlTool 辅助 ─────────────────────────────

// crawlResult 是 Firecrawl API 的响应结构。
type crawlResult struct {
	Success bool `json:"success"`
	Data    []struct {
		Markdown string `json:"markdown"`
		Metadata struct {
			SourceURL string `json:"sourceURL"`
			Title     string `json:"title"`
		} `json:"metadata"`
	} `json:"data"`
	Total int `json:"total"`
}

// parseCrawlResponse 解析 Firecrawl 响应并返回结果。
func parseCrawlResponse(resp *http.Response, targetURL, instructions string) (string, error) {
	var result crawlResult
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&result); err != nil {
		return ToolError(fmt.Sprintf("解析响应失败: %v", err)), nil
	}

	var pages []map[string]any
	for _, d := range result.Data {
		pages = append(pages, map[string]any{
			"url":     d.Metadata.SourceURL,
			"title":   d.Metadata.Title,
			"content": truncateString(d.Markdown, 20000),
		})
	}

	output, err := json.Marshal(map[string]any{
		"url":           targetURL,
		"instructions":  instructions,
		"pages_crawled": len(pages),
		"total":         result.Total,
		"pages":         pages,
	})
	if err != nil {
		return ToolError(fmt.Sprintf("序列化结果失败: %v", err)), nil
	}

	return string(output), nil
}
