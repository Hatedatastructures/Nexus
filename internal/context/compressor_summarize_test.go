package context

import (
	"strings"
	"testing"

	"nexus-agent/internal/llm"
)

// ---------------------------------------------------------------------------
// computeSummaryBudget
// ---------------------------------------------------------------------------

func TestComputeSummaryBudget(t *testing.T) {
	t.Parallel()
	c := NewCompressor(3, 20000)

	t.Run("empty messages returns minimum budget", func(t *testing.T) {
		t.Parallel()
		budget := c.computeSummaryBudget(nil)
		if budget != 2000 {
			t.Errorf("budget = %d, want 2000", budget)
		}
	})

	t.Run("small messages clamped to minimum", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleUser, Content: "short"},
		}
		budget := c.computeSummaryBudget(msgs)
		if budget != 2000 {
			t.Errorf("budget = %d, want 2000", budget)
		}
	})

	t.Run("large messages capped at maximum", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleUser, Content: strings.Repeat("x", 300000)},
		}
		budget := c.computeSummaryBudget(msgs)
		if budget != 12000 {
			t.Errorf("budget = %d, want 12000", budget)
		}
	})

	t.Run("medium messages within range", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleUser, Content: strings.Repeat("a", 20000)},
			{Role: llm.RoleAssistant, Content: strings.Repeat("b", 20000)},
		}
		budget := c.computeSummaryBudget(msgs)
		if budget < 2000 || budget > 12000 {
			t.Errorf("budget = %d, want in [2000, 12000]", budget)
		}
	})

	t.Run("tool call arguments included in budget", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{
				Role:    llm.RoleAssistant,
				Content: "",
				ToolCalls: []llm.ToolCall{
					{ID: "c1", Name: "terminal", Arguments: strings.Repeat("a", 40000)},
				},
			},
		}
		budget := c.computeSummaryBudget(msgs)
		// 40000/4 = 10000 tokens, 10000*0.20 = 2000 budget
		if budget < 2000 {
			t.Errorf("budget = %d, want >= 2000", budget)
		}
	})
}

// ---------------------------------------------------------------------------
// serializeForSummary
// ---------------------------------------------------------------------------

func TestSerializeForSummary(t *testing.T) {
	t.Parallel()
	c := NewCompressor(3, 20000)

	t.Run("empty messages returns empty string", func(t *testing.T) {
		t.Parallel()
		got := c.serializeForSummary(nil)
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("user message serialized", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleUser, Content: "hello world"},
		}
		got := c.serializeForSummary(msgs)
		if !strings.Contains(got, "[USER]: hello world") {
			t.Errorf("expected [USER] prefix, got %q", got)
		}
	})

	t.Run("assistant message with tool calls", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{
				Role:    llm.RoleAssistant,
				Content: "I will run a command",
				ToolCalls: []llm.ToolCall{
					{ID: "c1", Name: "terminal", Arguments: `{"command":"ls"}`},
				},
			},
		}
		got := c.serializeForSummary(msgs)
		if !strings.Contains(got, "[ASSISTANT]:") {
			t.Errorf("expected [ASSISTANT] prefix, got %q", got)
		}
		if !strings.Contains(got, "terminal") {
			t.Errorf("expected tool call name, got %q", got)
		}
		if !strings.Contains(got, "Tool calls:") {
			t.Errorf("expected tool calls section, got %q", got)
		}
	})

	t.Run("tool result message serialized", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleTool, Content: "output", ToolCallID: "call_1"},
		}
		got := c.serializeForSummary(msgs)
		if !strings.Contains(got, "[TOOL RESULT call_1]: output") {
			t.Errorf("expected tool result format, got %q", got)
		}
	})

	t.Run("long content truncated", func(t *testing.T) {
		t.Parallel()
		longContent := strings.Repeat("x", 7000)
		msgs := []llm.Message{
			{Role: llm.RoleUser, Content: longContent},
		}
		got := c.serializeForSummary(msgs)
		if strings.Contains(got, "[truncated]") == false {
			t.Error("long content should be truncated")
		}
	})

	t.Run("system message serialized with uppercase role", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleSystem, Content: "system prompt"},
		}
		got := c.serializeForSummary(msgs)
		if !strings.Contains(got, "[SYSTEM]: system prompt") {
			t.Errorf("expected [SYSTEM] prefix, got %q", got)
		}
	})

	t.Run("sensitive data redacted in tool call args", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{
				Role:    llm.RoleAssistant,
				Content: "",
				ToolCalls: []llm.ToolCall{
					{ID: "c1", Name: "terminal", Arguments: `{"command":"curl -H 'api_key: sk-abc123def456ghi789jkl012' http://example.com"}`},
				},
			},
		}
		got := c.serializeForSummary(msgs)
		if strings.Contains(got, "sk-abc123def456ghi789jkl012") {
			t.Error("sensitive API key should be redacted")
		}
		if !strings.Contains(got, "[REDACTED]") {
			t.Error("expected [REDACTED] in output")
		}
	})
}

