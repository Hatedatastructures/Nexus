package platforms

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// WeCom Callback adapter -- handleCallback (GET verification)
// ---------------------------------------------------------------------------

func TestWeComCallbackHandleVerificationSuccess(t *testing.T) {
	t.Parallel()

	a := NewWeComCallbackAdapter(nil)
	a.token = "mytoken"
	a.msgCh = make(chan *MessageEvent, 100)

	timestamp := "1234567890"
	nonce := "nonce123"
	echostr := "hello_verify"

	sig := a.generateSignature(timestamp, nonce, echostr)

	req := httptest.NewRequest("GET", fmt.Sprintf(
		"/wecom/callback?msg_signature=%s&timestamp=%s&nonce=%s&echostr=%s",
		sig, timestamp, nonce, echostr), nil)
	w := httptest.NewRecorder()

	a.handleCallback(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if w.Body.String() != echostr {
		t.Errorf("body = %q, want %q", w.Body.String(), echostr)
	}
}

func TestWeComCallbackHandleVerificationInvalidSignature(t *testing.T) {
	t.Parallel()

	a := NewWeComCallbackAdapter(nil)
	a.token = "mytoken"
	a.msgCh = make(chan *MessageEvent, 100)

	req := httptest.NewRequest("GET",
		"/wecom/callback?msg_signature=invalidsig&timestamp=123&nonce=abc&echostr=echo", nil)
	w := httptest.NewRecorder()

	a.handleCallback(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

// ---------------------------------------------------------------------------
// WeCom Callback adapter -- handleCallback (POST message)
// ---------------------------------------------------------------------------

func TestWeComCallbackHandleMessageText(t *testing.T) {
	t.Parallel()

	a := NewWeComCallbackAdapter(nil)
	a.token = "mytoken"
	a.corpID = "corp123"
	a.msgCh = make(chan *MessageEvent, 100)

	xmlBody := `<xml>
			<ToUserName><![CDATA[corp123]]></ToUserName>
			<FromUserName><![CDATA[user456]]></FromUserName>
			<CreateTime>1700000000</CreateTime>
			<MsgType><![CDATA[text]]></MsgType>
			<Content><![CDATA[Hello WeCom]]></Content>
			<MsgId>789</MsgId>
			<AgentID>100</AgentID>
		</xml>`

	timestamp := "1234567890"
	nonce := "nonce123"
	sig := a.generateSignature(timestamp, nonce, xmlBody)

	req := httptest.NewRequest("POST", fmt.Sprintf(
		"/wecom/callback?msg_signature=%s&timestamp=%s&nonce=%s",
		sig, timestamp, nonce), strings.NewReader(xmlBody))
	w := httptest.NewRecorder()

	a.handleCallback(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	select {
	case event := <-a.msgCh:
		if event.Text != "Hello WeCom" {
			t.Errorf("Text = %q, want %q", event.Text, "Hello WeCom")
		}
		if event.MessageType != MsgText {
			t.Errorf("MessageType = %q, want %q", event.MessageType, MsgText)
		}
		if event.MessageID != "789" {
			t.Errorf("MessageID = %q, want %q", event.MessageID, "789")
		}
		if event.Source.Platform != PlatformWeCom {
			t.Errorf("Platform = %q, want %q", event.Source.Platform, PlatformWeCom)
		}
		if event.Source.ChatID != "corp123:user456" {
			t.Errorf("ChatID = %q, want %q", event.Source.ChatID, "corp123:user456")
		}
		if event.Source.UserID != "user456" {
			t.Errorf("UserID = %q, want %q", event.Source.UserID, "user456")
		}
		if event.Source.ChatType != "dm" {
			t.Errorf("ChatType = %q, want %q", event.Source.ChatType, "dm")
		}
	default:
		t.Fatal("expected message on channel")
	}
}

func TestWeComCallbackHandleMessageMissingSignature(t *testing.T) {
	t.Parallel()

	a := NewWeComCallbackAdapter(nil)
	a.token = "mytoken"
	a.msgCh = make(chan *MessageEvent, 100)

	req := httptest.NewRequest("POST", "/wecom/callback", strings.NewReader("<xml></xml>"))
	w := httptest.NewRecorder()

	a.handleCallback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestWeComCallbackHandleMessageInvalidSignature(t *testing.T) {
	t.Parallel()

	a := NewWeComCallbackAdapter(nil)
	a.token = "mytoken"
	a.msgCh = make(chan *MessageEvent, 100)

	req := httptest.NewRequest("POST",
		"/wecom/callback?msg_signature=badsig&timestamp=123&nonce=abc",
		strings.NewReader("<xml></xml>"))
	w := httptest.NewRecorder()

	a.handleCallback(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestWeComCallbackHandleMessageNonTextIgnored(t *testing.T) {
	t.Parallel()

	a := NewWeComCallbackAdapter(nil)
	a.token = "mytoken"
	a.corpID = "corp123"
	a.msgCh = make(chan *MessageEvent, 100)

	xmlBody := `<xml>
			<ToUserName><![CDATA[corp123]]></ToUserName>
			<FromUserName><![CDATA[user456]]></FromUserName>
			<CreateTime>1700000000</CreateTime>
			<MsgType><![CDATA[image]]></MsgType>
			<MsgId>888</MsgId>
		</xml>`

	timestamp := "1234567890"
	nonce := "nonce123"
	sig := a.generateSignature(timestamp, nonce, xmlBody)

	req := httptest.NewRequest("POST", fmt.Sprintf(
		"/wecom/callback?msg_signature=%s&timestamp=%s&nonce=%s",
		sig, timestamp, nonce), strings.NewReader(xmlBody))
	w := httptest.NewRecorder()

	a.handleCallback(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	// Non-text messages should not be pushed to channel
	select {
	case <-a.msgCh:
		t.Error("should not receive non-text messages")
	default:
		// expected
	}
}

func TestWeComCallbackHandleCallbackRejectsOtherMethods(t *testing.T) {
	t.Parallel()

	a := NewWeComCallbackAdapter(nil)
	a.msgCh = make(chan *MessageEvent, 100)

	req := httptest.NewRequest("PUT", "/wecom/callback", nil)
	w := httptest.NewRecorder()

	a.handleCallback(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}
