// Package tool 提供消息发送工具。
// 向指定的消息平台 (Telegram, Discord, Slack 等) 发送消息。
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

// ───────────────────────────── 消息发送工具 ─────────────────────────────

// SendMessageTool 实现向各消息平台发送消息。
// 支持 Telegram、Discord、Slack、飞书、钉钉、微信等平台。
// 通过各平台的 HTTP API 直接发送，无需依赖网关适配器实例。
type SendMessageTool struct {
	client *http.Client
}

// Name 返回工具名称。
func (t *SendMessageTool) Name() string { return "send_message" }

// Description 返回工具描述。
func (t *SendMessageTool) Description() string {
	return "向指定消息平台发送消息。支持 Telegram、Discord、Slack、飞书、钉钉、微信、WhatsApp 等平台。需要配置对应平台的 API Token/Key。"
}

// Toolset 返回工具所属工具集。
func (t *SendMessageTool) Toolset() string { return "messaging" }

// Emoji 返回工具图标。
func (t *SendMessageTool) Emoji() string { return "📨" }

// IsAvailable 检查消息发送是否可用。
// 只要配置了任意平台的 Token 即为可用。
func (t *SendMessageTool) IsAvailable() bool {
	return os.Getenv("TELEGRAM_BOT_TOKEN") != "" ||
		os.Getenv("DISCORD_BOT_TOKEN") != "" ||
		os.Getenv("SLACK_BOT_TOKEN") != "" ||
		os.Getenv("FEISHU_APP_ID") != "" ||
		os.Getenv("DINGTALK_ACCESS_TOKEN") != "" ||
		os.Getenv("WECHAT_APP_ID") != "" ||
		os.Getenv("WHATSAPP_ACCESS_TOKEN") != ""
}

// MaxResultChars 返回结果最大字符数。
func (t *SendMessageTool) MaxResultChars() int { return 5000 }

