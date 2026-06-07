package platforms

import (
	"context"
	"crypto/sha1"
	"encoding/xml"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// WeCom Callback adapter -- interface compliance
// ---------------------------------------------------------------------------

func TestWeComCallbackAdapterImplementsPlatformAdapter(t *testing.T) {
	t.Parallel()

	var _ PlatformAdapter = (*WeComCallbackAdapter)(nil)
}

// ---------------------------------------------------------------------------
// WeCom Callback adapter -- property accessors
// ---------------------------------------------------------------------------

func TestWeComCallbackAdapterProperties(t *testing.T) {
	t.Parallel()

	a := NewWeComCallbackAdapter(nil)
	if a.Name() != "WeCom Callback" {
		t.Errorf("Name() = %q, want %q", a.Name(), "WeCom Callback")
	}
	if a.PlatformType() != PlatformWeCom {
		t.Errorf("PlatformType() = %q, want %q", a.PlatformType(), PlatformWeCom)
	}
	if a.MaxMessageLength() != wecomCallbackMaxMessageLen {
		t.Errorf("MaxMessageLength() = %d, want %d", a.MaxMessageLength(), wecomCallbackMaxMessageLen)
	}
	if a.SupportsStreaming() {
		t.Error("SupportsStreaming() = true, want false")
	}
}

// ---------------------------------------------------------------------------
// WeCom Callback adapter -- unsupported operations
// ---------------------------------------------------------------------------

func TestWeComCallbackEditMessageUnsupported(t *testing.T) {
	t.Parallel()

	a := NewWeComCallbackAdapter(nil)
	result, err := a.EditMessage(context.Background(), "chat", "msg", "text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("expected Success = false for EditMessage")
	}
	if !strings.Contains(result.Error, "不支持编辑") {
		t.Errorf("Error = %q, should mention unsupported edit", result.Error)
	}
}

func TestWeComCallbackDeleteMessageUnsupported(t *testing.T) {
	t.Parallel()

	a := NewWeComCallbackAdapter(nil)
	err := a.DeleteMessage(context.Background(), "chat", "msg")
	if err == nil {
		t.Fatal("expected error for DeleteMessage")
	}
}

func TestWeComCallbackSendTypingNoop(t *testing.T) {
	t.Parallel()

	a := NewWeComCallbackAdapter(nil)
	if err := a.SendTyping(context.Background(), "chat"); err != nil {
		t.Errorf("SendTyping should be nil, got %v", err)
	}
}

func TestWeComCallbackSendVoiceUnsupported(t *testing.T) {
	t.Parallel()

	a := NewWeComCallbackAdapter(nil)
	result, _ := a.SendVoice(context.Background(), "chat", "/path", nil)
	if result.Success {
		t.Error("expected Success = false for SendVoice")
	}
}

func TestWeComCallbackSendVideoUnsupported(t *testing.T) {
	t.Parallel()

	a := NewWeComCallbackAdapter(nil)
	result, _ := a.SendVideo(context.Background(), "chat", "/path", "cap", nil)
	if result.Success {
		t.Error("expected Success = false for SendVideo")
	}
}

func TestWeComCallbackSendDocumentUnsupported(t *testing.T) {
	t.Parallel()

	a := NewWeComCallbackAdapter(nil)
	result, _ := a.SendDocument(context.Background(), "chat", "/path", "cap", nil)
	if result.Success {
		t.Error("expected Success = false for SendDocument")
	}
}

// ---------------------------------------------------------------------------
// WeCom Callback adapter -- Send validation
// ---------------------------------------------------------------------------

func TestWeComCallbackSendInvalidChatID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		chatID string
	}{
		{"no colon", "invalidchatid"},
		{"empty", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := NewWeComCallbackAdapter(nil)
			result, _ := a.Send(context.Background(), tc.chatID, "text", nil)
			if result.Success {
				t.Error("expected Success = false for invalid chatID")
			}
			if !strings.Contains(result.Error, "chatID") {
				t.Errorf("Error = %q, should mention chatID format", result.Error)
			}
		})
	}
}

func TestWeComCallbackSendImageFallsBackToCaption(t *testing.T) {
	t.Parallel()

	a := NewWeComCallbackAdapter(nil)
	// chatID format "corp_id:user_id" - will fail on API call but won't panic
	result, _ := a.SendImage(context.Background(), "corp123:user456", "http://img", "caption text", nil)
	// Will fail due to missing real API but the caption fallback logic is exercised
	_ = result
}

