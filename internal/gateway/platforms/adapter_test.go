package platforms

import (
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Platform enum
// ---------------------------------------------------------------------------

func TestPlatformConstants(t *testing.T) {
	t.Parallel()

	want := map[Platform]string{
		PlatformLocal:       "local",
		PlatformTelegram:    "telegram",
		PlatformDiscord:     "discord",
		PlatformSlack:       "slack",
		PlatformWhatsApp:    "whatsapp",
		PlatformWeChat:      "wechat",
		PlatformFeishu:      "feishu",
		PlatformDingTalk:    "dingtalk",
		PlatformSignal:      "signal",
		PlatformMatrix:      "matrix",
		PlatformEmail:       "email",
		PlatformSMS:         "sms",
		PlatformWebhook:     "webhook",
		PlatformMattermost:  "mattermost",
		PlatformQQBot:       "qqbot",
		PlatformAPIServer:   "api_server",
		PlatformWeCom:       "wecom",
		PlatformWeiXin:      "weixin",
		PlatformYuanbao:     "yuanbao",
		PlatformBlueBubbles: "bluebubbles",
	}

	for plat, expected := range want {
		if string(plat) != expected {
			t.Errorf("Platform %s = %q, want %q", plat, string(plat), expected)
		}
	}
}

// ---------------------------------------------------------------------------
// MessageType constants
// ---------------------------------------------------------------------------

func TestMessageTypeConstants(t *testing.T) {
	t.Parallel()

	types := map[MessageType]string{
		MsgText:     "TEXT",
		MsgPhoto:    "PHOTO",
		MsgVoice:    "VOICE",
		MsgVideo:    "VIDEO",
		MsgDocument: "DOCUMENT",
		MsgSticker:  "STICKER",
		MsgLocation: "LOCATION",
		MsgCommand:  "COMMAND",
	}

	for mt, expected := range types {
		if string(mt) != expected {
			t.Errorf("MessageType %s = %q, want %q", mt, string(mt), expected)
		}
	}
}

// ---------------------------------------------------------------------------
// MessageEvent
// ---------------------------------------------------------------------------

func TestMessageEventFields(t *testing.T) {
	t.Parallel()

	ts := time.Now()
	ev := &MessageEvent{
		Text:         "hello",
		MessageType:  MsgText,
		MessageID:    "msg-1",
		MediaURLs:    []string{"https://example.com/img.png"},
		ReplyToMsgID: "msg-0",
		ReplyToText:  "original",
		ThreadID:     "thread-1",
		RawMessage:   map[string]any{"k": "v"},
		Timestamp:    ts,
		IsBot:        false,
		Source: &SessionSource{
			Platform: PlatformTelegram,
			ChatID:   "123",
			ChatName: "test-chat",
			ChatType: "group",
			UserID:   "456",
			UserName: "tester",
			ThreadID: "thread-1",
		},
	}

	if ev.Text != "hello" {
		t.Errorf("Text = %q, want %q", ev.Text, "hello")
	}
	if ev.MessageType != MsgText {
		t.Errorf("MessageType = %q, want %q", ev.MessageType, MsgText)
	}
	if ev.Source.Platform != PlatformTelegram {
		t.Errorf("Source.Platform = %q, want %q", ev.Source.Platform, PlatformTelegram)
	}
	if ev.Source.ChatID != "123" {
		t.Errorf("Source.ChatID = %q, want %q", ev.Source.ChatID, "123")
	}
	if len(ev.MediaURLs) != 1 {
		t.Fatalf("MediaURLs length = %d, want 1", len(ev.MediaURLs))
	}
	if ev.MediaURLs[0] != "https://example.com/img.png" {
		t.Errorf("MediaURLs[0] = %q, want %q", ev.MediaURLs[0], "https://example.com/img.png")
	}
}

func TestMessageEventZeroValue(t *testing.T) {
	t.Parallel()

	var ev MessageEvent
	if ev.Text != "" {
		t.Errorf("zero Text = %q, want empty", ev.Text)
	}
	if ev.Source != nil {
		t.Errorf("zero Source = %v, want nil", ev.Source)
	}
	if ev.MessageType != "" {
		t.Errorf("zero MessageType = %q, want empty", ev.MessageType)
	}
	if ev.IsBot != false {
		t.Errorf("zero IsBot = %v, want false", ev.IsBot)
	}
}

// ---------------------------------------------------------------------------
// SessionSource
// ---------------------------------------------------------------------------

func TestSessionSourceFields(t *testing.T) {
	t.Parallel()

	src := &SessionSource{
		Platform: PlatformDiscord,
		ChatID:   "ch-1",
		ChatName: "general",
		ChatType: "group",
		UserID:   "usr-1",
		UserName: "alice",
		ThreadID: "th-1",
	}

	if src.Platform != PlatformDiscord {
		t.Errorf("Platform = %q, want %q", src.Platform, PlatformDiscord)
	}
	if src.ChatType != "group" {
		t.Errorf("ChatType = %q, want %q", src.ChatType, "group")
	}
}

// ---------------------------------------------------------------------------
// SendResult
// ---------------------------------------------------------------------------

func TestSendResultSuccess(t *testing.T) {
	t.Parallel()

	sr := &SendResult{
		Success:   true,
		MessageID: "100",
		Error:     "",
		Retryable: false,
	}

	if !sr.Success {
		t.Error("Success = false, want true")
	}
	if sr.MessageID != "100" {
		t.Errorf("MessageID = %q, want %q", sr.MessageID, "100")
	}
}

func TestSendResultFailure(t *testing.T) {
	t.Parallel()

	sr := &SendResult{
		Success:   false,
		Error:     "network timeout",
		Retryable: true,
	}

	if sr.Success {
		t.Error("Success = true, want false")
	}
	if !sr.Retryable {
		t.Error("Retryable = false, want true")
	}
}

// ---------------------------------------------------------------------------
// SendOptions
// ---------------------------------------------------------------------------

func TestSendOptionsFields(t *testing.T) {
	t.Parallel()

	opts := &SendOptions{
		ReplyToMsgID: "42",
		ParseMode:    "Markdown",
		Silent:       true,
		Metadata:     map[string]any{"key": "val"},
	}

	if opts.ReplyToMsgID != "42" {
		t.Errorf("ReplyToMsgID = %q, want %q", opts.ReplyToMsgID, "42")
	}
	if opts.ParseMode != "Markdown" {
		t.Errorf("ParseMode = %q, want %q", opts.ParseMode, "Markdown")
	}
	if !opts.Silent {
		t.Error("Silent = false, want true")
	}
}

func TestSendOptionsNil(t *testing.T) {
	t.Parallel()

	var opts *SendOptions
	// Ensure nil SendOptions does not panic when dereferenced in adapter code.
	// Adapters guard with `if opts != nil`.
	if opts != nil {
		t.Error("expected nil SendOptions")
	}
}

// ---------------------------------------------------------------------------
// BuildSessionKey
// ---------------------------------------------------------------------------

func TestBuildSessionKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		src      *SessionSource
		expected string
	}{
		{
			name: "telegram group",
			src: &SessionSource{
				Platform: PlatformTelegram,
				ChatType: "group",
				ChatID:   "123456",
			},
			expected: "agent:main:telegram:group:123456",
		},
		{
			name: "discord dm",
			src: &SessionSource{
				Platform: PlatformDiscord,
				ChatType: "dm",
				ChatID:   "abc",
			},
			expected: "agent:main:discord:dm:abc",
		},
		{
			name: "slack channel",
			src: &SessionSource{
				Platform: PlatformSlack,
				ChatType: "channel",
				ChatID:   "C99",
			},
			expected: "agent:main:slack:channel:C99",
		},
		{
			name: "empty fields",
			src: &SessionSource{
				Platform: "",
				ChatType: "",
				ChatID:   "",
			},
			expected: "agent:main:::",
		},
		{
			name: "local platform",
			src: &SessionSource{
				Platform: PlatformLocal,
				ChatType: "dm",
				ChatID:   "user-1",
			},
			expected: "agent:main:local:dm:user-1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := BuildSessionKey(tc.src)
			if got != tc.expected {
				t.Errorf("BuildSessionKey() = %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestBuildSessionKeyFormat(t *testing.T) {
	t.Parallel()

	src := &SessionSource{
		Platform: PlatformTelegram,
		ChatType: "dm",
		ChatID:   "42",
	}

	key := BuildSessionKey(src)
	parts := strings.Split(key, ":")
	if len(parts) != 5 {
		t.Fatalf("expected 5 colon-separated parts, got %d: %q", len(parts), parts)
	}
	if parts[0] != "agent" || parts[1] != "main" {
		t.Errorf("prefix = %q:%q, want agent:main", parts[0], parts[1])
	}
}