// Schema 返回工具的 JSON Schema。
func (t *SendMessageTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "send_message",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"platform": map[string]any{
					"type":        "string",
					"description": "目标平台: telegram, discord, slack, feishu, dingtalk, wechat, whatsapp, webhook",
				},
				"chat_id": map[string]any{
					"type":        "string",
					"description": "目标聊天/频道/群组 ID",
				},
				"message": map[string]any{
					"type":        "string",
					"description": "消息内容 (支持平台特定的格式，如 Telegram 的 MarkdownV2)",
				},
				"thread_id": map[string]any{
					"type":        "string",
					"description": "话题/讨论串 ID (可选，Discord thread、Slack thread 等)",
				},
				"parse_mode": map[string]any{
					"type":        "string",
					"description": "消息解析模式: html, markdown, plain，默认 plain",
				},
			},
			"required": []string{"platform", "chat_id", "message"},
		},
	}
}

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

	if t.client == nil {
		t.client = &http.Client{Timeout: 30 * time.Second}
	}

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
		slog.Error("消息发送失败", "platform", platform, "chat_id", chatID, "err", err)
		return ToolError(fmt.Sprintf("消息发送失败 (%s): %v", platform, err)), nil
	}

	slog.Info("消息发送成功", "platform", platform, "chat_id", chatID)
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
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Telegram API 错误 (HTTP %d): %s", resp.StatusCode, string(respBody))
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
		return nil, fmt.Errorf("Telegram API 错误: %s", apiResp.Description)
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

	url := "https://discord.com/api/v10/channels/" + chatID + "/messages"

	req, err := http.NewRequestWithContext(ctx, "POST", url, jsonReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bot "+token)

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Discord API 错误 (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var apiResp struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(respBody, &apiResp)

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

	req, err := http.NewRequestWithContext(ctx, "POST", "https://slack.com/api/chat.postMessage", jsonReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Slack API 错误 (HTTP %d): %s", resp.StatusCode, string(respBody))
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
		return nil, fmt.Errorf("Slack API 错误: %s", apiResp.Error)
	}

	return map[string]any{
		"output":     fmt.Sprintf("Slack 消息发送成功 (channel: %s)", chatID),
		"platform":   "slack",
		"chat_id":    chatID,
		"message_id": apiResp.TS,
	}, nil
}

// sendFeishu 发送飞书消息。
func (t *SendMessageTool) sendFeishu(ctx context.Context, chatID, message string) (map[string]any, error) {
	appID := os.Getenv("FEISHU_APP_ID")
	appSecret := os.Getenv("FEISHU_APP_SECRET")
	if appID == "" || appSecret == "" {
		return nil, fmt.Errorf("未配置 FEISHU_APP_ID 或 FEISHU_APP_SECRET")
	}

	// 获取 tenant_access_token
	token, err := t.getFeishuToken(ctx, appID, appSecret)
	if err != nil {
		return nil, fmt.Errorf("获取飞书 Token 失败: %w", err)
	}

	body := map[string]any{
		"receive_id": chatID,
		"msg_type":   "text",
		"content":    `{"text":"` + message + `"}`,
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://open.feishu.cn/open-apis/im/v1/messages?receive_id_type=chat_id",
		jsonReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("飞书 API 错误 (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	return map[string]any{
		"output":   fmt.Sprintf("飞书消息发送成功 (chat: %s)", chatID),
		"platform": "feishu",
		"chat_id":  chatID,
	}, nil
}

// getFeishuToken 获取飞书 tenant_access_token。
func (t *SendMessageTool) getFeishuToken(ctx context.Context, appID, appSecret string) (string, error) {
	body := map[string]any{
		"app_id":     appID,
		"app_secret": appSecret,
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal",
		jsonReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var tokenResp struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", err
	}
	if tokenResp.Code != 0 {
		return "", fmt.Errorf("飞书 Token 错误: %s", tokenResp.Msg)
	}

	return tokenResp.TenantAccessToken, nil
}

// sendDingTalk 发送钉钉消息 (通过自定义机器人 Webhook)。
func (t *SendMessageTool) sendDingTalk(ctx context.Context, chatID, message string) (map[string]any, error) {
	// 钉钉支持两种方式: 机器人 Webhook 或企业内部应用
	// 优先使用 DINGTALK_WEBHOOK，其次使用 DINGTALK_ACCESS_TOKEN
	webhook := os.Getenv("DINGTALK_WEBHOOK")
	if webhook != "" {
		return t.sendDingTalkWebhook(ctx, webhook, message)
	}

	accessToken := os.Getenv("DINGTALK_ACCESS_TOKEN")
	if accessToken == "" {
		return nil, fmt.Errorf("未配置 DINGTALK_WEBHOOK 或 DINGTALK_ACCESS_TOKEN")
	}

	// 企业内部应用方式
	body := map[string]any{
		"msgKey":  "sampleText",
		"msgParam": `{"content":"` + message + `"}`,
	}

	chatIDs := strings.Split(chatID, ",")
	for _, id := range chatIDs {
		body["openConversationId"] = strings.TrimSpace(id)
		body["robotCode"] = os.Getenv("DINGTALK_ROBOT_CODE")

		req, err := http.NewRequestWithContext(ctx, "POST",
			"https://oapi.dingtalk.com/robot/send?access_token="+accessToken,
			jsonReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := t.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("HTTP 请求失败: %w", err)
		}
		defer resp.Body.Close()

		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("钉钉 API 错误 (HTTP %d): %s", resp.StatusCode, string(respBody))
		}
	}

	return map[string]any{
		"output":   fmt.Sprintf("钉钉消息发送成功 (chat: %s)", chatID),
		"platform": "dingtalk",
		"chat_id":  chatID,
	}, nil
}

// sendDingTalkWebhook 通过自定义机器人 Webhook 发送钉钉消息。
func (t *SendMessageTool) sendDingTalkWebhook(ctx context.Context, webhook, message string) (map[string]any, error) {
	body := map[string]any{
		"msgtype": "text",
		"text": map[string]any{
			"content": message,
		},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", webhook, jsonReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("钉钉 Webhook 错误 (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var dingResp struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	_ = json.Unmarshal(respBody, &dingResp)
	if dingResp.ErrCode != 0 {
		return nil, fmt.Errorf("钉钉 Webhook 错误: %s", dingResp.ErrMsg)
	}

	return map[string]any{
		"output":   "钉钉消息发送成功",
		"platform": "dingtalk",
	}, nil
}

// sendWeChat 发送微信消息 (通过公众号客服接口)。
func (t *SendMessageTool) sendWeChat(ctx context.Context, chatID, message string) (map[string]any, error) {
	appID := os.Getenv("WECHAT_APP_ID")
	appSecret := os.Getenv("WECHAT_APP_SECRET")
	if appID == "" || appSecret == "" {
		return nil, fmt.Errorf("未配置 WECHAT_APP_ID 或 WECHAT_APP_SECRET")
	}

	// 获取 access_token
	token, err := t.getWeChatToken(ctx, appID, appSecret)
	if err != nil {
		return nil, fmt.Errorf("获取微信 Token 失败: %w", err)
	}

	body := map[string]any{
		"touser": chatID,
		"msgtype": "text",
		"text": map[string]any{
			"content": message,
		},
	}

	url := fmt.Sprintf("https://api.weixin.qq.com/cgi-bin/message/custom/send?access_token=%s", token)
	req, err := http.NewRequestWithContext(ctx, "POST", url, jsonReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("微信 API 错误 (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var wxResp struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	_ = json.Unmarshal(respBody, &wxResp)
	if wxResp.ErrCode != 0 {
		return nil, fmt.Errorf("微信 API 错误: %s", wxResp.ErrMsg)
	}

	return map[string]any{
		"output":   fmt.Sprintf("微信消息发送成功 (user: %s)", chatID),
		"platform": "wechat",
		"chat_id":  chatID,
	}, nil
}

// getWeChatToken 获取微信公众号 access_token。
func (t *SendMessageTool) getWeChatToken(ctx context.Context, appID, appSecret string) (string, error) {
	url := fmt.Sprintf("https://api.weixin.qq.com/cgi-bin/token?grant_type=client_credential&appid=%s&secret=%s", appID, appSecret)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", err
	}
	if tokenResp.ErrCode != 0 {
		return "", fmt.Errorf("微信 Token 错误: %s", tokenResp.ErrMsg)
	}

	return tokenResp.AccessToken, nil
}

// sendWhatsApp 发送 WhatsApp 消息 (通过 Cloud API)。
func (t *SendMessageTool) sendWhatsApp(ctx context.Context, chatID, message string) (map[string]any, error) {
	token := os.Getenv("WHATSAPP_ACCESS_TOKEN")
	phoneNumberID := os.Getenv("WHATSAPP_PHONE_NUMBER_ID")
	if token == "" || phoneNumberID == "" {
		return nil, fmt.Errorf("未配置 WHATSAPP_ACCESS_TOKEN 或 WHATSAPP_PHONE_NUMBER_ID")
	}

	body := map[string]any{
		"messaging_product": "whatsapp",
		"recipient_type":    "individual",
		"to":                chatID,
		"type":              "text",
		"text": map[string]any{
			"body": message,
		},
	}

	url := fmt.Sprintf("https://graph.facebook.com/v17.0/%s/messages", phoneNumberID)
	req, err := http.NewRequestWithContext(ctx, "POST", url, jsonReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("WhatsApp API 错误 (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	return map[string]any{
		"output":   fmt.Sprintf("WhatsApp 消息发送成功 (to: %s)", chatID),
		"platform": "whatsapp",
		"chat_id":  chatID,
	}, nil
}

// sendWebhook 发送通用 Webhook 消息 (POST JSON 到指定 URL)。
func (t *SendMessageTool) sendWebhook(ctx context.Context, webhookURL, message string) (map[string]any, error) {
	if !strings.HasPrefix(webhookURL, "http://") && !strings.HasPrefix(webhookURL, "https://") {
		return nil, fmt.Errorf("无效的 Webhook URL: %s", webhookURL)
	}

	body := map[string]any{
		"text":    message,
		"content": message,
	}

	req, err := http.NewRequestWithContext(ctx, "POST", webhookURL, jsonReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("Webhook 错误 (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	return map[string]any{
		"output":   fmt.Sprintf("Webhook 消息发送成功 (url: %s)", webhookURL),
		"platform": "webhook",
	}, nil
}

// jsonReader 将 map 序列化为 JSON 并返回 Reader。
func jsonReader(data map[string]any) *bytes.Reader {
	bytesData, _ := json.Marshal(data)
	return bytes.NewReader(bytesData)
}

// ───────────────────────────── init 注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&SendMessageTool{})
}
