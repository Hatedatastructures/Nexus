// Package platforms 提供消息平台的适配器接口和通用类型。
// 每个消息平台 (Telegram/Discord/Slack/微信/飞书等) 都通过 PlatformAdapter 接口接入网关。
package platforms

import (
	"context"
	"time"
)

// ───────────────────────────── 消息类型 ─────────────────────────────

// MessageType 表示平台消息的媒体类型
type MessageType string

const (
	MsgText     MessageType = "TEXT"     // 文本消息
	MsgPhoto    MessageType = "PHOTO"    // 图片消息
	MsgVoice    MessageType = "VOICE"    // 语音消息
	MsgVideo    MessageType = "VIDEO"    // 视频消息
	MsgDocument MessageType = "DOCUMENT" // 文件消息
	MsgSticker  MessageType = "STICKER"  // 贴纸消息
	MsgLocation MessageType = "LOCATION" // 位置消息
	MsgCommand  MessageType = "COMMAND"  // 斜杠命令
)

// ───────────────────────────── 平台枚举 ─────────────────────────────

// Platform 定义支持的平台类型
type Platform string

const (
	PlatformLocal       Platform = "local"       // 本地终端
	PlatformTelegram    Platform = "telegram"    // Telegram
	PlatformDiscord     Platform = "discord"     // Discord
	PlatformSlack       Platform = "slack"       // Slack
	PlatformWhatsApp    Platform = "whatsapp"    // WhatsApp
	PlatformWeChat      Platform = "wechat"      // 微信 (公众号/客服)
	PlatformFeishu      Platform = "feishu"      // 飞书
	PlatformDingTalk    Platform = "dingtalk"    // 钉钉
	PlatformSignal      Platform = "signal"      // Signal
	PlatformMatrix      Platform = "matrix"      // Matrix
	PlatformEmail       Platform = "email"       // 邮件
	PlatformSMS         Platform = "sms"         // 短信
	PlatformWebhook     Platform = "webhook"     // Webhook
	PlatformMattermost  Platform = "mattermost"  // Mattermost
	PlatformQQBot       Platform = "qqbot"       // QQ Bot
	PlatformAPIServer   Platform = "api_server"  // OpenAI 兼容 API 服务器
	PlatformWeCom       Platform = "wecom"       // 企业微信
	PlatformWeiXin      Platform = "weixin"      // 微信 (个人号)
	PlatformYuanbao     Platform = "yuanbao"     // 元宝
	PlatformBlueBubbles Platform = "bluebubbles" // BlueBubbles (iMessage)
)

// ───────────────────────────── 消息事件 ─────────────────────────────

// MessageEvent 表示来自平台的入站消息
type MessageEvent struct {
	Text         string         // 消息文本内容
	MessageType  MessageType    // 消息媒体类型
	Source       *SessionSource // 会话来源信息
	MessageID    string         // 平台消息 ID (用于回复/编辑)
	MediaURLs    []string       // 媒体文件 URL 列表
	ReplyToMsgID string         // 被回复的消息 ID
	ReplyToText  string         // 被回复的消息文本
	ThreadID     string         // 话题/讨论串 ID
	RawMessage   any            // 平台原始消息对象 (平台特定)
	Timestamp    time.Time      // 消息时间戳
	IsBot        bool           // 发送者是否为机器人
}

// SessionSource 标识消息的会话来源
type SessionSource struct {
	Platform Platform // 平台类型
	ChatID   string   // 聊天/频道 ID
	ChatName string   // 聊天/频道名称
	ChatType string   // 聊天类型: "dm" / "group" / "channel"
	UserID   string   // 用户 ID
	UserName string   // 用户名
	ThreadID string   // 话题 ID (可选)
}

// ───────────────────────────── 发送结果 ─────────────────────────────

// SendResult 表示消息发送的结果
type SendResult struct {
	Success   bool   // 是否成功
	MessageID string // 平台消息 ID
	Error     string // 错误消息 (成功时为空)
	Retryable bool   // 是否可重试 (临时网络错误等)
}

// SendOptions 定义消息发送的附加选项
type SendOptions struct {
	ReplyToMsgID string         // 要回复的消息 ID
	ParseMode    string         // 解析模式 (HTML / Markdown)
	Silent       bool           // 是否静默发送 (不通知用户)
	Metadata     map[string]any // 平台特定附加数据
}

// ───────────────────────────── 平台适配器接口 ─────────────────────────────

// PlatformAdapter 是消息平台适配器必须实现的接口。
// 每个平台 (Telegram, Discord, Slack, 微信等) 都有一个对应的适配器实现。
type PlatformAdapter interface {
	// Name 返回适配器的显示名称 (如 "Telegram", "Discord")。
	Name() string

	// PlatformType 返回平台类型枚举。
	PlatformType() Platform

	// Connect 建立与平台的连接，开始接收消息。
	// 返回一个消息通道，适配器会将入站消息发送到此通道。
	// 如果连接失败，返回错误。
	Connect(ctx context.Context) (<-chan *MessageEvent, error)

	// Disconnect 优雅断开与平台的连接。
	Disconnect(ctx context.Context) error

	// Send 发送文本消息到指定聊天。
	Send(ctx context.Context, chatID string, content string, opts *SendOptions) (*SendResult, error)

	// EditMessage 编辑已发送的消息。
	// 用于流式响应的渐进更新。
	// 如果平台不支持编辑 (如 SMS)，返回 SendResult{Success: false}。
	EditMessage(ctx context.Context, chatID string, messageID string, content string) (*SendResult, error)

	// DeleteMessage 删除已发送的消息。
	DeleteMessage(ctx context.Context, chatID string, messageID string) error

	// SendTyping 向聊天发送"正在输入..."指示器。
	// 如果平台不支持，静默忽略。
	SendTyping(ctx context.Context, chatID string) error

	// SendImage 发送图片消息。
	// imageURL 可以是 URL 或本地文件路径 (file:// 前缀)。
	SendImage(ctx context.Context, chatID string, imageURL string, caption string, opts *SendOptions) (*SendResult, error)

	// SendVoice 发送语音消息。
	// audioPath 是本地音频文件的路径。
	SendVoice(ctx context.Context, chatID string, audioPath string, opts *SendOptions) (*SendResult, error)

	// SendVideo 发送视频消息。
	SendVideo(ctx context.Context, chatID string, videoPath string, caption string, opts *SendOptions) (*SendResult, error)

	// SendDocument 发送文件消息。
	SendDocument(ctx context.Context, chatID string, filePath string, caption string, opts *SendOptions) (*SendResult, error)

	// MaxMessageLength 返回平台允许的单条消息最大字符数。
	MaxMessageLength() int

	// SupportsStreaming 返回平台是否支持消息编辑流式输出。
	// 不支持的平台将等待完整回复后一次性发送。
	SupportsStreaming() bool
}

// ───────────────────────────── 工具函数 ─────────────────────────────

// BuildSessionKey 根据会话来源构造会话键。
// 格式: "agent:main:{platform}:{chat_type}:{chat_id}"
// 这是网关中会话路由和缓存的唯一标识。
func BuildSessionKey(src *SessionSource) string {
	return "agent:main:" + string(src.Platform) + ":" + src.ChatType + ":" + src.ChatID
}
