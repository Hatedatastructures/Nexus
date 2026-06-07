// Package state 数据库迁移框架。
//
// 迁移策略混合了版本门控和声明式列调和:
//   - 版本门控迁移: 处理无法声明式表达的数据迁移（行回填、索引变更）
//   - 声明式列调和: reconcileColumns() 将 SCHEMA_SQL 与实際表结构对比，
//     自动通过 ALTER TABLE ADD COLUMN 添加缺失列
//
// 当前目标版本: v11

package state

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"log/slog"
	pkgerrors "nexus-agent/internal/errors"
	"strings"
)

// ── 嵌入 ────────────────────────────────────────────────────

//go:embed schema.sql
var schemaSQL string

// SchemaSQL 返回完整 SQL 模式定义（供外部使用）
func SchemaSQL() string {
	return schemaSQL
}

// ── 常量 ────────────────────────────────────────────────────

// 当前模式版本
const currentSchemaVersion = 11

// ── 迁移入口 ────────────────────────────────────────────────

// RunMigrations 运行所有必要的数据库迁移，将模式升级到当前版本。
//
// 流程:
//  1. 创建 schema_version 表（如果不存在）
//  2. 执行声明式列调和（对比 SCHEMA_SQL 与实際表，添加缺失列）
//  3. 运行版本门控数据迁移（v10, v11 等）
//  4. 更新 schema_version 到当前版本
func RunMigrations(ctx context.Context, db *sql.DB) error {
	// 步骤 1: 基础模式
	if err := execSchemaStatements(ctx, db, schemaSQL); err != nil {
		return pkgerrors.Wrap(pkgerrors.FileIO, "初始化基础模式失败", err)
	}

	// 步骤 2: 声明式列调和
	if err := reconcileColumns(ctx, db); err != nil {
		return pkgerrors.Wrap(pkgerrors.FileIO, "列调和失败", err)
	}

	// 步骤 3: 版本门控数据迁移
	dbVersion, err := getSchemaVersion(ctx, db)
	if err != nil {
		return pkgerrors.Wrap(pkgerrors.FileIO, "读取模式版本失败", err)
	}

	if dbVersion < currentSchemaVersion {
		slog.Info("starting version-gated migration",
			"from_version", dbVersion,
			"to_version", currentSchemaVersion,
		)
		if err := runVersionMigrations(ctx, db, dbVersion); err != nil {
			return pkgerrors.Wrap(pkgerrors.FileIO, "版本迁移失败", err)
		}
	}

	// 步骤 4: 确保 FTS 表和触发器存在
	if err := ensureFTS(ctx, db); err != nil {
		return pkgerrors.Wrap(pkgerrors.FileIO, "FTS 设置失败", err)
	}

	// 步骤 5: 确保唯一标题索引存在
	if err := ensureTitleIndex(ctx, db); err != nil {
		slog.Warn("failed to create unique title index", "error", err)
	}

	// 步骤 6: 更新版本号
	if err := setSchemaVersion(ctx, db, currentSchemaVersion); err != nil {
		return pkgerrors.Wrap(pkgerrors.FileIO, "更新模式版本失败", err)
	}

	slog.Info("database migration completed", "version", currentSchemaVersion)
	return nil
}

// ── 版本门控迁移 ────────────────────────────────────────────

// runVersionMigrations 按版本顺序执行数据迁移
func runVersionMigrations(ctx context.Context, db *sql.DB, fromVersion int) error {
	// v10: trigram FTS5 表
	if fromVersion < 10 {
		if err := migrateV10(ctx, db); err != nil {
			return pkgerrors.Wrap(pkgerrors.FileIO, "v10 迁移失败", err)
		}
	}

	// v11: 重建 FTS5 表以包含 tool_name + tool_calls 的内联索引
	if fromVersion < 11 {
		if err := migrateV11(ctx, db); err != nil {
			return pkgerrors.Wrap(pkgerrors.FileIO, "v11 迁移失败", err)
		}
	}

	return nil
}

// ── 声明式列调和 ────────────────────────────────────────────

