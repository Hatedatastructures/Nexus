package platforms

import (
	"context"
	"encoding/xml"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// WeChat adapter -- ReceiveCallback
// ---------------------------------------------------------------------------

func TestWeChatReceiveCallbackTextMessage(t *testing.T) {
	t.Parallel()

	a := NewWeChatAdapter("app", "secret", "token")
	_, _ = a.Connect(context.Background())
	defer func() { _ = a.Disconnect(context.Background()) }()

	xmlBody := `<xml>
			<ToUserName><![CDATA[gh_123]]></ToUserName>
			<FromUserName><![CDATA[user_abc]]></FromUserName>
			<CreateTime>1700000000</CreateTime>
			<MsgType><![CDATA[text]]></MsgType>
			<Content><![CDATA[Hello World]]></Content>
			<MsgId>1234567890</MsgId>
		</xml>`

	event, replyXML, err := a.ReceiveCallback([]byte(xmlBody))
	if err != nil {
		t.Fatalf("ReceiveCallback() error = %v", err)
	}

	if event == nil {
		t.Fatal("expected non-nil event")
	}
	if event.Text != "Hello World" {
		t.Errorf("Text = %q, want %q", event.Text, "Hello World")
	}
	if event.MessageType != MsgText {
		t.Errorf("MessageType = %q, want %q", event.MessageType, MsgText)
	}
	if event.Source.Platform != PlatformWeChat {
		t.Errorf("Platform = %q, want %q", event.Source.Platform, PlatformWeChat)
	}
	if event.Source.ChatID != "user_abc" {
		t.Errorf("ChatID = %q, want %q", event.Source.ChatID, "user_abc")
	}
	if event.Source.UserID != "user_abc" {
		t.Errorf("UserID = %q, want %q", event.Source.UserID, "user_abc")
	}
	if event.MessageID != "1234567890" {
		t.Errorf("MessageID = %q, want %q", event.MessageID, "1234567890")
	}
	if replyXML == "" {
		t.Error("expected non-empty reply XML")
	}
}

func TestWeChatReceiveCallbackImageMessage(t *testing.T) {
	t.Parallel()

	a := NewWeChatAdapter("app", "secret", "token")
	_, _ = a.Connect(context.Background())
	defer func() { _ = a.Disconnect(context.Background()) }()

	xmlBody := `<xml>
			<ToUserName><![CDATA[gh_123]]></ToUserName>
			<FromUserName><![CDATA[user_abc]]></FromUserName>
			<CreateTime>1700000000</CreateTime>
			<MsgType><![CDATA[image]]></MsgType>
			<PicUrl><![CDATA[http://example.com/img.jpg]]></PicUrl>
			<MsgId>9999</MsgId>
		</xml>`

	event, _, err := a.ReceiveCallback([]byte(xmlBody))
	if err != nil {
		t.Fatalf("ReceiveCallback() error = %v", err)
	}
	if event.MessageType != MsgPhoto {
		t.Errorf("MessageType = %q, want %q", event.MessageType, MsgPhoto)
	}
	if len(event.MediaURLs) != 1 || event.MediaURLs[0] != "http://example.com/img.jpg" {
		t.Errorf("MediaURLs = %v, want [http://example.com/img.jpg]", event.MediaURLs)
	}
}

func TestWeChatReceiveCallbackVoiceMessage(t *testing.T) {
	t.Parallel()

	a := NewWeChatAdapter("app", "secret", "token")
	_, _ = a.Connect(context.Background())
	defer func() { _ = a.Disconnect(context.Background()) }()

	xmlBody := `<xml>
			<ToUserName><![CDATA[gh_123]]></ToUserName>
			<FromUserName><![CDATA[user_abc]]></FromUserName>
			<CreateTime>1700000000</CreateTime>
			<MsgType><![CDATA[voice]]></MsgType>
			<MsgId>8888</MsgId>
		</xml>`

	event, _, err := a.ReceiveCallback([]byte(xmlBody))
	if err != nil {
		t.Fatalf("ReceiveCallback() error = %v", err)
	}
	if event.MessageType != MsgVoice {
		t.Errorf("MessageType = %q, want %q", event.MessageType, MsgVoice)
	}
}

func TestWeChatReceiveCallbackLocationMessage(t *testing.T) {
	t.Parallel()

	a := NewWeChatAdapter("app", "secret", "token")
	_, _ = a.Connect(context.Background())
	defer func() { _ = a.Disconnect(context.Background()) }()

	xmlBody := `<xml>
			<ToUserName><![CDATA[gh_123]]></ToUserName>
			<FromUserName><![CDATA[user_abc]]></FromUserName>
			<CreateTime>1700000000</CreateTime>
			<MsgType><![CDATA[location]]></MsgType>
			<MsgId>7777</MsgId>
		</xml>`

	event, _, err := a.ReceiveCallback([]byte(xmlBody))
	if err != nil {
		t.Fatalf("ReceiveCallback() error = %v", err)
	}
	if event.MessageType != MsgLocation {
		t.Errorf("MessageType = %q, want %q", event.MessageType, MsgLocation)
	}
}

func TestWeChatReceiveCallbackInvalidXML(t *testing.T) {
	t.Parallel()

	a := NewWeChatAdapter("app", "secret", "token")
	_, _ = a.Connect(context.Background())
	defer func() { _ = a.Disconnect(context.Background()) }()

	_, _, err := a.ReceiveCallback([]byte("not valid xml <><>"))
	if err == nil {
		t.Fatal("expected error for invalid XML")
	}
}

func TestWeChatReceiveCallbackUnsupportedMessageType(t *testing.T) {
	t.Parallel()

	a := NewWeChatAdapter("app", "secret", "token")
	_, _ = a.Connect(context.Background())
	defer func() { _ = a.Disconnect(context.Background()) }()

	xmlBody := `<xml>
			<ToUserName><![CDATA[gh_123]]></ToUserName>
			<FromUserName><![CDATA[user_abc]]></FromUserName>
			<CreateTime>1700000000</CreateTime>
			<MsgType><![CDATA[link]]></MsgType>
			<MsgId>6666</MsgId>
		</xml>`

	event, _, err := a.ReceiveCallback([]byte(xmlBody))
	if err != nil {
		t.Fatalf("ReceiveCallback() error = %v", err)
	}
	if event.MessageType != MsgText {
		t.Errorf("MessageType = %q, want %q for unsupported type fallback", event.MessageType, MsgText)
	}
	if !strings.Contains(event.Text, "不支持的消息类型") {
		t.Errorf("Text = %q, should contain unsupported type indicator", event.Text)
	}
}

// ---------------------------------------------------------------------------
// WeChat adapter -- wechatXMLMessage struct
// ---------------------------------------------------------------------------

func TestWeChatXMLMessageParsing(t *testing.T) {
	t.Parallel()

	xmlBody := `<xml>
			<ToUserName><![CDATA[gh_123]]></ToUserName>
			<FromUserName><![CDATA[user_abc]]></FromUserName>
			<CreateTime>1700000000</CreateTime>
			<MsgType><![CDATA[text]]></MsgType>
			<Content><![CDATA[Test content]]></Content>
			<MsgId>111</MsgId>
			<PicUrl><![CDATA[http://example.com/pic.jpg]]></PicUrl>
			<MediaId><![CDATA[media_123]]></MediaId>
			<Format><![CDATA[amr]]></Format>
			<Recognition><![CDATA[recognized text]]></Recognition>
			<Location_X>39.9</Location_X>
			<Location_Y>116.3</Location_Y>
			<Scale>15</Scale>
			<Label><![CDATA[Beijing]]></Label>
		</xml>`

	var msg wechatXMLMessage
	err := xml.Unmarshal([]byte(xmlBody), &msg)
	if err != nil {
		t.Fatalf("xml.Unmarshal() error = %v", err)
	}

	if msg.ToUserName != "gh_123" {
		t.Errorf("ToUserName = %q, want %q", msg.ToUserName, "gh_123")
	}
	if msg.FromUserName != "user_abc" {
		t.Errorf("FromUserName = %q, want %q", msg.FromUserName, "user_abc")
	}
	if msg.CreateTime != 1700000000 {
		t.Errorf("CreateTime = %d, want 1700000000", msg.CreateTime)
	}
	if msg.MsgType != "text" {
		t.Errorf("MsgType = %q, want %q", msg.MsgType, "text")
	}
	if msg.Content != "Test content" {
		t.Errorf("Content = %q, want %q", msg.Content, "Test content")
	}
	if msg.MsgID != "111" {
		t.Errorf("MsgId = %q, want %q", msg.MsgID, "111")
	}
	if msg.PicURL != "http://example.com/pic.jpg" {
		t.Errorf("PicUrl = %q, want %q", msg.PicURL, "http://example.com/pic.jpg")
	}
	if msg.MediaID != "media_123" {
		t.Errorf("MediaId = %q, want %q", msg.MediaID, "media_123")
	}
	if msg.Recognition != "recognized text" {
		t.Errorf("Recognition = %q, want %q", msg.Recognition, "recognized text")
	}
}

// ---------------------------------------------------------------------------
// WeChat adapter -- session key construction via BuildSessionKey
// ---------------------------------------------------------------------------

func TestWeChatSessionKeyConstruction(t *testing.T) {
	t.Parallel()

	a := NewWeChatAdapter("app", "secret", "token")
	_, _ = a.Connect(context.Background())
	defer func() { _ = a.Disconnect(context.Background()) }()

	xmlBody := `<xml>
			<ToUserName><![CDATA[gh_123]]></ToUserName>
			<FromUserName><![CDATA[user_xyz]]></FromUserName>
			<CreateTime>1700000000</CreateTime>
			<MsgType><![CDATA[text]]></MsgType>
			<Content><![CDATA[hi]]></Content>
			<MsgId>555</MsgId>
		</xml>`

	event, _, _ := a.ReceiveCallback([]byte(xmlBody))

	key := BuildSessionKey(event.Source)
	expected := "agent:main:wechat:dm:user_xyz"
	if key != expected {
		t.Errorf("BuildSessionKey() = %q, want %q", key, expected)
	}
}

// ---------------------------------------------------------------------------
// WeChat adapter -- timestamp from XML
// ---------------------------------------------------------------------------

func TestWeChatReceiveCallbackTimestamp(t *testing.T) {
	t.Parallel()

	a := NewWeChatAdapter("app", "secret", "token")
	_, _ = a.Connect(context.Background())
	defer func() { _ = a.Disconnect(context.Background()) }()

	xmlBody := `<xml>
			<ToUserName><![CDATA[gh_123]]></ToUserName>
			<FromUserName><![CDATA[user_abc]]></FromUserName>
			<CreateTime>1700000000</CreateTime>
			<MsgType><![CDATA[text]]></MsgType>
			<Content><![CDATA[ts test]]></Content>
			<MsgId>444</MsgId>
		</xml>`

	event, _, _ := a.ReceiveCallback([]byte(xmlBody))
	expectedTime := time.Unix(1700000000, 0)
	if !event.Timestamp.Equal(expectedTime) {
		t.Errorf("Timestamp = %v, want %v", event.Timestamp, expectedTime)
	}
}
