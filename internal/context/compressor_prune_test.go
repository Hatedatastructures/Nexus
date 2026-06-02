package context

import (
	"strings"
	"testing"

	"nexus-agent/internal/llm"
)

// ---------------------------------------------------------------------------
// parseArgs
// ---------------------------------------------------------------------------

func TestParseArgs(t *testing.T) {
	t.Parallel()

	t.Run("empty string returns empty map", func(t *testing.T) {
		t.Parallel()
		m := parseArgs("")
		if len(m) != 0 {
			t.Errorf("parseArgs(\"\") = %d entries, want 0", len(m))
		}
	})

	t.Run("invalid JSON returns empty map", func(t *testing.T) {
		t.Parallel()
		m := parseArgs("not json")
		if len(m) != 0 {
			t.Errorf("parseArgs(\"not json\") = %d entries, want 0", len(m))
		}
	})

	t.Run("valid JSON returns parsed map", func(t *testing.T) {
		t.Parallel()
		m := parseArgs(`{"path": "/tmp/file.go", "count": 5}`)
		if len(m) != 2 {
			t.Fatalf("parseArgs returned %d entries, want 2", len(m))
		}
		if m["path"] != "/tmp/file.go" {
			t.Errorf("m[\"path\"] = %v, want /tmp/file.go", m["path"])
		}
	})
}

// ---------------------------------------------------------------------------
// argStr
// ---------------------------------------------------------------------------

