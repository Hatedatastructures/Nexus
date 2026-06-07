package state

import (
	"strings"
	"testing"
	"time"
)

func TestSplitSQLStatements_TriggerWithSemicolons(t *testing.T) {
	input := `CREATE TRIGGER t AFTER INSERT ON x BEGIN
		INSERT INTO y VALUES (1);
		INSERT INTO y VALUES (2);
	END;
	SELECT 1;`
	stmts := splitSQLStatements(input)
	if len(stmts) != 2 {
		t.Fatalf("expected 2 statements, got %d: %v", len(stmts), stmts)
	}
	if !strings.HasPrefix(stmts[0], "CREATE TRIGGER") {
		t.Errorf("first stmt should be trigger, got %q", stmts[0])
	}
}



func TestSplitSQLStatements_EmptyInput(t *testing.T) {
	stmts := splitSQLStatements("")
	if len(stmts) != 0 {
		t.Errorf("expected 0 statements, got %d", len(stmts))
	}
}



func TestSplitSQLStatements_CommentsOnly(t *testing.T) {
	input := "-- just a comment\n-- another comment"
	stmts := splitSQLStatements(input)
	if len(stmts) != 0 {
		t.Errorf("expected 0 statements from comments, got %d", len(stmts))
	}
}



func TestSplitSQLStatements_NoTrailingSemicolon(t *testing.T) {
	input := "SELECT 1"
	stmts := splitSQLStatements(input)
	if len(stmts) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(stmts))
	}
	if stmts[0] != "SELECT 1" {
		t.Errorf("got %q", stmts[0])
	}
}



