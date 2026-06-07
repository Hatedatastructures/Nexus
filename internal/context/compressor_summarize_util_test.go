package context

import (
	"strings"
	"testing"
)

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