// ---------------------------------------------------------------------------
// FormatSummary
// ---------------------------------------------------------------------------

func TestFormatSummary(t *testing.T) {
	t.Parallel()

	t.Run("empty messages returns structured empty summary", func(t *testing.T) {
		t.Parallel()
		got := FormatSummary(nil)
		if !strings.Contains(got, "## 消息统计") {
			t.Error("missing section header")
		}
		if !strings.Contains(got, "用户消息: 0 条") {
			t.Error("expected zero user messages")
		}
	})

	t.Run("counts messages by role", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleUser, Content: "do something important here"},
			{Role: llm.RoleAssistant, Content: "sure"},
			{Role: llm.RoleTool, Content: "result"},
			{Role: llm.RoleUser, Content: "another request from user side"},
		}
		got := FormatSummary(msgs)
		if !strings.Contains(got, "用户消息: 2 条") {
			t.Error("expected 2 user messages")
		}
		if !strings.Contains(got, "助手消息: 1 条") {
			t.Error("expected 1 assistant message")
		}
		if !strings.Contains(got, "工具结果: 1 条") {
			t.Error("expected 1 tool result")
		}
	})

	t.Run("extracts tool calls", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{
				Role:    llm.RoleAssistant,
				Content: "",
				ToolCalls: []llm.ToolCall{
					{ID: "c1", Name: "terminal", Arguments: `{"command":"go test"}`},
				},
			},
		}
		got := FormatSummary(msgs)
		if !strings.Contains(got, "## 工具调用摘要") {
			t.Error("missing tool calls section")
		}
		if !strings.Contains(got, "[terminal]") {
			t.Error("missing tool call entry")
		}
	})

	t.Run("extracts pending tasks from user messages", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleUser, Content: "Please implement the authentication module for the project"},
		}
		got := FormatSummary(msgs)
		if !strings.Contains(got, "## 待处理任务") {
			t.Error("missing pending tasks section")
		}
		if !strings.Contains(got, "authentication module") {
			t.Error("missing task content")
		}
	})

	t.Run("deduplicates short user messages", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleUser, Content: "ok"},
			{Role: llm.RoleUser, Content: "yes"},
		}
		got := FormatSummary(msgs)
		if strings.Contains(got, "## 待处理任务\n- ok") {
			t.Error("short messages should not appear as pending tasks")
		}
	})

	t.Run("extracts key files from tool calls", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{
				Role:    llm.RoleAssistant,
				Content: "",
				ToolCalls: []llm.ToolCall{
					{ID: "c1", Name: "write_file", Arguments: `{"path":"main.go"}`},
				},
			},
		}
		got := FormatSummary(msgs)
		if !strings.Contains(got, "## 关键文件") {
			t.Error("missing key files section")
		}
		if !strings.Contains(got, "main.go") {
			t.Error("missing file path")
		}
	})

	t.Run("extracts decisions from assistant content", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{
				Role:    llm.RoleAssistant,
				Content: "I decided to use the repository pattern for this module",
			},
		}
		got := FormatSummary(msgs)
		if !strings.Contains(got, "## 关键决策") {
			t.Error("missing decisions section")
		}
		if !strings.Contains(got, "repository pattern") {
			t.Error("missing decision content")
		}
	})

	t.Run("includes tool results in context", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleTool, Content: "Tests passed successfully", ToolCallID: "c1"},
		}
		got := FormatSummary(msgs)
		if !strings.Contains(got, "## 当前上下文") {
			t.Error("missing context section")
		}
		if !strings.Contains(got, "Tests passed") {
			t.Error("missing tool result in context")
		}
	})

	t.Run("long user request truncated in pending tasks", func(t *testing.T) {
		t.Parallel()
		longReq := "Please implement " + strings.Repeat("a", 300)
		msgs := []llm.Message{
			{Role: llm.RoleUser, Content: longReq},
		}
		got := FormatSummary(msgs)
		// The request should appear truncated (not full 300+ chars)
		for _, line := range strings.Split(got, "\n") {
			if strings.HasPrefix(line, "- Please implement") && len(line) > 210 {
				t.Errorf("pending task line too long: %d chars", len(line))
			}
		}
	})

	t.Run("limits tool calls to 20 in output", func(t *testing.T) {
		t.Parallel()
		msgs := make([]llm.Message, 30)
		for i := range msgs {
			msgs[i] = llm.Message{
				Role:    llm.RoleAssistant,
				Content: "",
				ToolCalls: []llm.ToolCall{
					{ID: "c1", Name: "terminal", Arguments: `{"command":"ls"}`},
				},
			}
		}
		got := FormatSummary(msgs)
		if !strings.Contains(got, "仅显示最近 20 次") {
			t.Error("expected truncation notice for 30 tool calls")
		}
	})
}