// parseSchemaColumns 解析 SCHEMA_SQL 中每个表的预期列定义。
//
// 使用内存 SQLite 数据库来解析 SQL —— SQLite 本身处理所有语法
// (DEFAULT 表达式、内联 REFERENCES、CHECK 约束等)，所以零正则边缘情况。
// 打开内存 DB，执行 DDL，PRAGMA table_info 提取列元数据。
func parseSchemaColumns(schema string) (map[string]map[string]string, error) {
	ref, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.FileIO, "无法打开内存数据库解析Schema", err)
	}
	defer func() { _ = ref.Close() }()

	// :memory: 每个连接是独立的数据库，必须限制为单连接
	ref.SetMaxOpenConns(1)

	// 拆分并执行模式 SQL（跳过 FTS 和触发器，它们不需要列调和）
	for _, stmt := range splitSQLStatements(schema) {
		upper := strings.ToUpper(strings.TrimSpace(stmt))
		if strings.HasPrefix(upper, "CREATE VIRTUAL TABLE") ||
			strings.HasPrefix(upper, "CREATE TRIGGER") {
			continue
		}
		if _, err := ref.Exec(stmt); err != nil {
			slog.Debug("skipped statement while parsing schema definition", "stmt", stmt[:min(60, len(stmt))], "error", err)
		}
	}

	// 获取所有表名
	rows, err := ref.Query(
		"SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'",
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	tableColumns := make(map[string]map[string]string)
	var tableNames []string
	for rows.Next() {
		var tblName string
		if err := rows.Scan(&tblName); err != nil {
			return nil, err
		}
		tableNames = append(tableNames, tblName)
	}
	_ = rows.Close()

	for _, tblName := range tableNames {
		cols := make(map[string]string)
		infoRows, err := ref.Query("PRAGMA table_info(\"" + tblName + "\")")
		if err != nil {
			return nil, err
		}

		for infoRows.Next() {
			var cid int
			var name, colType string
			var notnull int
			var defaultVal sql.NullString
			var pk int
			if err := infoRows.Scan(&cid, &name, &colType, &notnull, &defaultVal, &pk); err != nil {
				_ = infoRows.Close()
				return nil, err
			}
			var parts []string
			if colType != "" {
				parts = append(parts, colType)
			}
			if notnull != 0 && pk == 0 {
				parts = append(parts, "NOT NULL")
			}
			if defaultVal.Valid {
				parts = append(parts, "DEFAULT "+defaultVal.String)
			}
			cols[name] = strings.Join(parts, " ")
		}
		_ = infoRows.Close()

		tableColumns[tblName] = cols
	}
	return tableColumns, nil
}

// reconcileColumns 通过对比 SCHEMA_SQL 定义与实际表结构来添加缺失的列。
//
// 遵循 Beets/sqlite-utils 模式: SCHEMA_SQL 中的 CREATE TABLE 定义
// 是所需模式的唯一真相来源。每次启动时，此函数对比实際列与声明列，
// 并 ALTER TABLE ADD COLUMN 任何缺失的列。
//
// 这使得列添加成为声明式操作 —— 只需将列添加到 schema.sql 即可，
// 它将在下次启动时自动创建。
func reconcileColumns(ctx context.Context, db *sql.DB) error {
	expected, err := parseSchemaColumns(schemaSQL)
	if err != nil {
		return pkgerrors.Wrap(pkgerrors.FileIO, "解析Schema列失败", err)
	}

	for tableName, declaredCols := range expected {
		// 获取实際表中存在的列
		rows, err := db.QueryContext(ctx, "PRAGMA table_info(\""+tableName+"\")")
		if err != nil {
			slog.Debug("failed to read table info (table may not exist)", "table", tableName, "error", err)
			continue
		}

		liveCols := make(map[string]bool)
		for rows.Next() {
			var cid int
			var name, colType string
			var notnull int
			var defaultVal sql.NullString
			var pk int
			if err := rows.Scan(&cid, &name, &colType, &notnull, &defaultVal, &pk); err != nil {
				_ = rows.Close()
				return pkgerrors.Wrap(pkgerrors.FileIO, fmt.Sprintf("读取 PRAGMA table_info(%s) 失败", tableName), err)
			}
			liveCols[name] = true
		}
		_ = rows.Close()

		// 添加缺失的列
		for colName, colType := range declaredCols {
			if liveCols[colName] {
				continue
			}
			safeName := strings.ReplaceAll(colName, "\"", "\"\"")
			alterSQL := fmt.Sprintf(
				"ALTER TABLE \"%s\" ADD COLUMN \"%s\" %s",
				tableName, safeName, colType,
			)
			if _, err := db.ExecContext(ctx, alterSQL); err != nil {
				slog.Debug("declarative column add failed",
					"table", tableName,
					"column", colName,
					"error", err,
				)
			} else {
				slog.Info("declarative column added", "table", tableName, "column", colName)
			}
		}
	}
	return nil
}

