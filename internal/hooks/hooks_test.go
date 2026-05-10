package hooks

import (
	"context"
	"fmt"
	"testing"
)

// ───────────────────────────── Mock Hook ─────────────────────────────

// mockHook 是用于测试的 Hook 实现。
type mockHook struct {
	name    string
	event   string
	matcher string // 空字符串表示匹配所有工具
	resp    *HookResponse
	err     error
	called  bool
}

func (h *mockHook) Name() string  { return h.name }
func (h *mockHook) Event() string { return h.event }

func (h *mockHook) Match(toolName string) bool {
	if h.matcher == "" {
		return true
	}
	return h.matcher == toolName
}

func (h *mockHook) Execute(_ context.Context, _ *HookEvent) (*HookResponse, error) {
	h.called = true
	return h.resp, h.err
}

// ───────────────────────────── TestHookManagerRegister ─────────────────────────────

func TestHookManagerRegister(t *testing.T) {
	tests := []struct {
		name    string
		hooks   []Hook
		wantErr bool
	}{
		{
			name:    "注册单个 hook",
			hooks:   []Hook{&mockHook{name: "h1", event: EventPreToolCall, resp: &HookResponse{Decision: "allow"}}},
			wantErr: false,
		},
		{
			name: "注册多个 hook",
			hooks: []Hook{
				&mockHook{name: "h1", event: EventPreToolCall, resp: &HookResponse{Decision: "allow"}},
				&mockHook{name: "h2", event: EventPostToolCall, resp: &HookResponse{Decision: "allow"}},
				&mockHook{name: "h3", event: EventPreToolCall, resp: &HookResponse{Decision: "allow"}},
			},
			wantErr: false,
		},
		{
			name:    "注册 nil hook 应报错",
			hooks:   []Hook{nil},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr := NewHookManager("", true)

			var err error
			for _, h := range tt.hooks {
				if regErr := mgr.Register(h); regErr != nil {
					err = regErr
				}
			}

			if (err != nil) != tt.wantErr {
				t.Errorf("Register() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}

	// 验证注册后 hook 确实被添加
	t.Run("注册后 hook 可被匹配执行", func(t *testing.T) {
		mgr := NewHookManager("", true)
		hook := &mockHook{name: "my-hook", event: EventPreToolCall, matcher: "file_write", resp: &HookResponse{Decision: "block", Reason: "阻止"}}
		if err := mgr.Register(hook); err != nil {
			t.Fatalf("Register() error: %v", err)
		}

		_, blocked, err := mgr.ExecutePreHooks(context.Background(), "file_write", nil)
		if err != nil {
			t.Fatalf("ExecutePreHooks() error: %v", err)
		}
		if !blocked {
			t.Error("注册的 hook 应阻止 file_write")
		}
		if !hook.called {
			t.Error("注册的 hook 应被调用")
		}
	})
}

// ───────────────────────────── TestExecutePreHooks ─────────────────────────────

func TestExecutePreHooks(t *testing.T) {
	tests := []struct {
		name       string
		hooks      []Hook
		toolName   string
		wantBlock  bool
		wantReason string
	}{
		{
			name: "无匹配 hook 则不阻止",
			hooks: []Hook{
				&mockHook{name: "h1", event: EventPostToolCall, resp: &HookResponse{Decision: "block"}},
			},
			toolName:  "file_read",
			wantBlock: false,
		},
		{
			name: "allow hook 不阻止",
			hooks: []Hook{
				&mockHook{name: "h1", event: EventPreToolCall, resp: &HookResponse{Decision: "allow"}},
			},
			toolName:  "file_read",
			wantBlock: false,
		},
		{
			name: "block hook 终止链",
			hooks: []Hook{
				&mockHook{name: "blocker", event: EventPreToolCall, resp: &HookResponse{Decision: "block", Reason: "禁止"}},
			},
			toolName:   "file_write",
			wantBlock:  true,
			wantReason: "禁止",
		},
		{
			name: "首个 block hook 终止链，后续 hook 不执行",
			hooks: []Hook{
				&mockHook{name: "blocker", event: EventPreToolCall, resp: &HookResponse{Decision: "block", Reason: "阻止"}},
				&mockHook{name: "second", event: EventPreToolCall, matcher: "file_write", resp: &HookResponse{Decision: "allow"}},
			},
			toolName:   "file_write",
			wantBlock:  true,
			wantReason: "阻止",
		},
		{
			name: "多个 allow hook 全部执行",
			hooks: func() []Hook {
				h1 := &mockHook{name: "h1", event: EventPreToolCall, resp: &HookResponse{Decision: "allow"}}
				h2 := &mockHook{name: "h2", event: EventPreToolCall, resp: &HookResponse{Decision: "allow"}}
				return []Hook{h1, h2}
			}(),
			toolName:  "file_read",
			wantBlock: false,
		},
		{
			name: "matcher 不匹配的 hook 被跳过",
			hooks: []Hook{
				&mockHook{name: "terminal_only", event: EventPreToolCall, matcher: "terminal", resp: &HookResponse{Decision: "block"}},
			},
			toolName:  "file_read",
			wantBlock: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr := NewHookManager("", true)
			for _, h := range tt.hooks {
				if err := mgr.Register(h); err != nil {
					t.Fatalf("Register() failed: %v", err)
				}
			}

			resp, blocked, err := mgr.ExecutePreHooks(context.Background(), tt.toolName, map[string]any{"key": "val"})
			if err != nil {
				t.Fatalf("ExecutePreHooks() error: %v", err)
			}

			if blocked != tt.wantBlock {
				t.Errorf("blocked = %v, want %v", blocked, tt.wantBlock)
			}

			if tt.wantBlock && resp != nil && resp.Reason != tt.wantReason {
				t.Errorf("reason = %q, want %q", resp.Reason, tt.wantReason)
			}
		})
	}
}

// ───────────────────────────── TestExecutePostHooks ─────────────────────────────

func TestExecutePostHooks(t *testing.T) {
	tests := []struct {
		name     string
		hooks    []Hook
		toolName string
		wantErr  bool
	}{
		{
			name:     "无匹配 hook 不报错",
			hooks:    []Hook{},
			toolName: "file_read",
			wantErr:  false,
		},
		{
			name: "所有 post hook 都执行",
			hooks: func() []Hook {
				h1 := &mockHook{name: "post1", event: EventPostToolCall, resp: &HookResponse{Decision: "allow"}}
				h2 := &mockHook{name: "post2", event: EventPostToolCall, resp: &HookResponse{Decision: "allow"}}
				return []Hook{h1, h2}
			}(),
			toolName: "file_write",
			wantErr:  false,
		},
		{
			name: "post hook 中的 block 不会中断链",
			hooks: func() []Hook {
				h1 := &mockHook{name: "post1", event: EventPostToolCall, resp: &HookResponse{Decision: "block"}}
				h2 := &mockHook{name: "post2", event: EventPostToolCall, resp: &HookResponse{Decision: "allow"}}
				return []Hook{h1, h2}
			}(),
			toolName: "file_write",
			wantErr:  false,
		},
		{
			name: "hook 执行失败被跳过不报错",
			hooks: []Hook{
				&mockHook{name: "failing", event: EventPostToolCall, err: fmt.Errorf("执行失败")},
			},
			toolName: "file_read",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr := NewHookManager("", true)
			for _, h := range tt.hooks {
				if err := mgr.Register(h); err != nil {
					t.Fatalf("Register() failed: %v", err)
				}
			}

			err := mgr.ExecutePostHooks(context.Background(), tt.toolName, map[string]any{}, "output")
			if (err != nil) != tt.wantErr {
				t.Errorf("ExecutePostHooks() error = %v, wantErr %v", err, tt.wantErr)
			}

			// 验证所有 hook 都被调用
			for _, h := range tt.hooks {
				if mh, ok := h.(*mockHook); ok {
					if !mh.called {
						t.Errorf("hook %q 未被调用", mh.name)
					}
				}
			}
		})
	}
}
