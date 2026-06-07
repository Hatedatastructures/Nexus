package memory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- NewBuiltinProvider ----

func TestNewBuiltinProvider(t *testing.T) {
	t.Parallel()

	p := NewBuiltinProvider(t.TempDir())
	if p == nil {
		t.Fatal("expected non-nil provider")
	}
	if p.Name() != "builtin" {
		t.Errorf("expected name 'builtin', got %q", p.Name())
	}
}

// ---- Initialize ----

func TestBuiltinProvider_Initialize(t *testing.T) {
	t.Parallel()

	t.Run("creates directory and files", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		p := NewBuiltinProvider(dir)

		if err := p.Initialize(context.Background(), "session-1"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify files were created
		memPath := filepath.Join(dir, "memories", "MEMORY.md")
		userPath := filepath.Join(dir, "memories", "USER.md")
		for _, path := range []string{memPath, userPath} {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Errorf("expected file %s to exist: %v", path, err)
			} else if len(data) != 0 {
				t.Errorf("expected empty file, got %d bytes", len(data))
			}
		}
	})

	t.Run("loads existing content", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		memDir := filepath.Join(dir, "memories")
		mkdir(t, memDir)
		writeFile(t, filepath.Join(memDir, "MEMORY.md"), "existing entry")
		writeFile(t, filepath.Join(memDir, "USER.md"), "user info")

		p := NewBuiltinProvider(dir)
		if err := p.Initialize(context.Background(), "s1"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		prompt := p.SystemPromptBlock()
		if !strings.Contains(prompt, "existing entry") {
			t.Error("expected existing entry in system prompt")
		}
		if !strings.Contains(prompt, "user info") {
			t.Error("expected user info in system prompt")
		}
	})

	t.Run("fails with unwritable directory", func(t *testing.T) {
		t.Parallel()
		// Create a file where a directory should be, so MkdirAll fails
		dir := t.TempDir()
		blocker := filepath.Join(dir, "memories")
		// Write a file (not a directory) at the "memories" path
		if err := os.WriteFile(blocker, []byte("blocker"), 0600); err != nil {
			t.Fatalf("setup: %v", err)
		}
		p := NewBuiltinProvider(dir)
		err := p.Initialize(context.Background(), "s1")
		if err == nil {
			t.Fatal("expected error when memories path is a file")
		}
	})
}

// ---- SystemPromptBlock ----

func TestBuiltinProvider_SystemPromptBlock(t *testing.T) {
	t.Parallel()

	t.Run("returns empty when no entries", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		p := NewBuiltinProvider(dir)
		if err := p.Initialize(context.Background(), "s1"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if p.SystemPromptBlock() != "" {
			t.Errorf("expected empty prompt, got %q", p.SystemPromptBlock())
		}
	})

	t.Run("wraps in memory-context tags", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		memDir := filepath.Join(dir, "memories")
		mkdir(t, memDir)
		writeFile(t, filepath.Join(memDir, "MEMORY.md"), "test entry")

		p := NewBuiltinProvider(dir)
		if err := p.Initialize(context.Background(), "s1"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		prompt := p.SystemPromptBlock()
		if !strings.Contains(prompt, "<memory-context>") {
			t.Error("expected <memory-context> tag")
		}
		if !strings.Contains(prompt, "</memory-context>") {
			t.Error("expected </memory-context> tag")
		}
		if !strings.Contains(prompt, "test entry") {
			t.Error("expected test entry in prompt")
		}
	})
}

// ---- SyncTurn ----

func TestBuiltinProvider_SyncTurn(t *testing.T) {
	t.Parallel()

	p := NewBuiltinProvider(t.TempDir())
	if err := p.SyncTurn(context.Background(), "user-msg", "assistant-msg"); err != nil {
		t.Fatalf("SyncTurn should always succeed: %v", err)
	}
}

// ---- Shutdown ----

func TestBuiltinProvider_Shutdown(t *testing.T) {
	t.Parallel()

	p := NewBuiltinProvider(t.TempDir())
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown should always succeed: %v", err)
	}
}

// ---- GetToolSchemas ----

func TestBuiltinProvider_GetToolSchemas(t *testing.T) {
	t.Parallel()

	p := NewBuiltinProvider(t.TempDir())
	schemas := p.GetToolSchemas()
	if len(schemas) != 1 {
		t.Fatalf("expected 1 schema, got %d", len(schemas))
	}
	if schemas[0].Name != "memory" {
		t.Errorf("expected schema name 'memory', got %q", schemas[0].Name)
	}
}

// ---- HandleToolCall ----

