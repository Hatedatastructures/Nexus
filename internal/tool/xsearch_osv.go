// Package tool 提供 X/Twitter 搜索和 OSV 漏洞查询工具。
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
	"sync"
	"time"
)

// ───────────────────────────── X/Twitter 搜索工具 ─────────────────────────────

// XSearchTool 通过 Twitter API v2 搜索推文。
type XSearchTool struct {
	client *http.Client
	once   sync.Once
}

func (t *XSearchTool) Name() string        { return "x_search" }
func (t *XSearchTool) Toolset() string     { return "security" }
func (t *XSearchTool) Emoji() string       { return "" }
func (t *XSearchTool) MaxResultChars() int { return 50000 }

func (t *XSearchTool) Description() string {
	return "搜索 X/Twitter 上的推文。返回作者、内容、时间和互动指标。"
}

func (t *XSearchTool) IsAvailable() bool { return os.Getenv("X_BEARER_TOKEN") != "" }

func (t *XSearchTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "x_search",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "搜索查询字符串，支持 Twitter 搜索语法",
				},
				"count": map[string]any{
					"type":        "integer",
					"description": "返回结果数量，默认 10，最大 50",
				},
			},
			"required": []string{"query"},
		},
	}
}

func (t *XSearchTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	query, ok := args["query"].(string)
	if !ok || query == "" {
		return ToolError("参数 query 是必填项且必须为字符串"), nil
	}

	count := 10
	if v, ok := args["count"].(float64); ok && v > 0 {
		count = int(v)
		if count > 50 {
			count = 50
		}
	}

	bearerToken := os.Getenv("X_BEARER_TOKEN")
	if bearerToken == "" {
		return ToolError("X_BEARER_TOKEN 环境变量未设置"), nil
	}

		t.once.Do(func() {
			t.client = &http.Client{Timeout: 30 * time.Second}
		})
		endpoint := fmt.Sprintf(
		"https://api.twitter.com/2/tweets/search/recent?query=%s&max_results=%d&tweet.fields=created_at,public_metrics,author_id&expansions=author_id&user.fields=username,name",
		url.QueryEscape(query), count,
	)

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return ToolError(fmt.Sprintf("创建请求失败: %v", err)), nil
	}
	req.Header.Set("Authorization", "Bearer "+bearerToken)

	resp, err := t.client.Do(req)
	if err != nil {
		return ToolError(fmt.Sprintf("请求失败: %v", err)), nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ToolError(fmt.Sprintf("读取响应失败: %v", err)), nil
	}

	if resp.StatusCode != http.StatusOK {
		return ToolError(fmt.Sprintf("Twitter API 返回 HTTP %d: %s", resp.StatusCode, string(body))), nil
	}

	var result struct {
		Data []struct {
			ID        string `json:"id"`
			Text      string `json:"text"`
			AuthorID  string `json:"author_id"`
			CreatedAt string `json:"created_at"`
			Metrics   struct {
				Retweets int `json:"retweet_count"`
				Replies  int `json:"reply_count"`
				Likes    int `json:"like_count"`
				Quotes   int `json:"quote_count"`
			} `json:"public_metrics"`
		} `json:"data"`
		Includes struct {
			Users []struct {
				ID       string `json:"id"`
				Name     string `json:"name"`
				Username string `json:"username"`
			} `json:"users"`
		} `json:"includes"`
		Meta struct {
			ResultCount int `json:"result_count"`
		} `json:"meta"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return ToolError(fmt.Sprintf("解析响应失败: %v", err)), nil
	}

	// 用户 ID -> 用户名映射
	userMap := make(map[string]struct{ Name, Username string })
	for _, u := range result.Includes.Users {
		userMap[u.ID] = struct{ Name, Username string }{u.Name, u.Username}
	}

	// 格式化为文本输出
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("搜索 \"%s\" 共找到 %d 条结果:\n\n", query, result.Meta.ResultCount))
	for i, tw := range result.Data {
		a := userMap[tw.AuthorID]
		sb.WriteString(fmt.Sprintf("--- [%d] ---\n", i+1))
		sb.WriteString(fmt.Sprintf("作者: %s (@%s)\n", a.Name, a.Username))
		sb.WriteString(fmt.Sprintf("内容: %s\n", tw.Text))
		sb.WriteString(fmt.Sprintf("时间: %s\n", tw.CreatedAt))
		sb.WriteString(fmt.Sprintf("互动: %d 转发, %d 回复, %d 点赞, %d 引用\n",
			tw.Metrics.Retweets, tw.Metrics.Replies, tw.Metrics.Likes, tw.Metrics.Quotes))
		sb.WriteString(fmt.Sprintf("链接: https://x.com/%s/status/%s\n\n", a.Username, tw.ID))
	}

	return ToolResult(map[string]any{
		"query": query, "count": result.Meta.ResultCount, "output": sb.String(),
	}), nil
}

// ───────────────────────────── OSV 漏洞查询工具 ─────────────────────────────

// OSVCheckTool 通过 OSV.dev API 查询包漏洞。
type OSVCheckTool struct {
	client *http.Client
	once   sync.Once
}

func (t *OSVCheckTool) Name() string        { return "osv_check" }
func (t *OSVCheckTool) Toolset() string     { return "security" }
func (t *OSVCheckTool) Emoji() string       { return "" }
func (t *OSVCheckTool) MaxResultChars() int { return 50000 }

func (t *OSVCheckTool) Description() string {
	return "查询指定软件包的已知漏洞。支持 npm、PyPI、Go 等生态系统。"
}

func (t *OSVCheckTool) IsAvailable() bool { return true }

func (t *OSVCheckTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "osv_check",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"package_name": map[string]any{
					"type":        "string",
					"description": "包名称",
				},
				"version": map[string]any{
					"type":        "string",
					"description": "包版本号（可选）",
				},
				"ecosystem": map[string]any{
					"type":        "string",
					"description": "包生态系统，如 npm、PyPI、Go",
				},
			},
			"required": []string{"package_name", "ecosystem"},
		},
	}
}

func (t *OSVCheckTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	pkgName, ok := args["package_name"].(string)
	if !ok || pkgName == "" {
		return ToolError("参数 package_name 是必填项且必须为字符串"), nil
	}
	ecosystem, ok := args["ecosystem"].(string)
	if !ok || ecosystem == "" {
		return ToolError("参数 ecosystem 是必填项且必须为字符串"), nil
	}
	version, _ := args["version"].(string)

		t.once.Do(func() {
			t.client = &http.Client{Timeout: 30 * time.Second}
		})

	// 构建查询
	reqBody := map[string]any{
		"package": map[string]any{"name": pkgName, "ecosystem": ecosystem},
	}
	if version != "" {
		reqBody["version"] = version
	}
	bodyBytes, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.osv.dev/v1/query", bytes.NewReader(bodyBytes))
	if err != nil {
		return ToolError(fmt.Sprintf("创建请求失败: %v", err)), nil
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return ToolError(fmt.Sprintf("请求失败: %v", err)), nil
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return ToolError(fmt.Sprintf("读取响应失败: %v", err)), nil
	}
	if resp.StatusCode != http.StatusOK {
		return ToolError(fmt.Sprintf("OSV API 返回 HTTP %d: %s", resp.StatusCode, string(respBody))), nil
	}

	var osvResp struct {
		Vulns []struct {
			ID        string `json:"id"`
			Summary   string `json:"summary"`
			Severity  []struct {
				Type  string `json:"type"`
				Score string `json:"score"`
			} `json:"severity"`
			Aliases    []string `json:"aliases"`
			References []struct {
				URL string `json:"url"`
			} `json:"references"`
		} `json:"vulns"`
	}
	if err := json.Unmarshal(respBody, &osvResp); err != nil {
		return ToolError(fmt.Sprintf("解析响应失败: %v", err)), nil
	}

	pkgDesc := pkgName
	if version != "" {
		pkgDesc = pkgName + "@" + version
	}

	// 无漏洞
	if len(osvResp.Vulns) == 0 {
		return ToolResult(map[string]any{
			"package": pkgName, "version": version, "ecosystem": ecosystem,
			"vulnerable": false, "vuln_count": 0,
			"summary": fmt.Sprintf("%s (%s) 未发现已知漏洞", pkgDesc, ecosystem),
		}), nil
	}

	// 格式化漏洞列表
	var vulns []map[string]any
	for _, v := range osvResp.Vulns {
		entry := map[string]any{"id": v.ID, "summary": v.Summary, "aliases": v.Aliases}
		for _, s := range v.Severity {
			entry["severity_type"] = s.Type
			entry["severity_score"] = s.Score
			break
		}
		var refs []string
		for _, r := range v.References {
			refs = append(refs, r.URL)
		}
		if len(refs) > 0 {
			entry["references"] = refs
		}
		vulns = append(vulns, entry)
	}

	slog.Info("OSV 漏洞查询完成", "package", pkgDesc, "ecosystem", ecosystem, "vulns", len(vulns))

	return ToolResult(map[string]any{
		"package": pkgName, "version": version, "ecosystem": ecosystem,
		"vulnerable": true, "vuln_count": len(vulns), "vulns": vulns,
		"summary": fmt.Sprintf("%s (%s) 发现 %d 个已知漏洞", pkgDesc, ecosystem, len(vulns)),
	}), nil
}

// ───────────────────────────── init 注册 ─────────────────────────────

func init() {
	r := GetRegistry()
	r.Register(&XSearchTool{})
	r.Register(&OSVCheckTool{})
}