// ── SQL 语句处理 ────────────────────────────────────────────

// splitSQLStatements 按分号拆分 SQL 文本为独立语句。
// 跳过空行和纯注释行。正确处理 BEGIN...END 块内的分号——
// 只在嵌套深度为 0 时才在分号处拆分，避免触发器定义被截断。
func splitSQLStatements(sqlText string) []string {
	var result []string
	var current strings.Builder
	depth := 0

	for i := 0; i < len(sqlText); i++ {
		ch := sqlText[i]

		// 跳过单引号字符串
		if ch == '\'' {
			current.WriteByte(ch)
			for i++; i < len(sqlText); i++ {
				current.WriteByte(sqlText[i])
				if sqlText[i] == '\'' {
					// 检查转义引号 ''
					if i+1 < len(sqlText) && sqlText[i+1] == '\'' {
						i++
						current.WriteByte(sqlText[i])
						continue
					}
					break
				}
			}
			continue
		}

		// 跳过双引号标识符
		if ch == '"' {
			current.WriteByte(ch)
			for i++; i < len(sqlText); i++ {
				current.WriteByte(sqlText[i])
				if sqlText[i] == '"' {
					break
				}
			}
			continue
		}

		// 跳过 -- 单行注释
		if ch == '-' && i+1 < len(sqlText) && sqlText[i+1] == '-' {
			for ; i < len(sqlText) && sqlText[i] != '\n'; i++ {
				current.WriteByte(sqlText[i])
			}
			if i < len(sqlText) {
				current.WriteByte('\n')
			}
			continue
		}

		// 追踪 BEGIN/END 嵌套深度
		if ch == 'B' || ch == 'b' {
			upper := strings.ToUpper(sqlText[i:])
			if strings.HasPrefix(upper, "BEGIN") {
				// 确认后面不是标识符的一部分
				after := ""
				if len(upper) > 5 {
					after = string(upper[5])
				}
				if after == "" || after == " " || after == "\t" || after == "\n" || after == "\r" {
					depth++
				}
			}
		}
		if (ch == 'E' || ch == 'e') && depth > 0 {
			upper := strings.ToUpper(sqlText[i:])
			if strings.HasPrefix(upper, "END") {
				after := ""
				if len(upper) > 3 {
					after = string(upper[3])
				}
				if after == "" || after == ";" || after == " " || after == "\t" || after == "\n" || after == "\r" {
					depth--
				}
			}
		}

		// 只在嵌套深度为 0 时在分号处拆分
		if ch == ';' && depth == 0 {
			stmt := strings.TrimSpace(current.String())
			current.Reset()
			if stmt == "" {
				continue
			}
			if isCommentOnly(stmt) {
				continue
			}
			result = append(result, stmt)
			continue
		}

		current.WriteByte(ch)
	}

	// 处理末尾没有分号的语句
	stmt := strings.TrimSpace(current.String())
	if stmt != "" && !isCommentOnly(stmt) {
		result = append(result, stmt)
	}

	return result
}

// isCommentOnly 检查语句是否仅包含注释
func isCommentOnly(stmt string) bool {
	lines := strings.Split(stmt, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "--") {
			return false
		}
	}
	return true
}

