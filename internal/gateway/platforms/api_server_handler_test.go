package platforms

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// API Server adapter -- handleChatCompletions
// ---------------------------------------------------------------------------

func TestAPIServerHandleChatCompletionsRejectsNonPost(t *testing.T) {
	t.Parallel()

	a := NewAPIServerAdapter(nil)
	req := httptest.NewRequest("GET", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()
	a.handleChatCompletions(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestAPIServerHandleChatCompletionsInvalidJSON(t *testing.T) {
	t.Parallel()

	a := NewAPIServerAdapter(nil)
	a.msgCh = make(chan *MessageEvent, 100)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader("not json"))
	w := httptest.NewRecorder()
	a.handleChatCompletions(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestAPIServerHandleChatCompletionsNoUserMessage(t *testing.T) {
	t.Parallel()

	a := NewAPIServerAdapter(nil)
	a.msgCh = make(chan *MessageEvent, 100)

	body := `{"model":"gpt-4","messages":[{"role":"assistant","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()
	a.handleChatCompletions(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestAPIServerHandleChatCompletionsSuccessful(t *testing.T) {
	t.Parallel()

	a := NewAPIServerAdapter(nil)
	a.msgCh = make(chan *MessageEvent, 100)

	go func() {
		// Wait for the message event and respond
		event := <-a.msgCh
		// Find the pending response and deliver
		a.responseMu.Lock()
		if ch, ok := a.pendingResponses[event.MessageID]; ok {
			ch <- "Hello from AI"
			delete(a.pendingResponses, event.MessageID)
		}
		a.responseMu.Unlock()
	}()

	body := `{"model":"gpt-4","messages":[{"role":"user","content":"Hello"}],"stream":false}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	w := httptest.NewRecorder()
	a.handleChatCompletions(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response JSON parse error: %v", err)
	}
	if resp["object"] != "chat.completion" {
		t.Errorf("object = %v, want chat.completion", resp["object"])
	}
}

// ---------------------------------------------------------------------------
// API Server adapter -- isLoopbackHost
// ---------------------------------------------------------------------------

func TestIsLoopbackHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		host string
		want bool
	}{
		{"localhost", true},
		{"127.0.0.1", true},
		{"::1", true},
		{"[::1]", true},
		{"192.168.1.1", false},
		{"example.com", false},
		{"8.8.8.8", false},
		{"0.0.0.0", false},
	}

	for _, tc := range tests {
		t.Run(tc.host, func(t *testing.T) {
			t.Parallel()
			got := isLoopbackHost(tc.host)
			if got != tc.want {
				t.Errorf("isLoopbackHost(%q) = %v, want %v", tc.host, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// API Server adapter -- splitContent
// ---------------------------------------------------------------------------

func TestSplitContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		content   string
		chunkSize int
		expected  []string
	}{
		{"empty", "", 5, nil},
		{"short", "hi", 5, []string{"hi"}},
		{"exact", "hello", 5, []string{"hello"}},
		{"split", "hello world", 5, []string{"hello", " worl", "d"}},
		{"unicode", "你好世界", 2, []string{"你好", "世界"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := splitContent(tc.content, tc.chunkSize)
			if len(got) != len(tc.expected) {
				t.Fatalf("splitContent() = %v, want %v", got, tc.expected)
			}
			for i, s := range got {
				if s != tc.expected[i] {
					t.Errorf("chunk[%d] = %q, want %q", i, s, tc.expected[i])
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// API Server adapter -- validateHostOrigin
// ---------------------------------------------------------------------------

func TestValidateHostOriginLoopback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		host   string
		origin string
		want   bool
	}{
		{"localhost host", "localhost:8080", "", true},
		{"127.0.0.1 host", "127.0.0.1:8080", "", true},
		{"external host", "example.com:8080", "", false},
		{"localhost origin", "localhost:8080", "http://localhost:3000", true},
		{"external origin", "localhost:8080", "http://evil.com", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest("GET", "/", nil)
			req.Host = tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			got := validateHostOrigin(w, req)
			if got != tc.want {
				t.Errorf("validateHostOrigin() = %v, want %v", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// API Server adapter -- net.IP loopback detection
// ---------------------------------------------------------------------------

func TestIsLoopbackHostIP(t *testing.T) {
	t.Parallel()

	ip := net.ParseIP("127.0.0.2")
	if !ip.IsLoopback() {
		t.Error("127.0.0.2 should be loopback")
	}
}
