// Package tool 提供消息发送工具。
// 本文件包含飞书、钉钉、微信、WhatsApp、Webhook 等平台的消息发送实现。
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

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

	contentBytes, _ := json.Marshal(map[string]string{"text": message})
	body := map[string]any{
		"receive_id": chatID,
		"msg_type":   "text",
		"content":    string(contentBytes),
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://open.feishu.cn/open-apis/im/v1/messages?receive_id_type=chat_id",
		mustJSONReader(body))
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

	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8192))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("飞书 API 错误 (HTTP %d)", resp.StatusCode)
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
		mustJSONReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	var tokenResp struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&tokenResp); err != nil {
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
	msgParamBytes, _ := json.Marshal(map[string]string{"content": message})
	body := map[string]any{
		"msgKey":   "sampleText",
		"msgParam": string(msgParamBytes),
	}

	chatIDs := strings.Split(chatID, ",")
	for _, id := range chatIDs {
		body["openConversationId"] = strings.TrimSpace(id)
		body["robotCode"] = os.Getenv("DINGTALK_ROBOT_CODE")

		req, err := http.NewRequestWithContext(ctx, "POST",
			"https://oapi.dingtalk.com/robot/send?access_token="+accessToken,
			mustJSONReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := t.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("HTTP 请求失败: %w", err)
		}
		func() {
			defer func() { _ = resp.Body.Close() }()
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		}()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("钉钉 API 错误 (HTTP %d)", resp.StatusCode)
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
	if safe, reason := CheckURLSafety(webhook); !safe {
		return nil, fmt.Errorf("钉钉 Webhook URL 不安全: %s", reason)
	}

	body := map[string]any{
		"msgtype": "text",
		"text": map[string]any{
			"content": message,
		},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", webhook, mustJSONReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("钉钉 Webhook 错误 (HTTP %d)", resp.StatusCode)
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

	// 获取 access-token
	token, err := t.getWeChatToken(ctx, appID, appSecret)
	if err != nil {
		return nil, fmt.Errorf("获取微信 Token 失败: %w", err)
	}

	body := map[string]any{
		"touser":  chatID,
		"msgtype": "text",
		"text": map[string]any{
			"content": message,
		},
	}

	url := fmt.Sprintf("https://api.weixin.qq.com/cgi-bin/message/custom/send?access_token=%s", token)
	req, err := http.NewRequestWithContext(ctx, "POST", url, mustJSONReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("微信 API 错误 (HTTP %d)", resp.StatusCode)
	}

	var wxResp struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(respBody, &wxResp); err != nil {
		return nil, fmt.Errorf("解析微信响应失败: %w", err)
	}
	if wxResp.ErrCode != 0 {
		return nil, fmt.Errorf("微信 API 错误: %s", wxResp.ErrMsg)
	}

	return map[string]any{
		"output":   fmt.Sprintf("微信消息发送成功 (user: %s)", chatID),
		"platform": "wechat",
		"chat_id":  chatID,
	}, nil
}

// getWeChatToken 获取微信公众号 access-token。
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
	defer func() { _ = resp.Body.Close() }()

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&tokenResp); err != nil {
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
	req, err := http.NewRequestWithContext(ctx, "POST", url, mustJSONReader(body))
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

	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("WhatsApp API 错误 (HTTP %d)", resp.StatusCode)
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

	if safe, reason := CheckURLSafety(webhookURL); !safe {
		return nil, fmt.Errorf("webhook URL 不安全: %s", reason)
	}

	body := map[string]any{
		"text":    message,
		"content": message,
	}

	req, err := http.NewRequestWithContext(ctx, "POST", webhookURL, mustJSONReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("webhook 错误 (HTTP %d)", resp.StatusCode)
	}

	return map[string]any{
		"output":   fmt.Sprintf("Webhook 消息发送成功 (url: %s)", webhookURL),
		"platform": "webhook",
	}, nil
}
