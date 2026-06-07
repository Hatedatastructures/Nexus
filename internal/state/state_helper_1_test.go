package state

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNullStr(t *testing.T) {
	if got := nullStr(sql.NullString{Valid: true, String: "hello"}); got != "hello" {
		t.Errorf("nullStr(valid) = %q, want %q", got, "hello")
	}
	if got := nullStr(sql.NullString{Valid: false}); got != "" {
		t.Errorf("nullStr(invalid) = %q, want empty", got)
	}
}



func TestNullFloat(t *testing.T) {
	if got := nullFloat(sql.NullFloat64{Valid: true, Float64: 3.14}); got != 3.14 {
		t.Errorf("nullFloat(valid) = %v, want 3.14", got)
	}
	if got := nullFloat(sql.NullFloat64{Valid: false}); got != 0 {
		t.Errorf("nullFloat(invalid) = %v, want 0", got)
	}
}



func TestNullInt(t *testing.T) {
	if got := nullInt(sql.NullInt64{Valid: true, Int64: 42}); got != 42 {
		t.Errorf("nullInt(valid) = %v, want 42", got)
	}
	if got := nullInt(sql.NullInt64{Valid: false}); got != 0 {
		t.Errorf("nullInt(invalid) = %v, want 0", got)
	}
}



func TestNullStrOrNil(t *testing.T) {
	if got := nullStrOrNil(""); got != nil {
		t.Errorf("nullStrOrNil('') = %v, want nil", got)
	}
	if got := nullStrOrNil("abc"); got != "abc" {
		t.Errorf("nullStrOrNil('abc') = %v, want 'abc'", got)
	}
}



func TestNullIntOrNil(t *testing.T) {
	if got := nullIntOrNil(0); got != nil {
		t.Errorf("nullIntOrNil(0) = %v, want nil", got)
	}
	if got := nullIntOrNil(5); got != 5 {
		t.Errorf("nullIntOrNil(5) = %v, want 5", got)
	}
}



func TestJoinStrings(t *testing.T) {
	tests := []struct {
		parts []string
		sep   string
		want  string
	}{
		{[]string{}, ", ", ""},
		{[]string{"a"}, ", ", "a"},
		{[]string{"a", "b", "c"}, ", ", "a, b, c"},
		{[]string{"x", "y"}, "|", "x|y"},
	}
	for _, tt := range tests {
		got := joinStrings(tt.parts, tt.sep)
		if got != tt.want {
			t.Errorf("joinStrings(%v, %q) = %q, want %q", tt.parts, tt.sep, got, tt.want)
		}
	}
}



