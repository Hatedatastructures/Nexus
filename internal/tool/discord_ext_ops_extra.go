// Package tool 提供 Discord 扩展功能工具。
// 本文件包含消息操作、线程管理、API 调用等辅助实现。
package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ───────────────────────────── 消息操作 ─────────────────────────────

// fetchMessages 获取消息。
func (t *DiscordExtTool) fetchMessages(ctx context.Context, args map[string]any) (string, error) {
	channelID := getString(args, "channel_id", "")
	limit := clampLimit(getInt(args, "limit", 50), discordMaxLimit)

	if err := validateDiscordID("channel_id", channelID); err != nil {
		return "", err
	}

	endpoint := fmt.Sprintf("/channels/%s/messages?limit=%d", channelID, limit)

	resp, err := t.callAPI(ctx, "GET", endpoint, nil)
	if err != nil {
		return "", err
	}

	// 从 resp 中提取数组
	var messages []any
	if rawList, ok := resp["raw"].([]any); ok {
		messages = rawList
	} else {
		messages = getListAnyFromMap(resp, "messages")
	}

	var simplified []map[string]any
	for _, msg := range messages {
		if msgMap, ok := msg.(map[string]any); ok {
			author := getMap(msgMap, "author")
			simplified = append(simplified, map[string]any{
				"id":        getString(msgMap, "id", ""),
				"content":   getString(msgMap, "content", ""),
				"author_id": getString(author, "id", ""),
				"author":    getString(author, "username", ""),
				"timestamp": getString(msgMap, "timestamp", ""),
			})
		}
	}

	return jsonResult(map[string]any{
		"success":  true,
		"count":    len(simplified),
		"messages": simplified,
	})
}

// listPins 列出置顶消息。
func (t *DiscordExtTool) listPins(ctx context.Context, args map[string]any) (string, error) {
	channelID := getString(args, "channel_id", "")
	if err := validateDiscordID("channel_id", channelID); err != nil {
		return "", err
	}

	resp, err := t.callAPI(ctx, "GET", "/channels/"+channelID+"/pins", nil)
	if err != nil {
		return "", err
	}

	// 从 resp 中提取数组
	var pins []any
	if rawList, ok := resp["raw"].([]any); ok {
		pins = rawList
	} else {
		pins = getListAnyFromMap(resp, "pins")
	}

	return jsonResult(map[string]any{
		"success": true,
		"count":   len(pins),
		"pins":    pins,
	})
}

// createThread 创建线程。
func (t *DiscordExtTool) createThread(ctx context.Context, args map[string]any) (string, error) {
	channelID := getString(args, "channel_id", "")
	name := getString(args, "name", "")
	messageID := getString(args, "message_id", "")

	if err := validateDiscordID("channel_id", channelID); err != nil {
		return "", err
	}
	if name == "" {
		return "", fmt.Errorf("name 参数是必填项")
	}
	if messageID != "" {
		if err := validateDiscordID("message_id", messageID); err != nil {
			return "", err
		}
	}

	var endpoint string
	var body map[string]any

	if messageID != "" {
		// 从消息创建线程
		endpoint = "/channels/" + channelID + "/messages/" + messageID + "/threads"
		body = map[string]any{"name": name}
	} else {
		// 创建新线程
		endpoint = "/channels/" + channelID + "/threads"
		body = map[string]any{
			"name": name,
			"type": 11, // public_thread
		}
	}

	resp, err := t.callAPI(ctx, "POST", endpoint, body)
	if err != nil {
		return "", err
	}

	return jsonResult(map[string]any{
		"success": true,
		"thread":  resp,
	})
}

// ───────────────────────────── API 调用 ─────────────────────────────

// callAPI 调用 Discord REST API。
func (t *DiscordExtTool) callAPI(ctx context.Context, method string, endpoint string, body map[string]any) (map[string]any, error) {
	apiURL := t.apiURL + endpoint

	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("序列化请求体失败: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bot "+t.token)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == 403 {
		return nil, fmt.Errorf("权限不足 (HTTP 403)")
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("API 错误 (HTTP %d)", resp.StatusCode)
	}

	// 尝试解析为 JSON
	if len(respBody) == 0 {
		return map[string]any{"success": true}, nil
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return map[string]any{"raw": string(respBody)}, nil
	}

	return result, nil
}
