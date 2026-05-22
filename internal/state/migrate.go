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
		return fmt.Errorf("初始化基础模式失败: %w", err)
	}

	// 步骤 2: 声明式列调和
	if err := reconcileColumns(ctx, db); err != nil {
		return fmt.Errorf("列调和失败: %w", err)
	}

	// 步骤 3: 版本门控数据迁移
	dbVersion, err := getSchemaVersion(ctx, db)
	if err != nil {
		return fmt.Errorf("读取模式版本失败: %w", err)
	}

	if dbVersion < currentSchemaVersion {
		slog.Info("开始版本门控迁移",
			"from_version", dbVersion,
			"to_version", currentSchemaVersion,
		)
		if err := runVersionMigrations(ctx, db, dbVersion); err != nil {
			return fmt.Errorf("版本迁移失败: %w", err)
		}
	}

	// 步骤 4: 确保 FTS 表和触发器存在
	if err := ensureFTS(ctx, db); err != nil {
		return fmt.Errorf("FTS 设置失败: %w", err)
	}

	// 步骤 5: 确保唯一标题索引存在
	if err := ensureTitleIndex(ctx, db); err != nil {
		slog.Warn("创建标题唯一索引失败", "error", err)
	}

	// 步骤 6: 更新版本号
	if err := setSchemaVersion(ctx, db, currentSchemaVersion); err != nil {
		return fmt.Errorf("更新模式版本失败: %w", err)
	}

	slog.Info("数据库迁移完成", "version", currentSchemaVersion)
	return nil
}

// ── 版本门控迁移 ────────────────────────────────────────────

// runVersionMigrations 按版本顺序执行数据迁移
func runVersionMigrations(ctx context.Context, db *sql.DB, fromVersion int) error {
	// v10: trigram FTS5 表
	if fromVersion < 10 {
		if err := migrateV10(ctx, db); err != nil {
			return fmt.Errorf("v10 迁移失败: %w", err)
		}
	}

	// v11: 重建 FTS5 表以包含 tool_name + tool_calls 的内联索引
	if fromVersion < 11 {
		if err := migrateV11(ctx, db); err != nil {
			return fmt.Errorf("v11 迁移失败: %w", err)
		}
	}

	return nil
}

// migrateV10 创建 trigram FTS5 表用于 CJK 搜索
func migrateV10(ctx context.Context, db *sql.DB) error {
	slog.Info("运行 v10 迁移: 创建 trigram FTS5 表")

	// 检查 trigram 表是否已存在
	var exists int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='messages_fts_trigram'",
	).Scan(&exists)
	if err != nil {
		return err
	}

	if exists > 0 {
		slog.Debug("v10: trigram FTS5 表已存在，跳过")
		return nil
	}

	trigramSQL := `
	CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts_trigram USING fts5(
	    content, tokenize='trigram'
	);

	CREATE TRIGGER IF NOT EXISTS messages_fts_trigram_insert AFTER INSERT ON messages BEGIN
	    INSERT INTO messages_fts_trigram(rowid, content) VALUES (
	        new.id,
	        COALESCE(new.content, '') || ' ' || COALESCE(new.tool_name, '') || ' ' || COALESCE(new.tool_calls, '')
	    );
	END;

	CREATE TRIGGER IF NOT EXISTS messages_fts_trigram_delete AFTER DELETE ON messages BEGIN
	    DELETE FROM messages_fts_trigram WHERE rowid = old.id;
	END;

	CREATE TRIGGER IF NOT EXISTS messages_fts_trigram_update AFTER UPDATE ON messages BEGIN
	    DELETE FROM messages_fts_trigram WHERE rowid = old.id;
	    INSERT INTO messages_fts_trigram(rowid, content) VALUES (
	        new.id,
	        COALESCE(new.content, '') || ' ' || COALESCE(new.tool_name, '') || ' ' || COALESCE(new.tool_calls, '')
	    );
	END;
	`
	if err := execSchemaStatements(ctx, db, trigramSQL); err != nil {
		return err
	}

	// 回填已有数据
	_, err = db.ExecContext(ctx,
		`INSERT INTO messages_fts_trigram(rowid, content)
		 SELECT id, COALESCE(content, '') FROM messages WHERE content IS NOT NULL`,
	)
	if err != nil {
		slog.Warn("v10 trigram 回填失败（可能无数据）", "error", err)
	}

	return nil
}