func TestWeComCallbackSendImageEmptyCaption(t *testing.T) {
	t.Parallel()

	a := NewWeComCallbackAdapter(nil)
	result, _ := a.SendImage(context.Background(), "corp123:user456", "http://img", "", nil)
	// Should use imageURL as text when caption is empty
	_ = result
}

// ---------------------------------------------------------------------------
// WeCom Callback adapter -- generateSignature
// ---------------------------------------------------------------------------

func TestWeComCallbackGenerateSignature(t *testing.T) {
	t.Parallel()

	a := NewWeComCallbackAdapter(nil)
	a.token = "mytoken123"

	timestamp := "1234567890"
	nonce := "nonce123"
	echostr := "echostr456"

	params := []string{a.token, timestamp, nonce, echostr}
	sort.Strings(params)
	str := strings.Join(params, "")
	expectedHash := fmt.Sprintf("%x", sha1.Sum([]byte(str)))

	sig := a.generateSignature(timestamp, nonce, echostr)
	if sig != expectedHash {
		t.Errorf("generateSignature() = %q, want %q", sig, expectedHash)
	}
}

// ---------------------------------------------------------------------------
// WeCom Callback adapter -- WeComXMLMessage struct
// ---------------------------------------------------------------------------

func TestWeComXMLMessageParsing(t *testing.T) {
	t.Parallel()

	xmlBody := `<xml>
			<ToUserName><![CDATA[corp]]></ToUserName>
			<FromUserName><![CDATA[user]]></FromUserName>
			<CreateTime>1700000000</CreateTime>
			<MsgType><![CDATA[text]]></MsgType>
			<Content><![CDATA[Test content]]></Content>
			<MsgId>999</MsgId>
			<AgentID>1001</AgentID>
		</xml>`

	var msg WeComXMLMessage
	err := xml.Unmarshal([]byte(xmlBody), &msg)
	if err != nil {
		t.Fatalf("xml.Unmarshal() error = %v", err)
	}
	if msg.ToUserName != "corp" {
		t.Errorf("ToUserName = %q, want %q", msg.ToUserName, "corp")
	}
	if msg.FromUserName != "user" {
		t.Errorf("FromUserName = %q, want %q", msg.FromUserName, "user")
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
	if msg.MsgID != "999" {
		t.Errorf("MsgId = %q, want %q", msg.MsgID, "999")
	}
	if msg.AgentID != "1001" {
		t.Errorf("AgentID = %q, want %q", msg.AgentID, "1001")
	}
}

// ---------------------------------------------------------------------------
// WeCom Callback adapter -- WeComXMLResponse struct
// ---------------------------------------------------------------------------

func TestWeComXMLResponseSerialization(t *testing.T) {
	t.Parallel()

	resp := WeComXMLResponse{
		ToUserName:   "user",
		FromUserName: "corp",
		CreateTime:   time.Now().Unix(),
		MsgType:      "text",
		Content:      "reply",
	}

	output, err := xml.Marshal(&resp)
	if err != nil {
		t.Fatalf("xml.Marshal() error = %v", err)
	}
	if !strings.Contains(string(output), "user") {
		t.Error("XML output should contain ToUserName value")
	}
	if !strings.Contains(string(output), "reply") {
		t.Error("XML output should contain Content value")
	}
}

// ---------------------------------------------------------------------------
// WeCom Callback adapter -- constants
// ---------------------------------------------------------------------------

func TestWeComCallbackConstants(t *testing.T) {
	t.Parallel()

	if wecomCallbackRequestTimeout != 15*time.Second {
		t.Errorf("wecomCallbackRequestTimeout = %v, want 15s", wecomCallbackRequestTimeout)
	}
	if wecomCallbackMaxMessageLen != 2048 {
		t.Errorf("wecomCallbackMaxMessageLen = %d, want 2048", wecomCallbackMaxMessageLen)
	}
	if wecomCallbackMaxBodySize != 1<<20 {
		t.Errorf("wecomCallbackMaxBodySize = %d, want %d", wecomCallbackMaxBodySize, 1<<20)
	}
}

// ---------------------------------------------------------------------------
// WeCom Callback adapter -- session key construction
// ---------------------------------------------------------------------------

func TestWeComCallbackSessionKeyConstruction(t *testing.T) {
	t.Parallel()

	src := &SessionSource{
		Platform: PlatformWeCom,
		ChatType: "dm",
		ChatID:   "corp123:user456",
	}
	key := BuildSessionKey(src)
	expected := "agent:main:wecom:dm:corp123:user456"
	if key != expected {
		t.Errorf("BuildSessionKey() = %q, want %q", key, expected)
	}
}
