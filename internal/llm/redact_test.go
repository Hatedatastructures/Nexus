package llm

import (
	"strings"
	"testing"
)

func TestRedactErrorBody_SkKey(t *testing.T) {
	input := `{"error": "invalid api_key: sk-proj-abc123def456ghi789"}`
	got := RedactErrorBody(input)
	if strings.Contains(got, "sk-proj-abc123def456ghi789") {
		t.Errorf("API key not redacted: %s", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Errorf("expected [REDACTED] placeholder, got: %s", got)
	}
}

func TestRedactErrorBody_Bearer(t *testing.T) {
	input := `{"error": "Bearer xai-abc123def456ghi789jkl012"}`
	got := RedactErrorBody(input)
	if strings.Contains(got, "xai-abc123") {
		t.Errorf("Bearer token not redacted: %s", got)
	}
}

func TestRedactErrorBody_ApiKeyField(t *testing.T) {
	input := `"api_key": "sk-ant-1234567890abcdef"`
	got := RedactErrorBody(input)
	if strings.Contains(got, "sk-ant-1234567890abcdef") {
		t.Errorf("api_key field not redacted: %s", got)
	}
}

func TestRedactErrorBody_SecretKeyField(t *testing.T) {
	input := `"secret_key": "wJalrXUtnFEMI_K7MDENG_bPxRfiCYEXAMPLEKEY"`
	got := RedactErrorBody(input)
	if strings.Contains(got, "wJalrXUtnFEMI") {
		t.Errorf("secret_key not redacted: %s", got)
	}
}

func TestRedactErrorBody_Truncation(t *testing.T) {
	longBody := strings.Repeat("x", 600)
	got := RedactErrorBody(longBody)
	if !strings.Contains(got, "(truncated)") {
		t.Errorf("long body should be truncated, got length %d", len(got))
	}
}

func TestRedactErrorBody_PreservesNormalText(t *testing.T) {
	input := `{"error": "rate limit exceeded", "status": 429}`
	got := RedactErrorBody(input)
	if got != input {
		t.Errorf("normal error text should be preserved, got: %s", got)
	}
}

func TestRedactErrorBody_KeyEquals(t *testing.T) {
	input := `key=AIzaSyB1234567890abcdefghijklmnopqrstuvwx`
	got := RedactErrorBody(input)
	if strings.Contains(got, "AIzaSyB1234567890") {
		t.Errorf("key= pattern not redacted: %s", got)
	}
}