// migrateV11 重建 FTS5 表以使用内联内容模式，索引 content || tool_name || tool_calls
func migrateV11(ctx context.Context, db *sql.DB) error {
	slog.Info("运行 v11 迁移: 重建 FTS5 索引覆盖 tool_name + tool_calls")

	// 删除旧的 FTS 触发器和表
	for _, trig := range []string{
		"messages_fts_insert",
		"messages_fts_delete",
		"messages_fts_update",
		"messages_fts_trigram_insert",
		"messages_fts_trigram_delete",
		"messages_fts_trigram_update",
	} {
		_, _ = db.ExecContext(ctx, "DROP TRIGGER IF EXISTS "+trig)
	}

	for _, tbl := range []string{"messages_fts", "messages_fts_trigram"} {
		_, _ = db.ExecContext(ctx, "DROP TABLE IF EXISTS "+tbl)
	}

	// 使用新内联模式重建虚拟表和触发器
	ftsSQL := `
	CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
	    content
	);

	CREATE TRIGGER IF NOT EXISTS messages_fts_insert AFTER INSERT ON messages BEGIN
	    INSERT INTO messages_fts(rowid, content) VALUES (
	        new.id,
	        COALESCE(new.content, '') || ' ' || COALESCE(new.tool_name, '') || ' ' || COALESCE(new.tool_calls, '')
	    );
	END;

	CREATE TRIGGER IF NOT EXISTS messages_fts_delete AFTER DELETE ON messages BEGIN
	    DELETE FROM messages_fts WHERE rowid = old.id;
	END;

	CREATE TRIGGER IF NOT EXISTS messages_fts_update AFTER UPDATE ON messages BEGIN
	    DELETE FROM messages_fts WHERE rowid = old.id;
	    INSERT INTO messages_fts(rowid, content) VALUES (
	        new.id,
	        COALESCE(new.content, '') || ' ' || COALESCE(new.tool_name, '') || ' ' || COALESCE(new.tool_calls, '')
	    );
	END;
	`
	if err := execSchemaStatements(ctx, db, ftsSQL); err != nil {
		return err
	}

	trigramSQL := `
	CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts_trigram USING fts5(
	    content, tokenize='trigram'
	);

	CREATE TRIGGER IF NOT EXISTS messages_fts_trigram_insert AFTER INSERT ON messages BEGIN
	    INSERT INTO messages_fts_trigram(rowid, content) VALUES (
	        new.id,
	        COALESCE(new.content, '') || ' ' || COALESCE(new.tool_name, '') || ' ' || COALESCE(new.tool_calls, '')
	    );
	END;

	CREATE TRIGGER IF NOT EXISTS messages_fts_trigram_delete AFTER DELETE ON messages BEGIN
	    DELETE FROM messages_fts_trigram WHERE rowid = old.id;
	END;

	CREATE TRIGGER IF NOT EXISTS messages_fts_trigram_update AFTER UPDATE ON messages BEGIN
	    DELETE FROM messages_fts_trigram WHERE rowid = old.id;
	    INSERT INTO messages_fts_trigram(rowid, content) VALUES (
	        new.id,
	        COALESCE(new.content, '') || ' ' || COALESCE(new.tool_name, '') || ' ' || COALESCE(new.tool_calls, '')
	    );
	END;
	`
	if err := execSchemaStatements(ctx, db, trigramSQL); err != nil {
		return err
	}

	// 回填所有已有消息行到两个 FTS 索引
	backfillSQL := `COALESCE(content, '') || ' ' || COALESCE(tool_name, '') || ' ' || COALESCE(tool_calls, '')`
	_, err := db.ExecContext(ctx,
		"INSERT INTO messages_fts(rowid, content) SELECT id, "+backfillSQL+" FROM messages",
	)
	if err != nil {
		slog.Warn("v11 messages_fts 回填失败", "error", err)
	}

	_, err = db.ExecContext(ctx,
		"INSERT INTO messages_fts_trigram(rowid, content) SELECT id, "+backfillSQL+" FROM messages",
	)
	if err != nil {
		slog.Warn("v11 messages_fts_trigram 回填失败", "error", err)
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
		return nil, fmt.Errorf("无法打开内存数据库解析Schema: %w", err)
	}
	defer ref.Close()

	// 拆分并执行模式 SQL（跳过 FTS 和触发器，它们不需要列调和）
	for _, stmt := range splitSQLStatements(schema) {
		upper := strings.ToUpper(strings.TrimSpace(stmt))
		if strings.HasPrefix(upper, "CREATE VIRTUAL TABLE") ||
			strings.HasPrefix(upper, "CREATE TRIGGER") {
			continue
		}
		if _, err := ref.Exec(stmt); err != nil {
			slog.Debug("解析Schema定义时跳过语句", "stmt", stmt[:min(60, len(stmt))], "error", err)
		}
	}

	// 获取所有表名
	rows, err := ref.Query(
		"SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tableColumns := make(map[string]map[string]string)
	for rows.Next() {
		var tblName string
		if err := rows.Scan(&tblName); err != nil {
			return nil, err
		}

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
				infoRows.Close()
				return nil, err
			}
			// 重建类型表达式供 ALTER TABLE ADD COLUMN 使用
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
		infoRows.Close()

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
		return fmt.Errorf("解析Schema列失败: %w", err)
	}

	for tableName, declaredCols := range expected {
		// 获取实際表中存在的列
		rows, err := db.QueryContext(ctx, "PRAGMA table_info(\""+tableName+"\")")
		if err != nil {
			slog.Debug("读取表信息失败（表可能不存在）", "table", tableName, "error", err)
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
				rows.Close()
				return fmt.Errorf("读取 PRAGMA table_info(%s) 失败: %w", tableName, err)
			}
			liveCols[name] = true
		}
		rows.Close()

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
				slog.Debug("声明式添加列失败",
					"table", tableName,
					"column", colName,
					"error", err,
				)
			} else {
				slog.Info("声明式添加列", "table", tableName, "column", colName)
			}
		}
	}
	return nil
}

