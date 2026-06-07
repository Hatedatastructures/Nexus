// Package tool 提供消息发送工具。
// 向指定的消息平台 (Telegram, Discord, Slack 等) 发送消息。
// 本文件包含 SendMessageTool 的类型定义、Schema 及注册；执行逻辑见 send_message_exec.go。
package tool

import (
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
)

// ───────────────────────────── 消息发送工具 ─────────────────────────────

// SendMessageTool 实现向各消息平台发送消息。
// 支持 Telegram、Discord、Slack、飞书、钉钉、微信等平台。
// 通过各平台的 HTTP API 直接发送，无需依赖网关适配器实例。
type SendMessageTool struct {
	client     *http.Client
	clientOnce sync.Once
}

// Name 返回工具名称。
func (t *SendMessageTool) Name() string { return "send_message" }

// allowedURLSchemes 是消息内容中 Markdown 链接允许的 URL scheme。
var allowedURLSchemes = map[string]bool{
	"http":   true,
	"https":  true,
	"mailto": true,
	"tel":    true,
}

// msgLinkRe 匹配 Markdown 链接格式 [text](url)。
var msgLinkRe = regexp.MustCompile(`\[([^\]]*)\]\(([^)]+)\)`)

// sanitizeMessageLinks 清理消息内容中的 Markdown 链接，仅允许安全的 URL scheme。
func sanitizeMessageLinks(content string) string {
	return msgLinkRe.ReplaceAllStringFunc(content, func(match string) string {
		sub := msgLinkRe.FindStringSubmatch(match)
		if len(sub) < 3 {
			return match
		}
		url := strings.TrimSpace(sub[2])
		// 提取 scheme
		schemeEnd := strings.Index(url, ":")
		if schemeEnd < 0 {
			return match // 无 scheme，保留
		}
		scheme := strings.ToLower(url[:schemeEnd])
		if allowedURLSchemes[scheme] {
			return match
		}
		// 不安全的 scheme，移除链接但保留文本
		return sub[1]
	})
}

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

