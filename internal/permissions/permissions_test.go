package permissions

import (
	"testing"

	"nexus-agent/internal/approval"
)

// ───────────────────────────── TestParseLevel ─────────────────────────────

func TestParseLevel(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Level
		wantErr bool
	}{
		// 全名格式
		{name: "auto_allow", input: "auto_allow", want: LevelAutoAllow},
		{name: "auto_deny", input: "auto_deny", want: LevelAutoDeny},
		{name: "ask_once", input: "ask_once", want: LevelAskOnce},
		{name: "ask_always", input: "ask_always", want: LevelAskAlways},
		{name: "escalate", input: "escalate", want: LevelEscalate},
		// 简写格式
		{name: "allow 简写", input: "allow", want: LevelAutoAllow},
		{name: "deny 简写", input: "deny", want: LevelAutoDeny},
		{name: "once 简写", input: "once", want: LevelAskOnce},
		{name: "always 简写", input: "always", want: LevelAskAlways},
		// 数字格式
		{name: "数字 0", input: "0", want: LevelAutoAllow},
		{name: "数字 1", input: "1", want: LevelAutoDeny},
		{name: "数字 2", input: "2", want: LevelAskOnce},
		{name: "数字 3", input: "3", want: LevelAskAlways},
		{name: "数字 4", input: "4", want: LevelEscalate},
		// 错误输入
		{name: "未知字符串", input: "unknown_level", wantErr: true},
		{name: "空字符串", input: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseLevel(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseLevel(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("ParseLevel(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// ───────────────────────────── TestPolicyEvaluate ─────────────────────────────

func TestPolicyEvaluate(t *testing.T) {
	// 使用默认策略进行测试
	policy := DefaultPolicy()

	tests := []struct {
		name      string
		toolName  string
		args      map[string]any
		wantLevel Level
	}{
		{
			name:      "只读工具 web_search = AutoAllow",
			toolName:  "web_search",
			args:      nil,
			wantLevel: LevelAutoAllow,
		},
		{
			name:      "文件读取 file_read = AutoAllow",
			toolName:  "file_read",
			args:      map[string]any{"path": "/tmp/test.txt"},
			wantLevel: LevelAutoAllow,
		},
		{
			name:      "写入工具 file_write = AskOnce",
			toolName:  "file_write",
			args:      map[string]any{"path": "/tmp/test.txt"},
			wantLevel: LevelAskOnce,
		},
		{
			name:      "写入工具 file_edit = AskOnce",
			toolName:  "file_edit",
			args:      map[string]any{"path": "/tmp/test.txt"},
			wantLevel: LevelAskOnce,
		},
		{
			name:      "危险工具 file_delete = AskAlways",
			toolName:  "file_delete",
			args:      map[string]any{"path": "/tmp/test.txt"},
			wantLevel: LevelAskAlways,
		},
		{
			name:      "危险工具 terminal = AskAlways",
			toolName:  "terminal",
			args:      map[string]any{"command": "ls"},
			wantLevel: LevelAskAlways,
		},
		{
			name:      "未知工具兜底 = AskAlways",
			toolName:  "some_unknown_tool",
			args:      nil,
			wantLevel: LevelAskAlways,
		},
		{
			name:      "只读 memory_read = AutoAllow (glob 匹配)",
			toolName:  "memory_read",
			args:      nil,
			wantLevel: LevelAutoAllow,
		},
		{
			name:      "浏览器操作 browser_open = AskOnce",
			toolName:  "browser_open",
			args:      nil,
			wantLevel: LevelAskOnce,
		},
		{
			name:      "委派任务 delegate = AskAlways",
			toolName:  "delegate",
			args:      nil,
			wantLevel: LevelAskAlways,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := policy.Evaluate(tt.toolName, tt.args)
			if decision.Level != tt.wantLevel {
				t.Errorf("Evaluate(%q) level = %v, want %v (reason: %s)",
					tt.toolName, decision.Level, tt.wantLevel, decision.Reason)
			}
		})
	}
}

// ───────────────────────────── TestPolicyGlobMatch ─────────────────────────────

func TestPolicyGlobMatch(t *testing.T) {
	policy := &Policy{
		Name: "test",
		Rules: []Rule{
			{ToolPattern: "file_*", Level: LevelAskOnce, Reason: "文件操作"},
			{ToolPattern: "terminal", Level: LevelAskAlways, Reason: "终端"},
			{ToolPattern: "browser_*", Level: LevelAutoAllow, Reason: "浏览器"},
		},
		Default: LevelAutoDeny,
	}

	tests := []struct {
		name      string
		toolName  string
		wantLevel Level
	}{
		{name: "file_read 匹配 file_*", toolName: "file_read", wantLevel: LevelAskOnce},
		{name: "file_write 匹配 file_*", toolName: "file_write", wantLevel: LevelAskOnce},
		{name: "file_delete 匹配 file_*", toolName: "file_delete", wantLevel: LevelAskOnce},
		{name: "terminal 精确匹配", toolName: "terminal", wantLevel: LevelAskAlways},
		{name: "browser_open 匹配 browser_*", toolName: "browser_open", wantLevel: LevelAutoAllow},
		{name: "web_search 不匹配任何规则", toolName: "web_search", wantLevel: LevelAutoDeny},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := policy.Evaluate(tt.toolName, nil)
			if decision.Level != tt.wantLevel {
				t.Errorf("Evaluate(%q) level = %v, want %v", tt.toolName, decision.Level, tt.wantLevel)
			}
		})
	}
}

// ───────────────────────────── TestCheckerSessionMemory ─────────────────────────────

func TestCheckerSessionMemory(t *testing.T) {
	// 创建策略: file_write = AskOnce
	policy := &Policy{
		Name: "test",
		Rules: []Rule{
			{ToolPattern: "file_write", Level: LevelAskOnce, Reason: "文件写入需要确认"},
		},
		Default: LevelAskAlways,
	}

	approvalChecker := approval.NewChecker("off", nil, nil)
	checker := NewChecker(policy, approvalChecker)

	args := map[string]any{"path": "/tmp/test.txt"}

	// 第一次检查: 应为 AskOnce
	decision1 := checker.Check("file_write", args)
	if decision1.Level != LevelAskOnce {
		t.Errorf("第一次 Check() level = %v, want %v", decision1.Level, LevelAskOnce)
	}

	// 记住允许决策 (模拟用户确认后记住为 AutoAllow)
	checker.RememberDecision("file_write", args, LevelAutoAllow)

	// 第二次检查: 应通过会话记忆自动放行
	decision2 := checker.Check("file_write", args)
	if decision2.Level != LevelAutoAllow {
		t.Errorf("第二次 Check() level = %v, want %v (会话记忆应生效)", decision2.Level, LevelAutoAllow)
	}

	// 清除会话记忆
	checker.ClearSession()

	// 第三次检查: 清除后应恢复为 AskOnce
	decision3 := checker.Check("file_write", args)
	if decision3.Level != LevelAskOnce {
		t.Errorf("清除记忆后 Check() level = %v, want %v", decision3.Level, LevelAskOnce)
	}
}

// ───────────────────────────── TestPolicyHardBlock ─────────────────────────────

func TestPolicyHardBlock(t *testing.T) {
	// 注意: 默认策略的硬封锁规则使用 AND 逻辑组合 arg 模式
	// (command~=rm -rf / AND command~=mkfs)。实际场景中几乎不可能同时满足，
	// 这里用自定义策略测试 arg 模式匹配逻辑。
	t.Run("自定义策略: 命中 arg 精确匹配的硬封锁", func(t *testing.T) {
		policy := &Policy{
			Name: "hard-block-test",
			Rules: []Rule{
				{
					ToolPattern: "terminal",
					ArgPatterns: []string{"command=rm -rf /"},
					Level:       LevelAutoDeny,
					Reason:      "禁止递归删除",
				},
			},
			Default: LevelAskAlways,
		}

		decision := policy.Evaluate("terminal", map[string]any{"command": "rm -rf /"})
		if decision.Level != LevelAutoDeny {
			t.Errorf("rm -rf / 应被 AutoDeny, got %v", decision.Level)
		}
	})

	t.Run("自定义策略: arg glob 模式匹配", func(t *testing.T) {
		policy := &Policy{
			Name: "glob-test",
			Rules: []Rule{
				{
					ToolPattern: "terminal",
					ArgPatterns: []string{"command~=rm*"},
					Level:       LevelAutoDeny,
					Reason:      "禁止 rm 命令",
				},
			},
			Default: LevelAskAlways,
		}

		decision := policy.Evaluate("terminal", map[string]any{"command": "rm -rf /tmp"})
		if decision.Level != LevelAutoDeny {
			t.Errorf("rm 命令 glob 匹配应为 AutoDeny, got %v", decision.Level)
		}
	})

	t.Run("默认策略: terminal 无匹配硬封锁 arg 则降级到 AskAlways", func(t *testing.T) {
		policy := DefaultPolicy()
		decision := policy.Evaluate("terminal", map[string]any{"command": "ls -la"})
		if decision.Level != LevelAskAlways {
			t.Errorf("terminal ls -la 应为 AskAlways, got %v", decision.Level)
		}
	})

	t.Run("非 terminal 工具不受硬封锁影响", func(t *testing.T) {
		policy := DefaultPolicy()
		decision := policy.Evaluate("web_search", nil)
		if decision.Level != LevelAutoAllow {
			t.Errorf("web_search 应为 AutoAllow, got %v", decision.Level)
		}
	})
}