// ── FTS 和索引确保 ──────────────────────────────────────────

// ensureFTS 确保 FTS5 虚拟表和触发器存在
func ensureFTS(ctx context.Context, db *sql.DB) error {
	// 检查默认 FTS 表
	ftsExists, err := tableExists(ctx, db, "messages_fts")
	if err != nil {
		return err
	}
	if !ftsExists {
		slog.Info("创建 messages_fts 虚拟表和触发器")
		if err := createFTSDefault(ctx, db); err != nil {
			return err
		}
		// 回填已有消息
		if _, err := db.ExecContext(ctx,
			`INSERT INTO messages_fts(rowid, content)
			 SELECT id, COALESCE(content, '') || ' ' || COALESCE(tool_name, '') || ' ' || COALESCE(tool_calls, '')
			 FROM messages`,
		); err != nil {
			slog.Debug("FTS 回填跳过（可能无消息）", "error", err)
		}
	}

	// 检查 trigram FTS 表
	trigramExists, err := tableExists(ctx, db, "messages_fts_trigram")
	if err != nil {
		return err
	}
	if !trigramExists {
		slog.Info("创建 messages_fts_trigram 虚拟表和触发器")
		if err := createFTSTrigram(ctx, db); err != nil {
			return err
		}
		// 回填已有消息
		if _, err := db.ExecContext(ctx,
			`INSERT INTO messages_fts_trigram(rowid, content)
			 SELECT id, COALESCE(content, '') || ' ' || COALESCE(tool_name, '') || ' ' || COALESCE(tool_calls, '')
			 FROM messages`,
		); err != nil {
			slog.Debug("Trigram FTS 回填跳过（可能无消息）", "error", err)
		}
	}

	return nil
}

// ensureTitleIndex 确保标题唯一索引存在
func ensureTitleIndex(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx,
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_sessions_title_unique ON sessions(title) WHERE title IS NOT NULL",
	)
	return err
}

