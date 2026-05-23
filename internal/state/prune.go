// Package state 自动清理和 WAL 维护。
//
// 提供过期会话的自动删除和 WAL checkpoint 功能。
// 所有写操作通过 executeWrite() 执行，带重试机制。
package state

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"
)

// ── 自动清理 ────────────────────────────────────────────────

// AutoPrune 删除超过指定天数的已结束会话。
//
// 只清理已结束的会话（ended_at IS NOT NULL），不会删除活跃会话。
// 子会话的 parent_session_id 会被置为 NULL（孤儿化），而非级联删除。
//
// 在删除之前尝试 TRUNCATE WAL checkpoint，在删除之后建议调用
// VACUUM 以回收磁盘空间（需要排他锁，应在系统空闲时调用）。
//
// 返回成功删除的会话数量。
func (s *Store) AutoPrune(ctx context.Context, maxAgeDays int) (int, error) {
	if maxAgeDays <= 0 {
		maxAgeDays = 90 // 默认 90 天
	}

	cutoff := time.Now().Unix() - int64(maxAgeDays)*86400
	var removedIDs []string

	err := s.executeWrite(ctx, func(db *sql.DB) error {
		// 查询待删除的会话 ID
		rows, err := db.QueryContext(ctx,
			`SELECT id FROM sessions
			 WHERE started_at < ? AND ended_at IS NOT NULL`,
			float64(cutoff),
		)
		if err != nil {
			return fmt.Errorf("查询过期会话失败: %w", err)
		}
		defer rows.Close()

		var sessionIDs []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return fmt.Errorf("扫描会话ID失败: %w", err)
			}
			sessionIDs = append(sessionIDs, id)
		}
		if err := rows.Err(); err != nil {
			return err
		}

		if len(sessionIDs) == 0 {
			return nil
		}

		// 孤儿化将删除会话作为父会话的子会话
		if err := orphanChildren(ctx, db, sessionIDs); err != nil {
			return err
		}

		// 逐个删除会话及其消息
		for _, sid := range sessionIDs {
			if _, err := db.ExecContext(ctx,
				"DELETE FROM messages WHERE session_id = ?", sid,
			); err != nil {
				return fmt.Errorf("删除消息失败(session=%s): %w", sid, err)
			}
			if _, err := db.ExecContext(ctx,
				"DELETE FROM sessions WHERE id = ?", sid,
			); err != nil {
				return fmt.Errorf("删除会话失败(session=%s): %w", sid, err)
			}
			removedIDs = append(removedIDs, sid)
		}

		return nil
	})

	if err != nil {
		return 0, err
	}

	if len(removedIDs) > 0 {
		slog.Info("auto-pruned expired sessions",
			"count", len(removedIDs),
			"max_age_days", maxAgeDays,
		)
	}

	return len(removedIDs), nil
}

// orphanChildren 将引用被删除会话的子会话的 parent_session_id 置为 NULL
func orphanChildren(ctx context.Context, db *sql.DB, parentIDs []string) error {
	if len(parentIDs) == 0 {
		return nil
	}

	// 构建参数化查询的占位符
	placeholders := make([]string, len(parentIDs))
	args := make([]interface{}, len(parentIDs))
	for i, id := range parentIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(
		"UPDATE sessions SET parent_session_id = NULL WHERE parent_session_id IN (%s)",
		joinStrings(placeholders, ", "),
	)

	_, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("孤儿化子会话失败: %w", err)
	}
	return nil
}

// ── WAL Checkpoint ──────────────────────────────────────────

// CheckpointWAL 执行 PASSIVE WAL checkpoint。
//
// PASSIVE 模式尽可能多地将已提交的 WAL 帧写回主数据库文件，
// 同时不阻塞任何读取或写入。适用于定期后台维护。
//
// 在持有互斥锁的情况下执行以确保安全。
func (s *Store) CheckpointWAL(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	result, err := s.db.QueryContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)")
	if err != nil {
		return fmt.Errorf("WAL checkpoint 查询失败: %w", err)
	}
	defer result.Close()

	// wal_checkpoint 返回三列: busy, total, checkpointed
	if result.Next() {
		var busy, total, checkpointed int
		if err := result.Scan(&busy, &total, &checkpointed); err != nil {
			return fmt.Errorf("扫描 WAL checkpoint 结果失败: %w", err)
		}

		if total > 0 {
			slog.Debug("WAL checkpoint",
				"busy", busy,
				"total", total,
				"checkpointed", checkpointed,
			)
		}
	}

	return nil
}

// ── 辅助 ────────────────────────────────────────────────────

// joinStrings 连接字符串切片
func joinStrings(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	result := parts[0]
	for i := 1; i < len(parts); i++ {
		result += sep + parts[i]
	}
	return result
}