func TestArgStr(t *testing.T) {
	t.Parallel()

	m := map[string]any{
		"name":   "hello",
		"count":  42,
		"empty":  "",
		"nilVal": nil,
	}

	tests := []struct {
		key  string
		want string
	}{
		{"name", "hello"},
		{"count", "42"},
		{"empty", "?"},
		{"nilVal", "<nil>"},
		{"missing", "?"},
	}

	for _, tc := range tests {
		t.Run(tc.key, func(t *testing.T) {
			t.Parallel()
			got := argStr(m, tc.key)
			if got != tc.want {
				t.Errorf("argStr(%q) = %q, want %q", tc.key, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// argInt
// ---------------------------------------------------------------------------

func TestArgInt(t *testing.T) {
	t.Parallel()

	m := map[string]any{
		"f64":   float64(10),
		"int":   20,
		"int64": int64(30),
		"str":   "not a number",
	}

	tests := []struct {
		key  string
		def  int
		want int
	}{
		{"f64", 0, 10},
		{"int", 0, 20},
		{"int64", 0, 30},
		{"str", 99, 99},
		{"missing", 99, 99},
	}

	for _, tc := range tests {
		t.Run(tc.key, func(t *testing.T) {
			t.Parallel()
			got := argInt(m, tc.key, tc.def)
			if got != tc.want {
				t.Errorf("argInt(%q, %d) = %d, want %d", tc.key, tc.def, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// extractJSONInt
// ---------------------------------------------------------------------------

func TestExtractJSONInt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		content string
		key     string
		want    int
	}{
		{`{"exit_code": 0}`, "exit_code", 0},
		{`{"exit_code": 127}`, "exit_code", 127},
		{`{"total_count": 42}`, "total_count", 42},
		{`{"other": 1}`, "missing_key", -1},
		{`not json`, "exit_code", -1},
		{`{"exit_code": -1}`, "exit_code", -1},
	}

	for _, tc := range tests {
		t.Run(tc.key+"_"+tc.content, func(t *testing.T) {
			t.Parallel()
			got := extractJSONInt(tc.content, tc.key)
			if got != tc.want {
				t.Errorf("extractJSONInt(%q, %q) = %d, want %d", tc.content, tc.key, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// truncateJSONValues
// ---------------------------------------------------------------------------

func TestTruncateJSONValues(t *testing.T) {
	t.Parallel()

	t.Run("short string unchanged", func(t *testing.T) {
		t.Parallel()
		got := truncateJSONValues("hello", 100)
		if got != "hello" {
			t.Errorf("got %v, want hello", got)
		}
	})

	t.Run("long string truncated", func(t *testing.T) {
		t.Parallel()
		long := strings.Repeat("a", 200)
		got := truncateJSONValues(long, 50)
		s, ok := got.(string)
		if !ok {
			t.Fatal("expected string")
		}
		if !strings.HasSuffix(s, "...[truncated]") {
			t.Errorf("truncated string missing suffix, got %q", s)
		}
	})

	t.Run("map values truncated recursively", func(t *testing.T) {
		t.Parallel()
		m := map[string]any{
			"short": "ok",
			"long":  strings.Repeat("x", 200),
		}
		got := truncateJSONValues(m, 50).(map[string]any)
		if got["short"] != "ok" {
			t.Error("short value should be unchanged")
		}
		longVal := got["long"].(string)
		if !strings.HasSuffix(longVal, "...[truncated]") {
			t.Errorf("long map value not truncated")
		}
	})

	t.Run("slice values truncated", func(t *testing.T) {
		t.Parallel()
		s := []any{"short", strings.Repeat("b", 200)}
		got := truncateJSONValues(s, 50).([]any)
		if got[0] != "short" {
			t.Error("first element should be unchanged")
		}
		if !strings.HasSuffix(got[1].(string), "...[truncated]") {
			t.Errorf("second element not truncated")
		}
	})

	t.Run("non-string non-collection passed through", func(t *testing.T) {
		t.Parallel()
		if truncateJSONValues(42, 10) != 42 {
			t.Error("int should pass through")
		}
		if truncateJSONValues(true, 10) != true {
			t.Error("bool should pass through")
		}
	})
}

// ---------------------------------------------------------------------------
// truncateToolCallArgsJSON
// ---------------------------------------------------------------------------

func TestTruncateToolCallArgsJSON(t *testing.T) {
	t.Parallel()

	t.Run("short JSON unchanged", func(t *testing.T) {
		t.Parallel()
		input := `{"path":"/tmp"}`
		got := truncateToolCallArgsJSON(input, 500)
		if got != input {
			t.Errorf("short JSON should be unchanged, got %q", got)
		}
	})

	t.Run("invalid JSON byte truncation", func(t *testing.T) {
		t.Parallel()
		long := strings.Repeat("x", 600)
		got := truncateToolCallArgsJSON(long, 500)
		if !strings.HasSuffix(got, "...[truncated]") {
			t.Errorf("invalid JSON should be byte-truncated, got %q", got)
		}
	})

	t.Run("long JSON values truncated", func(t *testing.T) {
		t.Parallel()
		longVal := strings.Repeat("a", 800)
		input := `{"content":"` + longVal + `"}`
		got := truncateToolCallArgsJSON(input, 500)
		if len(got) >= len(input) {
			t.Errorf("long JSON should be shorter, got len %d >= %d", len(got), len(input))
		}
	})
}

// ---------------------------------------------------------------------------
// summarizeToolResult
// ---------------------------------------------------------------------------

func TestSummarizeToolResult(t *testing.T) {
	t.Parallel()

	t.Run("terminal", func(t *testing.T) {
		t.Parallel()
		got := summarizeToolResult("terminal", `{"command":"npm test"}`, "line1\nline2\nexit_code: 0")
		if !strings.Contains(got, "[terminal]") {
			t.Errorf("terminal summary missing [terminal] prefix: %q", got)
		}
		if !strings.Contains(got, "npm test") {
			t.Errorf("terminal summary missing command: %q", got)
		}
	})

	t.Run("terminal long command truncated", func(t *testing.T) {
		t.Parallel()
		longCmd := strings.Repeat("x", 100)
		got := summarizeToolResult("terminal", `{"command":"`+longCmd+`"}`, "ok")
		if strings.Contains(got, longCmd) {
			t.Error("long command should be truncated")
		}
	})

	t.Run("terminal missing command", func(t *testing.T) {
		t.Parallel()
		got := summarizeToolResult("terminal", `{"command":""}`, "output")
		if !strings.Contains(got, "?") {
			t.Errorf("missing command should show '?': %q", got)
		}
	})

	t.Run("read_file", func(t *testing.T) {
		t.Parallel()
		got := summarizeToolResult("read_file", `{"path":"config.go","offset":10}`, "content here")
		if !strings.Contains(got, "[read_file]") {
			t.Errorf("missing [read_file] prefix: %q", got)
		}
		if !strings.Contains(got, "config.go") {
			t.Errorf("missing file path: %q", got)
		}
	})

	t.Run("write_file", func(t *testing.T) {
		t.Parallel()
		got := summarizeToolResult("write_file", `{"path":"out.go"}`, "wrote 500 chars")
		if !strings.Contains(got, "out.go") {
			t.Errorf("write_file missing path: %q", got)
		}
	})

	t.Run("search_files with target", func(t *testing.T) {
		t.Parallel()
		got := summarizeToolResult("search_files", `{"pattern":"TODO","path":".","target":"filename"}`, `{"total_count": 5}`)
		if !strings.Contains(got, "filename") {
			t.Errorf("search_files missing target: %q", got)
		}
	})

	t.Run("search_files without target defaults to content", func(t *testing.T) {
		t.Parallel()
		got := summarizeToolResult("search_files", `{"pattern":"TODO","path":"."}`, `{"total_count": 3}`)
		if !strings.Contains(got, "content") {
			t.Errorf("search_files should default to 'content': %q", got)
		}
	})

	t.Run("patch with mode", func(t *testing.T) {
		t.Parallel()
		got := summarizeToolResult("patch", `{"path":"main.go","mode":"insert"}`, "ok")
		if !strings.Contains(got, "insert") {
			t.Errorf("patch missing mode: %q", got)
		}
	})

	t.Run("patch without mode defaults to replace", func(t *testing.T) {
		t.Parallel()
		got := summarizeToolResult("patch", `{"path":"main.go"}`, "ok")
		if !strings.Contains(got, "replace") {
			t.Errorf("patch should default to 'replace': %q", got)
		}
	})

	t.Run("web_search", func(t *testing.T) {
		t.Parallel()
		got := summarizeToolResult("web_search", `{"query":"golang testing"}`, "results")
		if !strings.Contains(got, "golang testing") {
			t.Errorf("web_search missing query: %q", got)
		}
	})

	t.Run("web_extract with urls", func(t *testing.T) {
		t.Parallel()
		got := summarizeToolResult("web_extract", `{"urls":["https://example.com"]}`, "content")
		if !strings.Contains(got, "example.com") {
			t.Errorf("web_extract missing url: %q", got)
		}
	})

	t.Run("web_extract without urls", func(t *testing.T) {
		t.Parallel()
		got := summarizeToolResult("web_extract", `{}`, "content")
		if !strings.Contains(got, "?") {
			t.Errorf("web_extract without urls should show '?': %q", got)
		}
	})

	t.Run("delegate_task", func(t *testing.T) {
		t.Parallel()
		got := summarizeToolResult("delegate_task", `{"goal":"refactor auth module"}`, "done")
		if !strings.Contains(got, "refactor auth module") {
			t.Errorf("delegate_task missing goal: %q", got)
		}
	})

	t.Run("delegate_task long goal truncated", func(t *testing.T) {
		t.Parallel()
		longGoal := strings.Repeat("g", 80)
		got := summarizeToolResult("delegate_task", `{"goal":"`+longGoal+`"}`, "done")
		if strings.Contains(got, longGoal) {
			t.Error("long goal should be truncated")
		}
	})

	t.Run("execute_code", func(t *testing.T) {
		t.Parallel()
		got := summarizeToolResult("execute_code", `{"code":"fmt.Println('hi')"}`, "output\nline2")
		if !strings.Contains(got, "fmt.Println") {
			t.Errorf("execute_code missing code preview: %q", got)
		}
	})

	t.Run("todo", func(t *testing.T) {
		t.Parallel()
		got := summarizeToolResult("todo", `{}`, "updated list")
		if !strings.Contains(got, "[todo]") {
			t.Errorf("todo missing prefix: %q", got)
		}
	})

	t.Run("memory", func(t *testing.T) {
		t.Parallel()
		got := summarizeToolResult("memory", `{"action":"save","target":"project"}`, "ok")
		if !strings.Contains(got, "save") || !strings.Contains(got, "project") {
			t.Errorf("memory missing action/target: %q", got)
		}
	})

	t.Run("default unknown tool", func(t *testing.T) {
		t.Parallel()
		got := summarizeToolResult("custom_tool", `{"key1":"val1","key2":"val2"}`, "result")
		if !strings.Contains(got, "[custom_tool]") {
			t.Errorf("default missing tool name: %q", got)
		}
	})

	t.Run("empty content zero lines", func(t *testing.T) {
		t.Parallel()
		got := summarizeToolResult("terminal", `{"command":"ls"}`, "   ")
		if !strings.Contains(got, "0 lines") {
			t.Errorf("empty content should show 0 lines: %q", got)
		}
	})
}

// ---------------------------------------------------------------------------
// calcPruneBoundary
// ---------------------------------------------------------------------------

func TestCalcPruneBoundary(t *testing.T) {
	t.Parallel()
	c := NewCompressor(3, 20000)

	t.Run("token budget mode", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleUser, Content: strings.Repeat("a", 4000)},
			{Role: llm.RoleAssistant, Content: strings.Repeat("b", 4000)},
			{Role: llm.RoleTool, Content: strings.Repeat("c", 4000)},
			{Role: llm.RoleUser, Content: strings.Repeat("d", 4000)},
		}
		// protectTailTokens=2000, protectTailCount=1
		// Each message ~1000 tokens + 10 = 1010 tokens
		// From the tail: msg[3] = 1010 tokens, total=1010 < 2000, continue
		// msg[2] = 1010 tokens, total=2020 > 2000 and we have 2 msgs (>= minProtect=1) -> stop
		boundary := c.calcPruneBoundary(msgs, 1, 2000)
		if boundary < 0 || boundary > len(msgs) {
			t.Errorf("boundary = %d, want in [0, %d]", boundary, len(msgs))
		}
	})

	t.Run("simple count mode", func(t *testing.T) {
		t.Parallel()
		msgs := make([]llm.Message, 10)
		for i := range msgs {
			msgs[i] = llm.Message{Role: llm.RoleUser, Content: "hi"}
		}
		// protectTailCount=3, total=10 -> boundary = 10-3 = 7
		boundary := c.calcPruneBoundary(msgs, 3, 0)
		if boundary != 7 {
			t.Errorf("boundary = %d, want 7", boundary)
		}
	})

	t.Run("count exceeds message length", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleUser, Content: "hi"},
			{Role: llm.RoleAssistant, Content: "hello"},
		}
		// protectTailCount=5 >= len(msgs)=2 -> boundary = 0
		boundary := c.calcPruneBoundary(msgs, 5, 0)
		if boundary != 0 {
			t.Errorf("boundary = %d, want 0", boundary)
		}
	})

	t.Run("token budget too large uses count fallback", func(t *testing.T) {
		t.Parallel()
		msgs := []llm.Message{
			{Role: llm.RoleUser, Content: "hi"},
			{Role: llm.RoleAssistant, Content: "hello"},
		}
		// protectTailTokens=1000000 >= 1000000 -> falls through to count mode
		boundary := c.calcPruneBoundary(msgs, 1, 1000000)
		if boundary != 1 {
			t.Errorf("boundary = %d, want 1", boundary)
		}
	})
}

// ---------------------------------------------------------------------------
// pruneOldToolResults
// ---------------------------------------------------------------------------

func TestPruneOldToolResults(t *testing.T) {
	t.Parallel()
	c := NewCompressor(3, 20000)

	t.Run("empty messages returns empty", func(t *testing.T) {
		t.Parallel()
		msgs, count := c.pruneOldToolResults(nil, 0, 0)
		if count != 0 {
			t.Errorf("count = %d, want 0", count)
		}
		if len(msgs) != 0 {
			t.Errorf("len(msgs) = %d, want 0", len(msgs))
		}
	})

	t.Run("deduplicates large identical tool results", func(t *testing.T) {
		t.Parallel()
		longContent := strings.Repeat("same output ", 100) // >500 chars
		msgs := []llm.Message{
			{Role: llm.RoleAssistant, Content: "", ToolCalls: []llm.ToolCall{
				{ID: "call_1", Name: "terminal", Arguments: `{"command":"ls"}`},
			}},
			{Role: llm.RoleTool, Content: longContent, ToolCallID: "call_1"},
			{Role: llm.RoleAssistant, Content: "", ToolCalls: []llm.ToolCall{
				{ID: "call_2", Name: "terminal", Arguments: `{"command":"ls"}`},
			}},
			{Role: llm.RoleTool, Content: longContent, ToolCallID: "call_2"},
		}
		result, count := c.pruneOldToolResults(msgs, 1, 0)
		if count < 1 {
			t.Errorf("expected at least 1 prune, got %d", count)
		}
		// The older result (lower index) should be deduplicated
		foundDedup := false
		for _, m := range result {
			if strings.Contains(m.Content, "重复工具输出") {
				foundDedup = true
				break
			}
		}
		if !foundDedup {
			t.Error("expected deduplication marker in result")
		}
	})

	t.Run("summarizes old tool results over 200 chars", func(t *testing.T) {
		t.Parallel()
		longResult := strings.Repeat("output line\n", 50) // >200 chars
		msgs := []llm.Message{
			{Role: llm.RoleAssistant, Content: "", ToolCalls: []llm.ToolCall{
				{ID: "call_1", Name: "terminal", Arguments: `{"command":"npm test"}`},
			}},
			{Role: llm.RoleTool, Content: longResult, ToolCallID: "call_1"},
			{Role: llm.RoleUser, Content: "protected"},
		}
		result, count := c.pruneOldToolResults(msgs, 1, 0)
		if count < 1 {
			t.Errorf("expected at least 1 prune, got %d", count)
		}
		// Tool result should be replaced with summary
		toolMsg := result[1]
		if toolMsg.Role != llm.RoleTool {
			t.Fatal("expected tool message at index 1")
		}
		if toolMsg.Content == longResult {
			t.Error("old tool result should have been summarized")
		}
	})

	t.Run("short tool results under 200 chars not summarized", func(t *testing.T) {
		t.Parallel()
		shortResult := "short output"
		msgs := []llm.Message{
			{Role: llm.RoleAssistant, Content: "", ToolCalls: []llm.ToolCall{
				{ID: "call_1", Name: "terminal", Arguments: `{"command":"ls"}`},
			}},
			{Role: llm.RoleTool, Content: shortResult, ToolCallID: "call_1"},
			{Role: llm.RoleUser, Content: "protected"},
		}
		_, count := c.pruneOldToolResults(msgs, 1, 0)
		if count != 0 {
			t.Errorf("short result should not be pruned, got count=%d", count)
		}
	})

	t.Run("truncates large tool call arguments", func(t *testing.T) {
		t.Parallel()
		longArgs := `{"command":"` + strings.Repeat("echo ", 200) + `"}`
		msgs := []llm.Message{
			{Role: llm.RoleAssistant, Content: "", ToolCalls: []llm.ToolCall{
				{ID: "call_1", Name: "terminal", Arguments: longArgs},
			}},
			{Role: llm.RoleTool, Content: "done", ToolCallID: "call_1"},
			{Role: llm.RoleUser, Content: "protected"},
		}
		result, _ := c.pruneOldToolResults(msgs, 1, 0)
		if len(result[0].ToolCalls) == 0 {
			t.Fatal("expected tool calls in assistant message")
		}
		if len(result[0].ToolCalls[0].Arguments) >= len(longArgs) {
			t.Errorf("tool call arguments should be truncated: got len %d, original %d",
				len(result[0].ToolCalls[0].Arguments), len(longArgs))
		}
	})

	t.Run("protects tail messages from pruning", func(t *testing.T) {
		t.Parallel()
		longResult := strings.Repeat("output\n", 100)
		msgs := []llm.Message{
			{Role: llm.RoleAssistant, Content: "", ToolCalls: []llm.ToolCall{
				{ID: "call_1", Name: "terminal", Arguments: `{"command":"ls"}`},
			}},
			{Role: llm.RoleTool, Content: longResult, ToolCallID: "call_1"},
			{Role: llm.RoleAssistant, Content: "", ToolCalls: []llm.ToolCall{
				{ID: "call_2", Name: "terminal", Arguments: `{"command":"ls -la"}`},
			}},
			{Role: llm.RoleTool, Content: longResult, ToolCallID: "call_2"},
		}
		// protectTailCount=2 protects the last 2 messages (call_2 and its result)
		result, count := c.pruneOldToolResults(msgs, 2, 0)
		if count < 1 {
			t.Errorf("expected at least 1 prune, got %d", count)
		}
		// Last tool result should be unchanged (protected)
		lastToolMsg := result[len(result)-1]
		if lastToolMsg.Content != longResult {
			t.Error("last tool result should be protected from pruning")
		}
	})
}
