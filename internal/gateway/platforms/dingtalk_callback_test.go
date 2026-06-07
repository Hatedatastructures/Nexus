package platforms

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// DingTalk adapter -- ReceiveCallback
// ---------------------------------------------------------------------------

func TestDingTalkReceiveCallbackNoSecret(t *testing.T) {
	t.Parallel()

	a := NewDingTalkAdapter("key", "secret")
	// callbackSecret is not set
	err := a.ReceiveCallback([]byte(`{"text":{"content":"hello"}}`))
	if err == nil {
		t.Fatal("expected error when callbackSecret is not configured")
	}
	if !strings.Contains(err.Error(), "callbackSecret") {
		t.Errorf("error = %q, should mention callbackSecret", err.Error())
	}
}

func TestDingTalkReceiveCallbackMissingSignatureParams(t *testing.T) {
	t.Parallel()

	a := NewDingTalkAdapter("key", "secret")
	a.callbackSecret = "cbsecret"

	err := a.ReceiveCallback([]byte(`{"text":{"content":"hello"}}`))
	if err == nil {
		t.Fatal("expected error when signature params are missing")
	}
	if !strings.Contains(err.Error(), "签名参数") {
		t.Errorf("error = %q, should mention missing signature params", err.Error())
	}
}

func TestDingTalkReceiveCallbackInvalidSignature(t *testing.T) {
	t.Parallel()

	a := NewDingTalkAdapter("key", "secret")
	a.callbackSecret = "cbsecret"

	payload, _ := json.Marshal(map[string]any{
		"timestamp": "1234567890",
		"sign":      "invalidsignature",
		"text":      map[string]string{"content": "hello"},
	})

	err := a.ReceiveCallback(payload)
	if err == nil {
		t.Fatal("expected error for invalid signature")
	}
	if !strings.Contains(err.Error(), "签名验证失败") {
		t.Errorf("error = %q, should mention signature verification failure", err.Error())
	}
}