// createFTSDefault 创建默认 FTS5 虚拟表和同步触发器
func createFTSDefault(ctx context.Context, db *sql.DB) error {
	sql := `
	CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(content);

	CREATE TRIGGER IF NOT EXISTS messages_fts_insert AFTER INSERT ON messages BEGIN
	    INSERT INTO messages_fts(rowid, content) VALUES (
	        new.id,
	        COALESCE(new.content, '') || ' ' || COALESCE(new.tool_name, '') || ' ' || COALESCE(new.tool_calls, '')
	    );
	END;

	CREATE TRIGGER IF NOT EXISTS messages_fts_delete AFTER DELETE ON messages BEGIN
	    DELETE FROM messages_fts WHERE rowid = old.id;
	END;

	CREATE TRIGGER IF NOT EXISTS messages_fts_update AFTER UPDATE ON messages BEGIN
	    DELETE FROM messages_fts WHERE rowid = old.id;
	    INSERT INTO messages_fts(rowid, content) VALUES (
	        new.id,
	        COALESCE(new.content, '') || ' ' || COALESCE(new.tool_name, '') || ' ' || COALESCE(new.tool_calls, '')
	    );
	END;
	`
	return execSchemaStatements(ctx, db, sql)
}

// createFTSTrigram 创建 trigram FTS5 虚拟表和同步触发器
func createFTSTrigram(ctx context.Context, db *sql.DB) error {
	sql := `
	CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts_trigram USING fts5(
	    content, tokenize='trigram'
	);

	CREATE TRIGGER IF NOT EXISTS messages_fts_trigram_insert AFTER INSERT ON messages BEGIN
	    INSERT INTO messages_fts_trigram(rowid, content) VALUES (
	        new.id,
	        COALESCE(new.content, '') || ' ' || COALESCE(new.tool_name, '') || ' ' || COALESCE(new.tool_calls, '')
	    );
	END;

	CREATE TRIGGER IF NOT EXISTS messages_fts_trigram_delete AFTER DELETE ON messages BEGIN
	    DELETE FROM messages_fts_trigram WHERE rowid = old.id;
	END;

	CREATE TRIGGER IF NOT EXISTS messages_fts_trigram_update AFTER UPDATE ON messages BEGIN
	    DELETE FROM messages_fts_trigram WHERE rowid = old.id;
	    INSERT INTO messages_fts_trigram(rowid, content) VALUES (
	        new.id,
	        COALESCE(new.content, '') || ' ' || COALESCE(new.tool_name, '') || ' ' || COALESCE(new.tool_calls, '')
	    );
	END;
	`
	return execSchemaStatements(ctx, db, sql)
}

// ── 辅助函数 ────────────────────────────────────────────────

// execSchemaStatements 按分号拆分 SQL 字符串并依次执行每条语句。
// 忽略空语句和注释。单个语句失败不阻止后续语句执行。
// CREATE TABLE 类语句失败会返回错误（可能是严重的 schema 损坏）。
// 其他语句失败仅记录日志。
func execSchemaStatements(ctx context.Context, db *sql.DB, sqlText string) error {
	var firstErr error
	for _, stmt := range splitSQLStatements(sqlText) {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			slog.Debug("Schema语句执行失败", "stmt", stmt[:min(80, len(stmt))], "error", err)
			// CREATE TABLE 语句失败通常意味着严重的 schema 问题，应向上报告
			if strings.Contains(strings.ToUpper(stmt), "CREATE TABLE") {
				if firstErr == nil {
					firstErr = fmt.Errorf("CREATE TABLE 语句执行失败: %w", err)
				}
			}
		}
	}
	return firstErr
}

// splitSQLStatements 按分号拆分 SQL 文本为独立语句。
// 跳过空行和纯注释行。
func splitSQLStatements(sqlText string) []string {
	raw := strings.Split(sqlText, ";")
	var result []string
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		// 跳过纯注释语句
		lines := strings.Split(s, "\n")
		allComments := true
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" && !strings.HasPrefix(trimmed, "--") {
				allComments = false
				break
			}
		}
		if allComments {
			continue
		}
		result = append(result, s)
	}
	return result
}

// tableExists 检查指定名称的表在数据库中是否存在
func tableExists(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var count int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?",
		name,
	).Scan(&count)
	return count > 0, err
}

// getSchemaVersion 读取当前数据库的模式版本号
func getSchemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	var version int
	err := db.QueryRowContext(ctx,
		"SELECT version FROM schema_version LIMIT 1",
	).Scan(&version)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return 0, nil
		}
		return 0, err
	}
	return version, nil
}

// setSchemaVersion 设置数据库模式版本号
func setSchemaVersion(ctx context.Context, db *sql.DB, version int) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO schema_version (version) VALUES (?)
		 ON CONFLICT DO UPDATE SET version = excluded.version`,
		version,
	)
	return err
}
