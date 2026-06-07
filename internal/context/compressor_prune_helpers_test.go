package context

import (
	"strings"
	"testing"
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
