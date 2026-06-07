package hooks

import (
	"testing"
)

// ─── HookSpec struct field tests ────────────────────────────────

func TestHookSpec_Fields(t *testing.T) {
	t.Parallel()

	spec := HookSpec{
		Event:      EventPreToolCall,
		Command:    "bash script.sh",
		Matcher:    "file_.*",
		TimeoutSec: 120,
	}
	if spec.Event != EventPreToolCall {
		t.Errorf("Event = %q, want %q", spec.Event, EventPreToolCall)
	}
	if spec.Command != "bash script.sh" {
		t.Errorf("Command = %q", spec.Command)
	}
	if spec.Matcher != "file_.*" {
		t.Errorf("Matcher = %q", spec.Matcher)
	}
	if spec.TimeoutSec != 120 {
		t.Errorf("TimeoutSec = %d, want 120", spec.TimeoutSec)
	}
}

// ─── HookEvent field tests ──────────────────────────────────────

func TestHookEvent_AllFields(t *testing.T) {
	t.Parallel()

	event := HookEvent{
		EventName:  EventPostToolCall,
		ToolName:   "bash",
		ToolInput:  map[string]any{"command": "ls"},
		ToolOutput: "file1.txt file2.txt",
		SessionID:  "sess-123",
		CWD:        "/home/user",
	}
	if event.EventName != EventPostToolCall {
		t.Errorf("EventName = %q", event.EventName)
	}
	if event.ToolName != "bash" {
		t.Errorf("ToolName = %q", event.ToolName)
	}
	if event.ToolInput["command"] != "ls" {
		t.Errorf("ToolInput = %v", event.ToolInput)
	}
	if event.ToolOutput != "file1.txt file2.txt" {
		t.Errorf("ToolOutput = %q", event.ToolOutput)
	}
	if event.SessionID != "sess-123" {
		t.Errorf("SessionID = %q", event.SessionID)
	}
	if event.CWD != "/home/user" {
		t.Errorf("CWD = %q", event.CWD)
	}
}

// ─── HookResponse field tests ───────────────────────────────────

func TestHookResponse_AllFields(t *testing.T) {
	t.Parallel()

	resp := HookResponse{
		Decision: "modify",
		Reason:   "sanitized input",
		Message:  "cleaned command",
	}
	if resp.Decision != "modify" {
		t.Errorf("Decision = %q", resp.Decision)
	}
	if resp.Reason != "sanitized input" {
		t.Errorf("Reason = %q", resp.Reason)
	}
	if resp.Message != "cleaned command" {
		t.Errorf("Message = %q", resp.Message)
	}
	if resp.IsBlock() {
		t.Error("modify should not be block")
	}
	if !resp.IsModify() {
		t.Error("modify should be IsModify")
	}
}

// ─── Event constants ───────────────────────────────────────────

func TestEventConstants(t *testing.T) {
	t.Parallel()

	if EventPreToolCall != "pre_tool_call" {
		t.Errorf("EventPreToolCall = %q, want %q", EventPreToolCall, "pre_tool_call")
	}
	if EventPostToolCall != "post_tool_call" {
		t.Errorf("EventPostToolCall = %q, want %q", EventPostToolCall, "post_tool_call")
	}
}

// ─── NewHookManager fields ──────────────────────────────────────

func TestNewHookManager_Fields(t *testing.T) {
	t.Parallel()

	mgr := NewHookManager("", true)
	if mgr == nil {
		t.Fatal("NewHookManager returned nil")
	}
	ref := mgr.AllowlistRef()
	if ref == nil {
		t.Error("AllowlistRef should not be nil")
	}
}

// ─── ShellHook Name matches Command ────────────────────────────

func TestShellHook_NameMatchesCommand(t *testing.T) {
	t.Parallel()

	hook, err := NewShellHook(HookSpec{
		Event:   EventPreToolCall,
		Command: "bash check.sh",
	})
	if err != nil {
		t.Fatal(err)
	}
	if hook.Name() != "bash check.sh" {
		t.Errorf("Name() = %q, want %q", hook.Name(), "bash check.sh")
	}
	if hook.Command() != "bash check.sh" {
		t.Errorf("Command() = %q, want %q", hook.Command(), "bash check.sh")
	}
}

// ─── validateHookCommand additional cases ───────────────────────

func TestValidateHookCommand_TabCharacter(t *testing.T) {
	t.Parallel()

	err := validateHookCommand("echo\trm")
	if err == nil {
		t.Error("tab character should be rejected")
	}
}

// ─── Allowlist concurrent access ────────────────────────────────

func TestAllowlist_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	a := NewAllowlist(dir, false)

	done := make(chan bool)

	go func() {
		for i := 0; i < 100; i++ {
			a.Add("cmd1")
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 100; i++ {
			a.IsAllowed("cmd1")
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 100; i++ {
			a.Remove("cmd1")
		}
		done <- true
	}()

	<-done
	<-done
	<-done
}

// ─── CompileMatcher edge cases ──────────────────────────────────

func TestCompileMatcher_MatchesCorrectly(t *testing.T) {
	t.Parallel()

	re, err := CompileMatcher("^(file|bash)_.*$")
	if err != nil {
		t.Fatal(err)
	}
	if !re.MatchString("file_read") {
		t.Error("should match file_read")
	}
	if !re.MatchString("bash_exec") {
		t.Error("should match bash_exec")
	}
	if re.MatchString("search") {
		t.Error("should not match search")
	}
}

// ─── ValidateEvent error message ────────────────────────────────

func TestValidateEvent_ErrorMessage(t *testing.T) {
	t.Parallel()

	err := ValidateEvent("bad_event")
	if err == nil {
		t.Fatal("expected error")
	}
}

// ─── HookManager multiple hook registration ─────────────────────

func TestHookManager_RegisterMultiple(t *testing.T) {
	t.Parallel()

	mgr := NewHookManager("", true)

	hook1, err := NewShellHook(HookSpec{
		Event:   EventPreToolCall,
		Command: "bash pre1.sh",
	})
	if err != nil {
		t.Fatal(err)
	}
	hook2, err := NewShellHook(HookSpec{
		Event:   EventPreToolCall,
		Command: "bash pre2.sh",
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := mgr.Register(hook1); err != nil {
		t.Fatalf("Register hook1: %v", err)
	}
	if err := mgr.Register(hook2); err != nil {
		t.Fatalf("Register hook2: %v", err)
	}
}

// ─── Hook interface conformance ─────────────────────────────────

func TestShellHook_ImplementsHook(t *testing.T) {
	t.Parallel()

	var _ Hook = (*ShellHook)(nil)
}