func TestDingTalkReceiveCallbackValidSignature(t *testing.T) {
	t.Parallel()

	a := NewDingTalkAdapter("key", "secret")
	a.callbackSecret = "cbsecret"
	a.msgCh = make(chan *MessageEvent, 128)

	ts := "1234567890"
	h := hmac.New(sha256.New, []byte("cbsecret"))
	h.Write([]byte(ts + "\n" + "cbsecret"))
	sign := base64.StdEncoding.EncodeToString(h.Sum(nil))

	payload, _ := json.Marshal(map[string]any{
		"timestamp":        ts,
		"sign":             sign,
		"text":             map[string]string{"content": "hello"},
		"senderId":         "user123",
		"senderNick":       "TestUser",
		"conversationId":   "conv123",
		"conversationType": "1",
		"msgId":            "msg123",
	})

	err := a.ReceiveCallback(payload)
	if err != nil {
		t.Fatalf("ReceiveCallback() error = %v", err)
	}

	// Verify message was pushed to channel
	select {
	case event := <-a.msgCh:
		if event.Text != "hello" {
			t.Errorf("Text = %q, want %q", event.Text, "hello")
		}
		if event.Source.Platform != PlatformDingTalk {
			t.Errorf("Platform = %q, want %q", event.Source.Platform, PlatformDingTalk)
		}
		if event.Source.UserID != "user123" {
			t.Errorf("UserID = %q, want %q", event.Source.UserID, "user123")
		}
		if event.Source.UserName != "TestUser" {
			t.Errorf("UserName = %q, want %q", event.Source.UserName, "TestUser")
		}
		if event.Source.ChatID != "conv123" {
			t.Errorf("ChatID = %q, want %q", event.Source.ChatID, "conv123")
		}
		if event.Source.ChatType != "dm" {
			t.Errorf("ChatType = %q, want %q", event.Source.ChatType, "dm")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for message on channel")
	}
}

func TestDingTalkReceiveCallbackGroupMessage(t *testing.T) {
	t.Parallel()

	a := NewDingTalkAdapter("key", "secret")
	a.callbackSecret = "cbsecret"
	a.msgCh = make(chan *MessageEvent, 128)

	ts := "1234567890"
	h := hmac.New(sha256.New, []byte("cbsecret"))
	h.Write([]byte(ts + "\n" + "cbsecret"))
	sign := base64.StdEncoding.EncodeToString(h.Sum(nil))

	payload, _ := json.Marshal(map[string]any{
		"timestamp":        ts,
		"sign":             sign,
		"text":             map[string]string{"content": "group msg"},
		"conversationId":   "conv_group",
		"conversationType": "2",
		"msgId":            "msg_group",
	})

	_ = a.ReceiveCallback(payload)

	select {
	case event := <-a.msgCh:
		if event.Source.ChatType != "group" {
			t.Errorf("ChatType = %q, want %q", event.Source.ChatType, "group")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for message on channel")
	}
}

func TestDingTalkReceiveCallbackEmptyMessageIgnored(t *testing.T) {
	t.Parallel()

	a := NewDingTalkAdapter("key", "secret")
	a.callbackSecret = "cbsecret"
	a.msgCh = make(chan *MessageEvent, 128)

	ts := "1234567890"
	h := hmac.New(sha256.New, []byte("cbsecret"))
	h.Write([]byte(ts + "\n" + "cbsecret"))
	sign := base64.StdEncoding.EncodeToString(h.Sum(nil))

	payload, _ := json.Marshal(map[string]any{
		"timestamp": ts,
		"sign":      sign,
		// no text content
	})

	err := a.ReceiveCallback(payload)
	if err != nil {
		t.Fatalf("ReceiveCallback() error = %v", err)
	}

	// No message should be pushed
	select {
	case <-a.msgCh:
		t.Error("expected no message for empty text")
	case <-time.After(100 * time.Millisecond):
		// expected
	}
}

func TestDingTalkReceiveCallbackTextAsString(t *testing.T) {
	t.Parallel()

	a := NewDingTalkAdapter("key", "secret")
	a.callbackSecret = "cbsecret"
	a.msgCh = make(chan *MessageEvent, 128)

	ts := "1234567890"
	h := hmac.New(sha256.New, []byte("cbsecret"))
	h.Write([]byte(ts + "\n" + "cbsecret"))
	sign := base64.StdEncoding.EncodeToString(h.Sum(nil))

	payload, _ := json.Marshal(map[string]any{
		"timestamp": ts,
		"sign":      sign,
		"text":      "string text content",
	})

	_ = a.ReceiveCallback(payload)

	select {
	case event := <-a.msgCh:
		if event.Text != "string text content" {
			t.Errorf("Text = %q, want %q", event.Text, "string text content")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for message on channel")
	}
}

func TestDingTalkReceiveCallbackInvalidJSON(t *testing.T) {
	t.Parallel()

	a := NewDingTalkAdapter("key", "secret")
	err := a.ReceiveCallback([]byte("invalid json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestDingTalkReceiveCallbackEncryptedPayload(t *testing.T) {
	t.Parallel()

	a := NewDingTalkAdapter("key", "secret")
	a.callbackSecret = "cbsecret"

	ts := "1234567890"
	h := hmac.New(sha256.New, []byte("cbsecret"))
	h.Write([]byte(ts + "\n" + "cbsecret"))
	sign := base64.StdEncoding.EncodeToString(h.Sum(nil))

	payload, _ := json.Marshal(map[string]any{
		"timestamp": ts,
		"sign":      sign,
		"encrypt":   "someencrypteddata",
	})

	err := a.ReceiveCallback(payload)
	// Encrypted callbacks should not error (they just log a warning)
	if err != nil {
		t.Errorf("encrypted callback should not error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// DingTalk adapter -- calcSign
// ---------------------------------------------------------------------------

func TestDingTalkCalcSign(t *testing.T) {
	t.Parallel()

	a := NewDingTalkAdapter("key", "mysecret")
	timestamp := "1700000000000"

	sign := a.calcSign(timestamp)

	h := hmac.New(sha256.New, []byte("mysecret"))
	h.Write([]byte(timestamp + "\n" + "mysecret"))
	expected := base64.StdEncoding.EncodeToString(h.Sum(nil))

	if sign != expected {
		t.Errorf("calcSign() = %q, want %q", sign, expected)
	}
}

// ---------------------------------------------------------------------------
// DingTalk adapter -- escapeJSONDing
// ---------------------------------------------------------------------------

func TestEscapeJSONDing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"plain text", "hello", "hello"},
		{"double quote", `say "hi"`, `say \"hi\"`},
		{"backslash", `path\to\file`, `path\\to\\file`},
		{"newline", "line1\nline2", `line1\nline2`},
		{"tab", "col1\tcol2", `col1\tcol2`},
		{"carriage return", "text\rmore", `text\rmore`},
		{"backspace", "text\bmore", `text\bmore`},
		{"form feed", "text\fmore", `text\fmore`},
		{"control char", string(rune(0x01)), `\u0001`},
		{"empty string", "", ""},
		{"chinese", "你好世界", "你好世界"},
		{"mixed", `hello "world"` + "\n\t", `hello \"world\"\n\t`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := escapeJSONDing(tc.input)
			if got != tc.expected {
				t.Errorf("escapeJSONDing() = %q, want %q", got, tc.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// DingTalk adapter -- session key construction
// ---------------------------------------------------------------------------

func TestDingTalkSessionKeyConstruction(t *testing.T) {
	t.Parallel()

	src := &SessionSource{
		Platform: PlatformDingTalk,
		ChatType: "group",
		ChatID:   "conv123",
	}
	key := BuildSessionKey(src)
	expected := "agent:main:dingtalk:group:conv123"
	if key != expected {
		t.Errorf("BuildSessionKey() = %q, want %q", key, expected)
	}
}
