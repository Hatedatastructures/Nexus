// Package tool 提供消息发送工具。
// 本文件包含 SendMessageTool 的执行逻辑 (Execute 方法及主流平台发送实现)。
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
	"strings"
	"time"
)

// Execute 执行消息发送。
// 根据平台类型选择对应的 API → 发送消息 → 返回结果。
func (t *SendMessageTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	platform, ok := args["platform"].(string)
	if !ok || platform == "" {
		return ToolError("参数 platform 是必填项且必须为字符串"), nil
	}

	chatID, ok := args["chat_id"].(string)
	if !ok || chatID == "" {
		return ToolError("参数 chat_id 是必填项且必须为字符串"), nil
	}

	message, ok := args["message"].(string)
	if !ok || message == "" {
		return ToolError("参数 message 是必填项且必须为字符串"), nil
	}

	threadID, _ := args["thread_id"].(string)
	parseMode, _ := args["parse_mode"].(string)

	// 清理消息内容中的不安全 Markdown 链接
	message = sanitizeMessageLinks(message)

	t.clientOnce.Do(func() {
		t.client = &http.Client{Timeout: 30 * time.Second}
	})

	var result map[string]any
	var err error

	switch strings.ToLower(platform) {
	case "telegram":
		result, err = t.sendTelegram(ctx, chatID, message, parseMode)
	case "discord":
		result, err = t.sendDiscord(ctx, chatID, message, threadID)
	case "slack":
		result, err = t.sendSlack(ctx, chatID, message, threadID)
	case "feishu":
		result, err = t.sendFeishu(ctx, chatID, message)
	case "dingtalk":
		result, err = t.sendDingTalk(ctx, chatID, message)
	case "wechat":
		result, err = t.sendWeChat(ctx, chatID, message)
	case "whatsapp":
		result, err = t.sendWhatsApp(ctx, chatID, message)
	case "webhook":
		result, err = t.sendWebhook(ctx, chatID, message)
	default:
		return ToolError(fmt.Sprintf("不支持的平台: %s。支持的平台: telegram, discord, slack, feishu, dingtalk, wechat, whatsapp, webhook", platform)), nil
	}

	if err != nil {
		slog.Error("message send failed", "platform", platform, "chat_id", chatID, "err", err)
		return ToolError(fmt.Sprintf("消息发送失败 (%s): %v", platform, err)), nil
	}

	slog.Info("message sent successfully", "platform", platform, "chat_id", chatID)
	return ToolResult(result), nil
}

// sendTelegram 发送 Telegram 消息。
func (t *SendMessageTool) sendTelegram(ctx context.Context, chatID, message, parseMode string) (map[string]any, error) {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("未配置 TELEGRAM_BOT_TOKEN")
	}

	body := map[string]any{
		"chat_id": chatID,
		"text":    message,
	}

	// 映射解析模式
	switch parseMode {
	case "html", "markdown":
		body["parse_mode"] = parseMode
	case "markdownv2":
		body["parse_mode"] = "MarkdownV2"
	}

	resp, err := t.doTelegramPost(ctx, token, "/sendMessage", body)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"output":     fmt.Sprintf("Telegram 消息发送成功 (chat: %s)", chatID),
		"platform":   "telegram",
		"chat_id":    chatID,
		"message_id": resp.messageID,
	}, nil
}

// doTelegramPost 向 Telegram Bot API 发送 POST 请求。
func (t *SendMessageTool) doTelegramPost(ctx context.Context, token, method string, body map[string]any) (*telegramSendResp, error) {
	bodyBytes, _ := json.Marshal(body)
	url := fmt.Sprintf("https://api.telegram.org/bot%s%s", token, method)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("telegram API 错误 (HTTP %d)", resp.StatusCode)
	}

	var apiResp struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Result      struct {
			MessageID int `json:"message_id"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}
	if !apiResp.OK {
		return nil, fmt.Errorf("telegram API 错误: %s", apiResp.Description)
	}

	return &telegramSendResp{messageID: apiResp.Result.MessageID}, nil
}

type telegramSendResp struct {
	messageID int
}

// sendDiscord 发送 Discord 消息。
func (t *SendMessageTool) sendDiscord(ctx context.Context, chatID, message, threadID string) (map[string]any, error) {
	token := os.Getenv("DISCORD_BOT_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("未配置 DISCORD_BOT_TOKEN")
	}

	body := map[string]any{
		"content": message,
	}
	if threadID != "" {
		body["channel_id"] = threadID
	}

	// 验证 chatID 格式 (Discord 频道 ID 仅含数字)
	for _, c := range chatID {
		if c < '0' || c > '9' {
			return nil, fmt.Errorf("无效的 Discord 频道 ID")
		}
	}

	url := "https://discord.com/api/v10/channels/" + chatID + "/messages"

	req, err := http.NewRequestWithContext(ctx, "POST", url, mustJSONReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bot "+token)

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("discord API 错误 (HTTP %d)", resp.StatusCode)
	}

	var apiResp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		slog.Warn("send_message: failed to parse discord response", "error", err)
	}

	return map[string]any{
		"output":     fmt.Sprintf("Discord 消息发送成功 (channel: %s)", chatID),
		"platform":   "discord",
		"chat_id":    chatID,
		"message_id": apiResp.ID,
	}, nil
}

// sendSlack 发送 Slack 消息。
func (t *SendMessageTool) sendSlack(ctx context.Context, chatID, message, threadID string) (map[string]any, error) {
	token := os.Getenv("SLACK_BOT_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("未配置 SLACK_BOT_TOKEN")
	}

	body := map[string]any{
		"channel": chatID,
		"text":    message,
	}
	if threadID != "" {
		body["thread_ts"] = threadID
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://slack.com/api/chat.postMessage", mustJSONReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("slack API 错误 (HTTP %d)", resp.StatusCode)
	}

	var apiResp struct {
		OK      bool   `json:"ok"`
		Error   string `json:"error"`
		TS      string `json:"ts"`
		Message struct {
			Text string `json:"text"`
		} `json:"message"`
	}
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}
	if !apiResp.OK {
		return nil, fmt.Errorf("slack API 错误: %s", apiResp.Error)
	}

	return map[string]any{
		"output":     fmt.Sprintf("Slack 消息发送成功 (channel: %s)", chatID),
		"platform":   "slack",
		"chat_id":    chatID,
		"message_id": apiResp.TS,
	}, nil
}

// jsonReader 将 map 序列化为 JSON 并返回 Reader。
func jsonReader(data map[string]any) (*bytes.Reader, error) {
	bytesData, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("JSON 序列化失败: %w", err)
	}
	return bytes.NewReader(bytesData), nil
}

// mustJSONReader 序列化 map 为 JSON Reader，出错时 panic (用于已知合法数据)。
func mustJSONReader(data map[string]any) *bytes.Reader {
	r, err := jsonReader(data)
	if err != nil {
		panic(fmt.Sprintf("jsonReader: %v", err))
	}
	return r
}