// ---------------------------------------------------------------------------
// extractArgSummary
// ---------------------------------------------------------------------------

func TestExtractArgSummary(t *testing.T) {
	t.Parallel()

	t.Run("empty string returns empty", func(t *testing.T) {
		t.Parallel()
		got := extractArgSummary("")
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("invalid JSON returns empty", func(t *testing.T) {
		t.Parallel()
		got := extractArgSummary("not json")
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("extracts path field", func(t *testing.T) {
		t.Parallel()
		got := extractArgSummary(`{"path":"/tmp/file.go","offset":1}`)
		if !strings.Contains(got, "path=/tmp/file.go") {
			t.Errorf("expected path extraction, got %q", got)
		}
	})

	t.Run("extracts command field", func(t *testing.T) {
		t.Parallel()
		got := extractArgSummary(`{"command":"go test ./..."}`)
		if !strings.Contains(got, "command=go test") {
			t.Errorf("expected command extraction, got %q", got)
		}
	})

	t.Run("extracts query field", func(t *testing.T) {
		t.Parallel()
		got := extractArgSummary(`{"query":"golang testing"}`)
		if !strings.Contains(got, "query=golang testing") {
			t.Errorf("expected query extraction, got %q", got)
		}
	})

	t.Run("truncates long values", func(t *testing.T) {
		t.Parallel()
		longVal := strings.Repeat("x", 100)
		got := extractArgSummary(`{"path":"` + longVal + `"}`)
		if len(got) > 90 {
			t.Errorf("result too long: %d chars, got %q", len(got), got)
		}
	})
}

// ---------------------------------------------------------------------------
// isDecisionStatement
// ---------------------------------------------------------------------------

func TestIsDecisionStatement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  bool
	}{
		{"I decided to use Redis for caching", true},
		{"We chose the microservices approach", true},
		{"Using PostgreSQL for the database", true},
		{"The approach we took was modular", true},
		{"Our strategy is incremental rollout", true},
		{"我决定使用 Go 语言重写这个项目", true},
		{"将架构改为微服务模式", true},
		{"迁移到新的云平台", true},
		{"切换到 PostgreSQL 数据库", true},
		{"hello world", false},
		{"ok", false},
		{"", false},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got := isDecisionStatement(tc.input)
			if got != tc.want {
				t.Errorf("isDecisionStatement(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// extractFilePath
// ---------------------------------------------------------------------------

func TestExtractFilePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		argsJSON string
		want     string
	}{
		{"path field", `{"path":"/tmp/file.go"}`, "/tmp/file.go"},
		{"file_path field", `{"file_path":"config.yaml"}`, "config.yaml"},
		{"file field", `{"file":"data.json"}`, "data.json"},
		{"filename field", `{"filename":"output.txt"}`, "output.txt"},
		{"destination field", `{"destination":"/var/log"}`, "/var/log"},
		{"no matching field", `{"command":"ls"}`, ""},
		{"empty string", "", ""},
		{"invalid JSON", "not json", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := extractFilePath(tc.argsJSON)
			if got != tc.want {
				t.Errorf("extractFilePath(%q) = %q, want %q", tc.argsJSON, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// inferFileOperation
// ---------------------------------------------------------------------------

func TestInferFileOperation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		toolName string
		want     string
	}{
		{"create_file", "已创建"},
		{"write_file", "已创建"},
		{"Write", "已创建"},
		{"NotebookEdit", "已创建"},
		{"edit_file", "已修改"},
		{"patch", "已修改"},
		{"Edit", "已修改"},
		{"replace", "已修改"},
		{"read_file", "已读取"},
		{"Read", "已读取"},
		{"Grep", "已读取"},
		{"Glob", "已读取"},
		{"head", "已读取"},
		{"cat", "已读取"},
		{"unknown_tool", "已读取"},
	}

	for _, tc := range tests {
		t.Run(tc.toolName, func(t *testing.T) {
			t.Parallel()
			got := inferFileOperation(tc.toolName)
			if got != tc.want {
				t.Errorf("inferFileOperation(%q) = %q, want %q", tc.toolName, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// redactSensitiveData
// ---------------------------------------------------------------------------

func TestRedactSensitiveData(t *testing.T) {
	t.Parallel()

	t.Run("redacts API key", func(t *testing.T) {
		t.Parallel()
		input := `api_key=sk-abc123def456ghi789jkl012mno`
		got := redactSensitiveData(input)
		if strings.Contains(got, "sk-abc123def456ghi789jkl012mno") {
			t.Error("API key should be redacted")
		}
	})

	t.Run("redacts password", func(t *testing.T) {
		t.Parallel()
		input := `password=mysecret123 host=db.example.com`
		got := redactSensitiveData(input)
		if strings.Contains(got, "mysecret123") {
			t.Error("password should be redacted")
		}
	})

	t.Run("redacts bearer token", func(t *testing.T) {
		t.Parallel()
		input := `token=abc123tokenxyz`
		got := redactSensitiveData(input)
		if strings.Contains(got, "abc123tokenxyz") {
			t.Error("bearer token should be redacted")
		}
	})

	t.Run("redacts OpenAI-style key", func(t *testing.T) {
		t.Parallel()
		input := `key=sk-abcdefghijklmnopqrstuvwxyz123456`
		got := redactSensitiveData(input)
		if !strings.Contains(got, "[REDACTED]") {
			t.Error("OpenAI key should be redacted")
		}
	})

	t.Run("redacts JWT pattern", func(t *testing.T) {
		t.Parallel()
		input := `token=eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0`
		got := redactSensitiveData(input)
		if !strings.Contains(got, "[REDACTED]") {
			t.Error("JWT should be redacted")
		}
	})

	t.Run("non-sensitive data unchanged", func(t *testing.T) {
		t.Parallel()
		input := `{"command":"go test ./...","path":"main.go"}`
		got := redactSensitiveData(input)
		if got != input {
			t.Errorf("non-sensitive data should be unchanged, got %q", got)
		}
	})

	t.Run("redacts connection string", func(t *testing.T) {
		t.Parallel()
		input := `connection_string=postgres://user:pass@host/db`
		got := redactSensitiveData(input)
		if strings.Contains(got, "postgres://user:pass") {
			t.Error("connection string should be redacted")
		}
	})

	t.Run("empty input returns empty", func(t *testing.T) {
		t.Parallel()
		got := redactSensitiveData("")
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}

// ---------------------------------------------------------------------------
// truncateStr
// ---------------------------------------------------------------------------

func TestTruncateStr(t *testing.T) {
	t.Parallel()

	t.Run("short string unchanged", func(t *testing.T) {
		t.Parallel()
		got := truncateStr("hello", 10)
		if got != "hello" {
			t.Errorf("got %q, want %q", got, "hello")
		}
	})

	t.Run("long string truncated", func(t *testing.T) {
		t.Parallel()
		long := strings.Repeat("a", 200)
		got := truncateStr(long, 50)
		if len(got) > 53 {
			t.Errorf("truncated string too long: %d chars", len(got))
		}
		if !strings.HasSuffix(got, "...") {
			t.Errorf("truncated string should end with '...', got %q", got)
		}
	})

	t.Run("exact length not truncated", func(t *testing.T) {
		t.Parallel()
		s := "hello"
		got := truncateStr(s, 5)
		if got != s {
			t.Errorf("got %q, want %q", got, s)
		}
	})

	t.Run("newlines replaced with spaces", func(t *testing.T) {
		t.Parallel()
		got := truncateStr("line1\nline2\nline3", 50)
		if strings.Contains(got, "\n") {
			t.Error("newlines should be replaced with spaces")
		}
	})

	t.Run("trims whitespace", func(t *testing.T) {
		t.Parallel()
		got := truncateStr("  hello  ", 10)
		if got != "hello" {
			t.Errorf("got %q, want %q", got, "hello")
		}
	})
}

// ---------------------------------------------------------------------------
// buildSummaryPrompt
// ---------------------------------------------------------------------------

func TestBuildSummaryPrompt(t *testing.T) {
	t.Parallel()

	t.Run("first time summary includes template", func(t *testing.T) {
		t.Parallel()
		c := NewCompressor(3, 20000)
		got := c.buildSummaryPrompt("content here", "", 3000, "")
		if !strings.Contains(got, "消息统计") {
			t.Error("prompt should contain template section")
		}
		if !strings.Contains(got, "content here") {
			t.Error("prompt should contain content")
		}
	})

	t.Run("iterative update includes previous summary", func(t *testing.T) {
		t.Parallel()
		c := NewCompressor(3, 20000)
		got := c.buildSummaryPrompt("new content", "previous summary text", 3000, "")
		if !strings.Contains(got, "previous summary text") {
			t.Error("prompt should contain previous summary")
		}
		if !strings.Contains(got, "new content") {
			t.Error("prompt should contain new content")
		}
	})

	t.Run("focus topic included when provided", func(t *testing.T) {
		t.Parallel()
		c := NewCompressor(3, 20000)
		got := c.buildSummaryPrompt("content", "", 3000, "authentication")
		if !strings.Contains(got, "authentication") {
			t.Error("prompt should contain focus topic")
		}
	})

	t.Run("custom template used when set", func(t *testing.T) {
		t.Parallel()
		c := NewCompressor(3, 20000)
		c.SummaryTemplate = "## Custom Section\n[custom content]"
		got := c.buildSummaryPrompt("content", "", 3000, "")
		if !strings.Contains(got, "Custom Section") {
			t.Error("prompt should use custom template")
		}
		if strings.Contains(got, "消息统计") {
			t.Error("prompt should not contain default template when custom is set")
		}
	})
}
