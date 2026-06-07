package commands

import (
	"strings"
	"testing"
)

func TestChatCommandName(t *testing.T) {
	t.Parallel()
	c := &ChatCommand{}
	if c.Name() != "chat" {
		t.Errorf("ChatCommand.Name() = %q, want %q", c.Name(), "chat")
	}
}

func TestChatCommandSynopsis(t *testing.T) {
	t.Parallel()
	c := &ChatCommand{}
	if c.Synopsis() == "" {
		t.Error("ChatCommand.Synopsis() returned empty string")
	}
}

func TestChatCommandRegistered(t *testing.T) {
	t.Parallel()
	cmd, ok := GetCommand("chat")
	if !ok {
		t.Fatal("chat command not registered")
	}
	if _, isChat := cmd.(*ChatCommand); !isChat {
		t.Errorf("expected *ChatCommand, got %T", cmd)
	}
}

func TestDeduplicateResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		resp string
		want string
	}{
		{
			name: "short response unchanged",
			resp: "hello",
			want: "hello",
		},
		{
			name: "medium response unchanged",
			resp: "This is a response that is under 200 characters and should be returned as-is without any deduplication applied.",
			want: "This is a response that is under 200 characters and should be returned as-is without any deduplication applied.",
		},
		{
			name: "long response without duplication",
			resp: strings.Repeat("abcdefghij", 25), // 250 chars, all unique
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := deduplicateResponse(tt.resp)
			// Ensure non-empty input gives non-empty output
			if tt.resp != "" && got == "" {
				t.Error("deduplicateResponse() returned empty for non-empty input")
			}
			// For short responses, exact match
			if len(tt.resp) < 200 {
				if got != tt.want {
					t.Errorf("deduplicateResponse() = %q, want %q", got, tt.want)
				}
			}
		})
	}
}

func TestDeduplicateResponseShortString(t *testing.T) {
	t.Parallel()
	input := "short"
	got := deduplicateResponse(input)
	if got != input {
		t.Errorf("short strings should be returned as-is, got %q", got)
	}
}

func TestSpinnerCharsDefined(t *testing.T) {
	t.Parallel()
	if len(spinnerChars) == 0 {
		t.Error("spinnerChars should not be empty")
	}
}