func TestBuiltinProvider_HandleToolCall(t *testing.T) {
	t.Parallel()

	setup := func(t *testing.T) *BuiltinProvider {
		t.Helper()
		dir := t.TempDir()
		p := NewBuiltinProvider(dir)
		if err := p.Initialize(context.Background(), "s1"); err != nil {
			t.Fatalf("init: %v", err)
		}
		return p
	}

	t.Run("rejects unknown tool", func(t *testing.T) {
		t.Parallel()
		p := setup(t)
		_, err := p.HandleToolCall(context.Background(), "unknown-tool", nil)
		if err == nil {
			t.Fatal("expected error for unknown tool")
		}
	})

	t.Run("rejects invalid target", func(t *testing.T) {
		t.Parallel()
		p := setup(t)
		result, err := p.HandleToolCall(context.Background(), "memory", map[string]any{
			"action":  "add",
			"target":  "invalid",
			"content": "test",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "false") {
			t.Error("expected success=false for invalid target")
		}
	})

	t.Run("add requires content", func(t *testing.T) {
		t.Parallel()
		p := setup(t)
		result, err := p.HandleToolCall(context.Background(), "memory", map[string]any{
			"action": "add",
			"target": "memory",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "false") {
			t.Error("expected success=false for missing content")
		}
	})

	t.Run("add succeeds", func(t *testing.T) {
		t.Parallel()
		p := setup(t)
		result, err := p.HandleToolCall(context.Background(), "memory", map[string]any{
			"action":  "add",
			"target":  "memory",
			"content": "test fact",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(result), &parsed); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if parsed["success"] != true {
			t.Error("expected success=true")
		}
	})

	t.Run("add rejects duplicate", func(t *testing.T) {
		t.Parallel()
		p := setup(t)
		args := map[string]any{
			"action":  "add",
			"target":  "memory",
			"content": "duplicate fact",
		}
		_, _ = p.HandleToolCall(context.Background(), "memory", args)
		result, _ := p.HandleToolCall(context.Background(), "memory", args)

		var parsed map[string]any
		_ = json.Unmarshal([]byte(result), &parsed)
		if parsed["success"] != true {
			// Duplicate is not an error; it returns success with message
			t.Error("duplicate add should return success=true with message")
		}
	})

	t.Run("replace requires old_text and content", func(t *testing.T) {
		t.Parallel()
		p := setup(t)

		result, _ := p.HandleToolCall(context.Background(), "memory", map[string]any{
			"action": "replace",
			"target": "memory",
		})
		if !strings.Contains(result, "false") {
			t.Error("expected failure without old_text")
		}

		result, _ = p.HandleToolCall(context.Background(), "memory", map[string]any{
			"action":   "replace",
			"target":   "memory",
			"old_text": "x",
		})
		if !strings.Contains(result, "false") {
			t.Error("expected failure without content")
		}
	})

	t.Run("replace succeeds", func(t *testing.T) {
		t.Parallel()
		p := setup(t)
		_, _ = p.HandleToolCall(context.Background(), "memory", map[string]any{
			"action":  "add",
			"target":  "memory",
			"content": "old entry text",
		})
		result, _ := p.HandleToolCall(context.Background(), "memory", map[string]any{
			"action":   "replace",
			"target":   "memory",
			"old_text": "old entry",
			"content":  "new entry text",
		})
		var parsed map[string]any
		_ = json.Unmarshal([]byte(result), &parsed)
		if parsed["success"] != true {
			t.Error("expected success=true for replace")
		}
	})

	t.Run("remove requires old_text", func(t *testing.T) {
		t.Parallel()
		p := setup(t)
		result, _ := p.HandleToolCall(context.Background(), "memory", map[string]any{
			"action": "remove",
			"target": "memory",
		})
		if !strings.Contains(result, "false") {
			t.Error("expected failure without old_text")
		}
	})

	t.Run("remove succeeds", func(t *testing.T) {
		t.Parallel()
		p := setup(t)
		_, _ = p.HandleToolCall(context.Background(), "memory", map[string]any{
			"action":  "add",
			"target":  "memory",
			"content": "to be removed",
		})
		result, _ := p.HandleToolCall(context.Background(), "memory", map[string]any{
			"action":   "remove",
			"target":   "memory",
			"old_text": "to be removed",
		})
		var parsed map[string]any
		_ = json.Unmarshal([]byte(result), &parsed)
		if parsed["success"] != true {
			t.Error("expected success=true for remove")
		}
	})

	t.Run("rejects unknown action", func(t *testing.T) {
		t.Parallel()
		p := setup(t)
		result, _ := p.HandleToolCall(context.Background(), "memory", map[string]any{
			"action": "explode",
			"target": "memory",
		})
		if !strings.Contains(result, "false") {
			t.Error("expected failure for unknown action")
		}
	})
}

// helpers

func mkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