func TestIsCommentOnly(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"-- hello", true},
		{"  -- hello  ", true},
		{"SELECT 1", false},
		{"", true},
		{"\n\n", true},
		{"-- a\n-- b", true},
		{"-- a\nSELECT 1", false},
	}
	for _, tt := range tests {
		got := isCommentOnly(tt.input)
		if got != tt.want {
			t.Errorf("isCommentOnly(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}



// TestSplitSQLStatements_SingleQuotes 测试单引号字符串内的分号不拆分
func TestSplitSQLStatements_SingleQuotes(t *testing.T) {
	input := `INSERT INTO t VALUES ('hello;world'); SELECT 1;`
	stmts := splitSQLStatements(input)
	if len(stmts) != 2 {
		t.Fatalf("expected 2 statements, got %d: %v", len(stmts), stmts)
	}
	if !strings.Contains(stmts[0], "hello;world") {
		t.Errorf("first statement should contain semicolon in string: %q", stmts[0])
	}
}

// TestSplitSQLStatements_DoubleQuotes 测试双引号标识符内的分号不拆分


// TestSplitSQLStatements_DoubleQuotes 测试双引号标识符内的分号不拆分
func TestSplitSQLStatements_DoubleQuotes(t *testing.T) {
	input := `CREATE TABLE "my;table" (id INTEGER); SELECT 1;`
	stmts := splitSQLStatements(input)
	if len(stmts) != 2 {
		t.Fatalf("expected 2 statements, got %d: %v", len(stmts), stmts)
	}
}

// TestSplitSQLStatements_EscapedSingleQuotes 测试转义单引号


// TestSplitSQLStatements_EscapedSingleQuotes 测试转义单引号
func TestSplitSQLStatements_EscapedSingleQuotes(t *testing.T) {
	input := `INSERT INTO t VALUES ('it''s a test'); SELECT 2;`
	stmts := splitSQLStatements(input)
	if len(stmts) != 2 {
		t.Fatalf("expected 2 statements, got %d: %v", len(stmts), stmts)
	}
}

// TestSetSchemaVersion_InsertPath 测试 setSchemaVersion 的 INSERT 路径


// TestParseSchemaColumns_InvalidStatement 测试 parseSchemaColumns 跳过无效 SQL
func TestParseSchemaColumns_InvalidStatement(t *testing.T) {
	// Include an invalid statement — should be skipped, not panic
	schema := `
		CREATE TABLE valid (id INTEGER PRIMARY KEY, name TEXT);
		INVALID SQL STATEMENT HERE;
		CREATE TABLE also_valid (id INTEGER PRIMARY KEY);
	`
	columns, err := parseSchemaColumns(schema)
	if err != nil {
		t.Fatalf("parseSchemaColumns with invalid stmt: %v", err)
	}
	if _, ok := columns["valid"]; !ok {
		t.Error("valid table should be parsed")
	}
	if _, ok := columns["also_valid"]; !ok {
		t.Error("also_valid table should be parsed")
	}
}

// TestSearchMessages_NegativeLimit 测试负数 limit 默认为 20


// TestParseSchemaColumns_Error 测试 parseSchemaColumns 对无效 SQL 的容错
func TestParseSchemaColumns_Error(t *testing.T) {
	// 传入包含无效 SQL 的 schema — 应跳过错误语句
	cols, err := parseSchemaColumns("CREATE TABLE t1 (id INTEGER PRIMARY KEY); INVALID SQL;")
	if err != nil {
		// parseSchemaColumns 可能返回错误也可能不返回，取决于失败模式
		t.Logf("parseSchemaColumns returned error (acceptable): %v", err)
		return
	}
	// 如果没有错误，检查有效表被解析
	if _, ok := cols["t1"]; !ok {
		t.Error("expected t1 in parsed columns")
	}
}

// TestReconcileColumns_MissingTable 测试 reconcileColumns 处理不存在的表


// TestParseSchemaColumns_Valid 测试 parseSchemaColumns 正常解析
func TestParseSchemaColumns_Valid(t *testing.T) {
	schemaText := SchemaSQL()
	cols, err := parseSchemaColumns(schemaText)
	if err != nil {
		t.Fatalf("parseSchemaColumns: %v", err)
	}
	if len(cols) == 0 {
		t.Error("expected non-empty column map")
	}

	// sessions 表应该有 id 列
	sessCols, ok := cols["sessions"]
	if !ok {
		t.Fatal("sessions table should be in column map")
	}
	if _, hasID := sessCols["id"]; !hasID {
		t.Error("sessions table should have 'id' column")
	}
}

// ── splitSQLStatements 边界覆盖 ───────────────────────────

// TestSplitSQLStatements_Empty 测试空输入


// TestSplitSQLStatements_Empty 测试空输入
func TestSplitSQLStatements_Empty(t *testing.T) {
	result := splitSQLStatements("")
	if len(result) != 0 {
		t.Errorf("expected 0 statements from empty input, got %d", len(result))
	}
}

// ── searchTrigramFTS / searchCJKLike 边界覆盖 ──────────────

// TestSearchMessages_ChineseShortQuery 测试短中文查询走 LIKE 路径


func TestParseSchemaColumns_AlterTable(t *testing.T) {
	cols, err := parseSchemaColumns("ALTER TABLE foo ADD COLUMN bar TEXT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cols) != 0 {
		t.Errorf("ALTER TABLE should return 0 columns, got %d", len(cols))
	}
}



func TestParseSchemaColumns_ReturnsExpectedTables(t *testing.T) {
	cols, err := parseSchemaColumns(schemaSQL)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"sessions", "messages", "schema_version"} {
		if _, ok := cols[expected]; !ok {
			t.Errorf("expected table %s not found in parsed schema", expected)
		}
	}
	if sessCols, ok := cols["sessions"]; ok {
		for _, col := range []string{"id", "source", "started_at", "title"} {
			if _, ok := sessCols[col]; !ok {
				t.Errorf("sessions table missing expected column: %s", col)
			}
		}
	}
}



func TestParseSchemaColumns_ValidSchema(t *testing.T) {
	cols, err := parseSchemaColumns(`CREATE TABLE messages (id TEXT PRIMARY KEY, session_id TEXT, role TEXT, content TEXT)`)
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) == 0 {
		t.Fatal("expected at least one table in schema columns")
	}
}



func TestSplitSQLStatements_TrailingStmt(t *testing.T) {
	input := "SELECT 1; SELECT 2; -- comment"
	stmts := splitSQLStatements(input)
	if len(stmts) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(stmts))
	}
}



func TestIsCommentOnly_EdgeCases(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"", true},
		{"   ", true},
		{"-- just a comment", true},
		{"-- comment\n-- another", true},
		{"-- comment\nSELECT 1", false},
	}
	for _, tc := range cases {
		got := isCommentOnly(tc.input)
		if got != tc.want {
			t.Errorf("isCommentOnly(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}




func TestJitterSleepRange(t *testing.T) {
	for i := 0; i < 100; i++ {
		d := jitterSleep()
		if d < 20*time.Millisecond || d > 150*time.Millisecond {
			t.Fatalf("jitterSleep %v out of range [20ms, 150ms]", d)
		}
	}
}



func TestIsLockedErr_NilInput(t *testing.T) {
	if isLockedErr(nil) {
		t.Error("nil error should not be locked")
	}
}



func TestIsCJKCodepoint_Ranges(t *testing.T) {
	cases := []struct {
		r   rune
		want bool
	}{
		{'a', false},
		{0x4E00, true},
		{0x9FFF, true},
		{0x3400, true},
		{0x3000, true},
		{0x303F, true},
		{0xAC00, true},
		{0xD7AF, true},
		{0x3040, true},
		{0x309F, true},
		{0x30A0, true},
		{0x30FF, true},
	}
	for _, tc := range cases {
		got := isCJKCodepoint(tc.r)
		if got != tc.want {
			t.Errorf("isCJKCodepoint(%U) = %v, want %v", tc.r, got, tc.want)
		}
	}
}



// ── Coverage Gap Tests (Round 15) ─────────────────────────────