func TestEscapeLikePattern(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"abc", "abc"},
		{"100%", "100\\%"},
		{"a_b", "a\\_b"},
		{"a\\b", "a\\\\b"},
		{"a%b_c\\d", "a\\%b\\_c\\\\d"},
	}
	for _, tt := range tests {
		got := escapeLikePattern(tt.input)
		if got != tt.want {
			t.Errorf("escapeLikePattern(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ── CJK 检测测试 ──────────────────────────────────────────────



func TestIsCJKCodepoint(t *testing.T) {
	cjk := []rune{'中', '日', '韓', 'の', 'ハ', '가', '。', '一', '鿿'}
	for _, r := range cjk {
		if !isCJKCodepoint(r) {
			t.Errorf("isCJKCodepoint(%U) = false, want true", r)
		}
	}
	nonCJK := []rune{'A', 'z', '0', ' ', '.', 'é', 'À'}
	for _, r := range nonCJK {
		if isCJKCodepoint(r) {
			t.Errorf("isCJKCodepoint(%U) = true, want false", r)
		}
	}
}



func TestContainsCJK(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"hello world", false},
		{"你好世界", true},
		{"hello 你好", true},
		{"カタカナ", true},
		{"", false},
		{"123", false},
	}
	for _, tt := range tests {
		got := containsCJK(tt.input)
		if got != tt.want {
			t.Errorf("containsCJK(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}



func TestCountCJK(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"hello", 0},
		{"你好", 2},
		{"hello你好world", 2},
		{"日本語テスト", 6},
		{"", 0},
		{"a中b日c", 2},
	}
	for _, tt := range tests {
		got := countCJK(tt.input)
		if got != tt.want {
			t.Errorf("countCJK(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

// ── FTS5 查询清理测试 ─────────────────────────────────────────



func TestSanitizeFTS5Query(t *testing.T) {
	tests := []struct {
		name, input, want string
	}{
		{"simple", "hello world", "hello world"},
		{"special chars", "hello+world", "hello world"},
		{"quoted phrase", `"exact phrase"`, `"exact phrase"`},
		{"leading star", "*hello", "hello"},
		{"dangling AND", "hello AND", "hello"},
		{"dangling OR start", "OR hello", "hello"},
		{"dangling NOT end", "hello NOT", "hello"},
		{"parentheses", "hello (world)", "hello  world"},
		{"dot-separated", "v1.2.3", `"v1.2.3"`},
		{"dash-separated", "my-var", `"my-var"`},
		{"underscore", `my_var`, `"my_var"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeFTS5Query(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeFTS5Query(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}



func TestSanitizeFTS5Query_EmptyInput(t *testing.T) {
	got := sanitizeFTS5Query("")
	if got != "" {
		t.Errorf("sanitizeFTS5Query('') = %q, want empty", got)
	}
}

// ── Store CRUD 测试 ───────────────────────────────────────────

// newTestStore 创建一个使用临时目录的测试 Store


func TestParseSchemaColumns(t *testing.T) {
	columns, err := parseSchemaColumns(schemaSQL)
	if err != nil {
		t.Fatalf("parseSchemaColumns: %v", err)
	}

	// Check sessions table
	sessCols, ok := columns["sessions"]
	if !ok {
		t.Fatal("sessions table not found in parsed schema")
	}
	expectedCols := []string{"id", "source", "user_id", "model", "system_prompt", "started_at", "ended_at"}
	for _, col := range expectedCols {
		if _, ok := sessCols[col]; !ok {
			t.Errorf("sessions table missing column %q", col)
		}
	}

	// Check messages table
	msgCols, ok := columns["messages"]
	if !ok {
		t.Fatal("messages table not found in parsed schema")
	}
	if _, ok := msgCols["content"]; !ok {
		t.Error("messages table missing 'content' column")
	}
}



func TestSplitSQLStatements(t *testing.T) {
	tests := []struct {
		name, input string
		wantCount   int
	}{
		{"empty", "", 0},
		{"single", "SELECT 1", 1},
		{"multiple", "SELECT 1; SELECT 2;", 2},
		{"with comments", "-- comment\nSELECT 1;", 1},
		{"only comments", "-- just a comment", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stmts := splitSQLStatements(tt.input)
			if len(stmts) != tt.wantCount {
				t.Errorf("got %d statements, want %d", len(stmts), tt.wantCount)
			}
		})
	}
}



func TestIsLockedErr(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"database is locked", true},
		{"database is busy", true},
		{"SQLITE_BUSY", true},
		{"no such table: foo", false},
		{"", false},
	}
	for _, tt := range tests {
		got := isLockedErr(errors.New(tt.input))
		if got != tt.want {
			t.Errorf("isLockedErr(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}



func TestIsLockedErr_Nil(t *testing.T) {
	if isLockedErr(nil) {
		t.Error("isLockedErr(nil) should be false")
	}
}

// ── CreateFTSTables 测试 ──────────────────────────────────────



func TestSchemaSQL(t *testing.T) {
	sql := SchemaSQL()
	if sql == "" {
		t.Error("SchemaSQL() returned empty string")
	}
	if !strings.Contains(sql, "CREATE TABLE") {
		t.Error("SchemaSQL() should contain CREATE TABLE statements")
	}
}

// ── reconcileColumns 测试 ─────────────────────────────────────



func TestJitterSleep(t *testing.T) {
	for i := 0; i < 100; i++ {
		d := jitterSleep()
		if d < 20*time.Millisecond || d > 150*time.Millisecond {
			t.Errorf("jitterSleep() = %v, want [20ms, 150ms]", d)
		}
	}
}

// ── tryCheckpoint 测试 ────────────────────────────────────────



func TestSanitizeFTS5Query_SpecialChars(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello+world", "hello world"},
		{"test{ing}", "test ing"},
		{"(parenthetical)", "parenthetical"},
		{"\"exact phrase\"", "\"exact phrase\""},
		{"hello   world", "hello   world"},
		{"AND something", "something"},
		{"something OR", "something"},
		{"NOT alone", "alone"},
		{"***test", "test"},
		{"my-api_key.code", "\"my-api_key.code\""},
	}
	for _, tt := range tests {
		got := sanitizeFTS5Query(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeFTS5Query(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ── CJK 检测补充测试 ───────────────────────────────────────────



func TestContainsCJK_Mixed(t *testing.T) {
	if !containsCJK("hello世界") {
		t.Error("expected CJK detection in mixed text")
	}
	if containsCJK("hello world") {
		t.Error("expected no CJK in pure Latin text")
	}
}



func TestSanitizeFTS5Query_EmptyAfterSanitize(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// Only special chars should result in empty sanitized query
	results, err := store.SearchMessages(ctx, "+++===!!!", 10)
	if err != nil {
		t.Fatalf("SearchMessages empty sanitize: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for sanitized-to-empty query, got %d", len(results))
	}
}



func TestSplitSQLStatements_Simple(t *testing.T) {
	input := "SELECT 1;\nSELECT 2;"
	stmts := splitSQLStatements(input)
	if len(stmts) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(stmts))
	}
	if stmts[0] != "SELECT 1" {
		t.Errorf("stmt 0: got %q", stmts[0])
	}
	if stmts[1] != "SELECT 2" {
		t.Errorf("stmt 1: got %q", stmts[1])
	}
}
