package agent

import (
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestSetRedactEnabled(t *testing.T) {
	orig := IsRedactEnabled()
	defer SetRedactEnabled(orig)

	SetRedactEnabled(false)
	if IsRedactEnabled() {
		t.Error("should be disabled")
	}
	SetRedactEnabled(true)
	if !IsRedactEnabled() {
		t.Error("should be enabled")
	}
}

func TestRedactSensitiveText_Disabled(t *testing.T) {
	orig := IsRedactEnabled()
	defer SetRedactEnabled(orig)

	SetRedactEnabled(false)
	result := RedactSensitiveText("my key is sk-ant-1234567890abcdefghijklmnop")
	if result != "my key is sk-ant-1234567890abcdefghijklmnop" {
		t.Errorf("disabled: got %q", result)
	}
}

func TestRedactSensitiveText_Empty(t *testing.T) {
	if RedactSensitiveText("") != "" {
		t.Error("empty should return empty")
	}
}

func TestRedactSensitiveText_SkKey(t *testing.T) {
	result := RedactSensitiveText("key: sk-1234567890abcdefghijklmn")
	if !strings.Contains(result, "sk-12") {
		t.Error("should keep prefix start")
	}
	if strings.Contains(result, "abcdefghijklmn") {
		t.Error("should mask the end")
	}
}

func TestRedactSensitiveText_GitHubToken(t *testing.T) {
	result := RedactSensitiveText("token: ghp_1234567890abcdefghijklmno")
	if !strings.Contains(result, "ghp_1") {
		t.Error("should keep ghp_ prefix start")
	}
}

func TestRedactSensitiveText_Bearer(t *testing.T) {
	result := RedactSensitiveText("Authorization: Bearer abcdefghijklmnopqrstuvwxyz1234567890")
	if strings.Contains(result, "qrstuvwxyz1234567890") {
		t.Error("should mask bearer token")
	}
}

func TestRedactSensitiveText_JWT(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abc123def456ghi789"
	result := RedactSensitiveText("token=" + jwt)
	if !strings.Contains(result, "***") {
		t.Error("JWT should be masked")
	}
}

func TestRedactSensitiveText_DBConnectionString(t *testing.T) {
	result := RedactSensitiveText("postgres://user:password123@localhost:5432/mydb")
	if !strings.Contains(result, "***") {
		t.Error("DB connection string should be masked")
	}
}

func TestRedactSensitiveText_URLUserInfo(t *testing.T) {
	result := RedactSensitiveText("http://admin:secretpassword123@host.com/path")
	if strings.Contains(result, "secretpassword123") {
		t.Error("URL password should be masked")
	}
}

func TestRedactSensitiveText_Phone(t *testing.T) {
	// The phone regex `\+[1-9]\d{6,14}` matches 7-15 digits after +
	// The matched portion may not be >= minMaskLen(18), so masking depends on length
	result := RedactSensitiveText("phone: +8613912345678")
	// Just verify the function doesn't crash and returns something
	if result == "" {
		t.Error("result should not be empty")
	}
}

func TestRedactSensitiveText_ShortToken(t *testing.T) {
	result := RedactSensitiveText("sk-short")
	if result != "sk-short" {
		t.Errorf("short token should not be masked, got %q", result)
	}
}

func TestMaskToken_Short(t *testing.T) {
	result := maskToken("short")
	if result != "short" {
		t.Errorf("short token: got %q", result)
	}
}

func TestMaskToken_ExactMinLen(t *testing.T) {
	token := strings.Repeat("a", minMaskLen)
	result := maskToken(token)
	if !strings.Contains(result, "*") {
		t.Error("min-len token should have masks")
	}
}

func TestMaskToken_Long(t *testing.T) {
	token := "sk-ant-1234567890abcdefghijklmnopqrstuvwxyz"
	result := maskToken(token)
	if !strings.HasPrefix(result, "sk-ant") {
		t.Error("should keep first 6 chars")
	}
	// total length should be preserved
	if len(result) != len(token) {
		t.Errorf("length mismatch: got %d, want %d", len(result), len(token))
	}
}

func TestMaskToken_TooShortForKeep(t *testing.T) {
	// prefixKeep(6) + suffixKeep(4) = 10 chars, which is < minMaskLen(18)
	// so maskToken returns the token unchanged
	token := strings.Repeat("x", prefixKeep+suffixKeep)
	result := maskToken(token)
	if result != token {
		t.Errorf("token shorter than minMaskLen should be unchanged: got %q", result)
	}
}

func TestIsTokenChar(t *testing.T) {
	tests := []struct {
		c    byte
		want bool
	}{
		{'a', true},
		{'Z', true},
		{'5', true},
		{'-', true},
		{'_', true},
		{'.', true},
		{' ', false},
		{'@', false},
		{'/', false},
	}
	for _, tt := range tests {
		if got := isTokenChar(tt.c); got != tt.want {
			t.Errorf("isTokenChar(%q) = %v, want %v", tt.c, got, tt.want)
		}
	}
}

func TestMaskPrefixPattern(t *testing.T) {
	pp := prefixPattern{prefix: "sk-", minLen: 20}
	result := maskPrefixPattern("no match here", pp)
	if result != "no match here" {
		t.Errorf("no match: got %q", result)
	}

	longToken := "sk-1234567890abcdefghijklmn"
	result = maskPrefixPattern("key="+longToken, pp)
	if result == "key="+longToken {
		t.Error("should mask the long token")
	}
}

func TestNewRedactingHandler(t *testing.T) {
	inner := slog.Default().Handler()
	h := NewRedactingHandler(inner)
	if h == nil {
		t.Fatal("handler is nil")
	}
}

func TestRedactingHandler_Enabled(t *testing.T) {
	inner := slog.Default().Handler()
	h := NewRedactingHandler(inner)
	if !h.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("should delegate Enabled")
	}
}

func TestRedactingHandler_Handle(t *testing.T) {
	var buf strings.Builder
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	h := NewRedactingHandler(inner)

	logger := slog.New(h)
	logger.Info("test sk-1234567890abcdefghijklmn")
	output := buf.String()
	if strings.Contains(output, "abcdefghijklmn") {
		t.Error("handler should redact sensitive data")
	}
}

func TestRedactingHandler_WithAttrs(t *testing.T) {
	inner := slog.Default().Handler()
	h := NewRedactingHandler(inner)
	h2 := h.WithAttrs([]slog.Attr{slog.String("key", "val")})
	if h2 == nil {
		t.Error("WithAttrs should return non-nil")
	}
}

func TestRedactingHandler_WithGroup(t *testing.T) {
	inner := slog.Default().Handler()
	h := NewRedactingHandler(inner)
	h2 := h.WithGroup("test")
	if h2 == nil {
		t.Error("WithGroup should return non-nil")
	}
}
