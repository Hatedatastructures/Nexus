package state

import (
	"context"
	"database/sql"
	"log/slog"
	pkgerrors "nexus-agent/internal/errors"
	"strings"
)

// ── 版本迁移实现 ────────────────────────────────────────────

// migrateV10 创建 trigram FTS5 表用于 CJK 搜索
func migrateV10(ctx context.Context, db *sql.DB) error {
	slog.Info("running v10 migration: creating trigram FTS5 table")

	// 检查 trigram 表是否已存在
	var exists int
	err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='messages_fts_trigram'",
	).Scan(&exists)
	if err != nil {
		return err
	}

	if exists > 0 {
		slog.Debug("v10: trigram FTS5 table already exists, skipping")
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

	// 回填已有数据（包含 content + tool_name + tool_calls，与触发器一致）
	_, err = db.ExecContext(ctx,
		`INSERT INTO messages_fts_trigram(rowid, content)
		 SELECT id, COALESCE(content, '') || ' ' || COALESCE(tool_name, '') || ' ' || COALESCE(tool_calls, '')
		 FROM messages WHERE content IS NOT NULL`,
	)
	if err != nil {
		slog.Warn("v10 trigram backfill failed (possibly no data)", "error", err)
	}

	return nil
}

// migrateV11 重建 FTS5 表以使用内联内容模式，索引 content || tool_name || tool_calls
func migrateV11(ctx context.Context, db *sql.DB) error {
	slog.Info("running v11 migration: rebuilding FTS5 index to cover tool_name + tool_calls")

	// 删除旧的 FTS 触发器和表
	for _, trig := range []string{
		"messages_fts_insert",
		"messages_fts_delete",
		"messages_fts_update",
		"messages_fts_trigram_insert",
		"messages_fts_trigram_delete",
		"messages_fts_trigram_update",
	} {
		if _, err := db.ExecContext(ctx, "DROP TRIGGER IF EXISTS "+trig); err != nil {
			slog.Warn("migration v11: failed to drop trigger", "trigger", trig, "err", err)
		}
	}

	for _, tbl := range []string{"messages_fts", "messages_fts_trigram"} {
		if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS "+tbl); err != nil {
			slog.Warn("migration v11: failed to drop table", "table", tbl, "err", err)
		}
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
		slog.Warn("v11 messages_fts backfill failed", "error", err)
	}

	_, err = db.ExecContext(ctx,
		"INSERT INTO messages_fts_trigram(rowid, content) SELECT id, "+backfillSQL+" FROM messages",
	)
	if err != nil {
		slog.Warn("v11 messages_fts_trigram backfill failed", "error", err)
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
		slog.Info("creating messages_fts virtual table and triggers")
		if err := createFTSDefault(ctx, db); err != nil {
			return err
		}
		// 回填已有消息
		if _, err := db.ExecContext(ctx,
			`INSERT INTO messages_fts(rowid, content)
			 SELECT id, COALESCE(content, '') || ' ' || COALESCE(tool_name, '') || ' ' || COALESCE(tool_calls, '')
			 FROM messages`,
		); err != nil {
			slog.Debug("FTS backfill skipped (possibly no messages)", "error", err)
		}
	}

	// 检查 trigram FTS 表
	trigramExists, err := tableExists(ctx, db, "messages_fts_trigram")
	if err != nil {
		return err
	}
	if !trigramExists {
		slog.Info("creating messages_fts_trigram virtual table and triggers")
		if err := createFTSTrigram(ctx, db); err != nil {
			return err
		}
		// 回填已有消息
		if _, err := db.ExecContext(ctx,
			`INSERT INTO messages_fts_trigram(rowid, content)
			 SELECT id, COALESCE(content, '') || ' ' || COALESCE(tool_name, '') || ' ' || COALESCE(tool_calls, '')
			 FROM messages`,
		); err != nil {
			slog.Debug("trigram FTS backfill skipped (possibly no messages)", "error", err)
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

// ── SQL 执行辅助 ────────────────────────────────────────────

// execSchemaStatements 按分号拆分 SQL 字符串并依次执行每条语句。
// 忽略空语句和注释。单个语句失败不阻止后续语句执行。
// CREATE TABLE 类语句失败会返回错误（可能是严重的 schema 损坏）。
// 其他语句失败仅记录日志。
func execSchemaStatements(ctx context.Context, db *sql.DB, sqlText string) error {
	var firstErr error
	for _, stmt := range splitSQLStatements(sqlText) {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			slog.Debug("schema statement execution failed", "stmt", stmt[:min(80, len(stmt))], "error", err)
			// CREATE TABLE 语句失败通常意味着严重的 schema 问题，应向上报告
			if strings.Contains(strings.ToUpper(stmt), "CREATE TABLE") {
				if firstErr == nil {
					firstErr = pkgerrors.Wrap(pkgerrors.FileIO, "CREATE TABLE 语句执行失败", err)
				}
			}
		}
	}
	return firstErr
}

// ── 辅助函数 ────────────────────────────────────────────────

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
		if strings.Contains(err.Error(), "no such table") || err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, err
	}
	return version, nil
}

// setSchemaVersion 设置数据库模式版本号
func setSchemaVersion(ctx context.Context, db *sql.DB, version int) error {
	// 先尝试更新，如果没有行则插入
	result, err := db.ExecContext(ctx, "UPDATE schema_version SET version = ?", version)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		_, err = db.ExecContext(ctx, "INSERT INTO schema_version (version) VALUES (?)", version)
	}
	return err
}
