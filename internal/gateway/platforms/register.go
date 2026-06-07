package platforms

import (
	"log/slog"
)

// RegisterAllAdapters 将所有内置平台适配器注册到指定注册中心。
// 替代分散在各文件中的 init() 自注册模式。
func RegisterAllAdapters(r *AdapterRegistry) {
	r.Register(&AdapterEntry{
		Platform: PlatformAPIServer,
		Name:     "API Server",
		Factory:  func() PlatformAdapter { return NewAPIServerAdapter(nil) },
	})
	r.Register(&AdapterEntry{
		Platform: PlatformDingTalk,
		Name:     "DingTalk",
		Factory:  func() PlatformAdapter { return NewDingTalkAdapter("", "") },
	})
	r.Register(&AdapterEntry{
		Platform: PlatformBlueBubbles,
		Name:     "BlueBubbles",
		Factory:  func() PlatformAdapter { return NewBlueBubblesAdapter() },
	})
	r.Register(&AdapterEntry{
		Platform: PlatformFeishu,
		Name:     "Feishu",
		Factory:  func() PlatformAdapter { return NewFeishuAdapter("", "") },
	})
	r.Register(&AdapterEntry{
		Platform: PlatformFeishu,
		Name:     "Feishu Comment",
		Factory:  func() PlatformAdapter { return NewFeishuCommentAdapter(nil) },
	})
	r.Register(&AdapterEntry{
		Platform: PlatformMatrix,
		Name:     "Matrix",
		Factory:  func() PlatformAdapter { return NewMatrixAdapter("", "", "") },
	})
	r.Register(&AdapterEntry{
		Platform: PlatformDiscord,
		Name:     "Discord",
		Factory:  func() PlatformAdapter { return NewDiscordAdapter("") },
	})
	r.Register(&AdapterEntry{
		Platform: PlatformMattermost,
		Name:     "Mattermost",
		Factory:  func() PlatformAdapter { return NewMattermostAdapter(nil) },
	})
	r.Register(&AdapterEntry{
		Platform: PlatformQQBot,
		Name:     "QQBot",
		Factory:  func() PlatformAdapter { return NewQQBotAdapter(nil) },
	})
	r.Register(&AdapterEntry{
		Platform: PlatformSignal,
		Name:     "Signal",
		Factory:  func() PlatformAdapter { return NewSignalAdapter(nil) },
	})
	r.Register(&AdapterEntry{
		Platform: PlatformSlack,
		Name:     "Slack",
		Factory:  func() PlatformAdapter { return &SlackAdapter{} },
	})
	r.Register(&AdapterEntry{
		Platform: PlatformSMS,
		Name:     "SMS",
		Factory:  func() PlatformAdapter { return NewSMSAdapter(nil) },
	})
	r.Register(&AdapterEntry{
		Platform: PlatformTelegram,
		Name:     "Telegram",
		Factory:  func() PlatformAdapter { return &TelegramAdapter{} },
	})
	r.Register(&AdapterEntry{
		Platform: PlatformWebhook,
		Name:     "Webhook",
		Factory:  func() PlatformAdapter { return NewWebhookAdapter(nil) },
	})
	r.Register(&AdapterEntry{
		Platform: PlatformWeChat,
		Name:     "WeChat",
		Factory:  func() PlatformAdapter { return NewWeChatAdapter("", "", "") },
	})
	r.Register(&AdapterEntry{
		Platform: PlatformWeCom,
		Name:     "WeCom Callback",
		Factory:  func() PlatformAdapter { return NewWeComCallbackAdapter(nil) },
	})
	r.Register(&AdapterEntry{
		Platform: PlatformWeiXin,
		Name:     "Weixin",
		Factory:  func() PlatformAdapter { return NewWeixinAdapter("", "") },
	})
	r.Register(&AdapterEntry{
		Platform: PlatformWhatsApp,
		Name:     "WhatsApp",
		Factory:  func() PlatformAdapter { return NewWhatsAppAdapter("", "") },
	})
	r.Register(&AdapterEntry{
		Platform: PlatformYuanbao,
		Name:     "Yuanbao",
		Factory:  func() PlatformAdapter { return NewYuanbaoAdapter("", "") },
	})

	slog.Debug("all platform adapters registered")
}
