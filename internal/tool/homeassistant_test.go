package tool

import (
	"context"
	"testing"
)

func TestHomeAssistantTool_Basics(t *testing.T) {
	t.Parallel()
	tool := NewHomeAssistantTool()

	if tool.Name() != "homeassistant" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "homeassistant")
	}
	if tool.Toolset() != "iot" {
		t.Errorf("Toolset() = %q, want %q", tool.Toolset(), "iot")
	}
	if tool.MaxResultChars() != haMaxResultChars {
		t.Errorf("MaxResultChars() = %d, want %d", tool.MaxResultChars(), haMaxResultChars)
	}
	schema := tool.Schema()
	if schema.Name != "homeassistant" {
		t.Errorf("Schema().Name = %q, want %q", schema.Name, "homeassistant")
	}
}

func TestHomeAssistantTool_IsAvailable_NoToken(t *testing.T) {
	tool := &HomeAssistantTool{token: ""}
	if tool.IsAvailable() {
		t.Error("IsAvailable() should be false without token")
	}
}

func TestHomeAssistantTool_Execute_NoToken(t *testing.T) {
	t.Parallel()
	tool := &HomeAssistantTool{token: ""}
	ctx := context.Background()

	_, err := tool.Execute(ctx, map[string]any{"action": "list_entities"})
	if err == nil {
		t.Error("expected error for missing token")
	}
}

func TestHomeAssistantTool_Execute_MissingAction(t *testing.T) {
	t.Parallel()
	tool := &HomeAssistantTool{token: "test-token"}
	ctx := context.Background()

	_, err := tool.Execute(ctx, map[string]any{})
	if err == nil {
		t.Error("expected error for missing action")
	}
}

func TestHomeAssistantTool_Execute_UnknownAction(t *testing.T) {
	t.Parallel()
	tool := &HomeAssistantTool{token: "test-token"}
	ctx := context.Background()

	_, err := tool.Execute(ctx, map[string]any{"action": "fly"})
	if err == nil {
		t.Error("expected error for unknown action")
	}
}

func TestHomeAssistantTool_Execute_GetState_MissingEntityID(t *testing.T) {
	t.Parallel()
	tool := &HomeAssistantTool{token: "test-token"}
	ctx := context.Background()

	_, err := tool.Execute(ctx, map[string]any{
		"action": "get_state",
	})
	if err == nil {
		t.Error("expected error for missing entity_id")
	}
}

func TestHomeAssistantTool_Execute_CallService_MissingDomain(t *testing.T) {
	t.Parallel()
	tool := &HomeAssistantTool{token: "test-token"}
	ctx := context.Background()

	_, err := tool.Execute(ctx, map[string]any{
		"action": "call_service",
	})
	if err == nil {
		t.Error("expected error for missing domain/service")
	}
}

func TestHomeAssistantTool_Execute_CallService_BlockedDomain(t *testing.T) {
	t.Parallel()
	tool := &HomeAssistantTool{token: "test-token"}
	ctx := context.Background()

	_, err := tool.Execute(ctx, map[string]any{
		"action":  "call_service",
		"domain":  "shell_command",
		"service": "run",
	})
	if err == nil {
		t.Error("expected error for blocked domain")
	}
}

func TestIsBlockedDomain(t *testing.T) {
	t.Parallel()
	tests := []struct {
		domain string
		want   bool
	}{
		{"shell_command", true},
		{"python_script", true},
		{"rest_command", true},
		{"script", true},
		{"automation", true},
		{"scene", true},
		{"light", false},
		{"switch", false},
		{"sensor", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.domain, func(t *testing.T) {
			t.Parallel()
			got := isBlockedDomain(tt.domain)
			if got != tt.want {
				t.Errorf("isBlockedDomain(%q) = %v, want %v", tt.domain, got, tt.want)
			}
		})
	}
}

func TestValidateHAInput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid", "light.living_room", false},
		{"slash", "light/living_room", true},
		{"backslash", "light\\living_room", true},
		{"dot_dot", "../etc/passwd", true},
		{"question", "light?test", true},
		{"hash", "light#test", true},
		{"null_byte", "light\x00room", true},
		{"empty", "", false},
		{"simple", "turn_on", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateHAInput(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateHAInput(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestHomeAssistantTool_DefaultURL(t *testing.T) {
	t.Parallel()
	tool := NewHomeAssistantTool()
	if tool.apiURL == "" {
		t.Error("apiURL should not be empty")
	}
}

func TestHomeAssistantTool_Constants(t *testing.T) {
	t.Parallel()
	if haDefaultAPIURL != "https://homeassistant.local:8123/api" {
		t.Errorf("haDefaultAPIURL = %q, unexpected", haDefaultAPIURL)
	}
	if haMaxResultChars != 50000 {
		t.Errorf("haMaxResultChars = %d, want 50000", haMaxResultChars)
	}
}
