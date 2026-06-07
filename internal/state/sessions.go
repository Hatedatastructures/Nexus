// Package state 会话管理。
//
// 提供会话的创建、查询、更新、结束和列表等 CRUD 操作。
// 所有写操作通过 executeWrite() 使用 BEGIN IMMEDIATE + 随机抖动重试，
// 避免多个进程同时写入时的车队效应。
package state

import (
	"context"
	"database/sql"
	"fmt"

	"log/slog"
	"math/rand/v2"
	pkgerrors "nexus-agent/internal/errors"
	"strings"
	"time"
)

// ── 写事务重试 ──────────────────────────────────────────────

const (
	writeMaxRetries = 15  // 最大重试次数
	writeRetryMinMs = 20  // 最小抖动时间 (毫秒)
	writeRetryMaxMs = 150 // 最大抖动时间 (毫秒)
	checkpointEvery = 50  // 每 N 次写入尝试一次 WAL checkpoint
)

// writeCount 已移入 Store 结构体，参见 db.go

// executeWrite 带重试的写事务执行器。
//
// 使用 BEGIN IMMEDIATE 在事务开始时获取 WAL 写锁（而非等到 COMMIT），
// 使锁竞争立刻浮现。遇到 "database is locked" 时释放互斥锁，
// 随机等待 20-150ms 后重试 —— 打破 SQLite 内置确定性退避造成的车队模式。
//
// 调用方通过闭包捕获返回值。
func (s *Store) executeWrite(ctx context.Context, fn func(*sql.DB) error) error {
	var lastErr error
	for attempt := 0; attempt < writeMaxRetries; attempt++ {
		s.mu.Lock()

		// BEGIN IMMEDIATE: 立刻获取 WAL 写锁
		_, err := s.db.ExecContext(ctx, "BEGIN IMMEDIATE")
		if err != nil {
			s.mu.Unlock()
			if isLockedErr(err) && attempt < writeMaxRetries-1 {
				lastErr = err
				time.Sleep(jitterSleep())
				continue
			}
			return pkgerrors.Wrap(pkgerrors.FileIO, "BEGIN IMMEDIATE 失败", err)
		}

		// 执行写操作
		err = fn(s.db)
		if err != nil {
			// 回滚（尽力而为）
			_, _ = s.db.ExecContext(ctx, "ROLLBACK")
			s.mu.Unlock()
			return err
		}

		// 提交
		_, err = s.db.ExecContext(ctx, "COMMIT")

		s.writeCount++
		needCheckpoint := s.writeCount%checkpointEvery == 0
		s.mu.Unlock()

		if err != nil {
			if isLockedErr(err) && attempt < writeMaxRetries-1 {
				lastErr = err
				time.Sleep(jitterSleep())
				continue
			}
			return pkgerrors.Wrap(pkgerrors.FileIO, "COMMIT 失败", err)
		}

		if needCheckpoint {
			s.tryCheckpoint(context.Background())
		}

		return nil
	}

	if lastErr != nil {
		return pkgerrors.Wrap(pkgerrors.FileIO, fmt.Sprintf("数据库写操作重试%d次后仍失败", writeMaxRetries), lastErr)
	}
	return pkgerrors.New(pkgerrors.FileIO, fmt.Sprintf("数据库写操作重试%d次后仍失败", writeMaxRetries))
}

// jitterSleep 返回 20-150ms 之间的随机等待时长。
// 随机性确保竞争的 writer 不会同时重试。
func jitterSleep() time.Duration {
	ms := writeRetryMinMs + rand.IntN(writeRetryMaxMs-writeRetryMinMs+1)
	return time.Duration(ms) * time.Millisecond
}

// isLockedErr 判断错误是否由 SQLite 锁竞争引起
func isLockedErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "locked") || strings.Contains(msg, "busy")
}

// tryCheckpoint 尝试执行 PASSIVE WAL checkpoint（尽力而为，永不报错）
func (s *Store) tryCheckpoint(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()

	result, err := s.db.QueryContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)")
	if err != nil {
		return
	}
	defer func() { _ = result.Close() }()

	// wal_checkpoint 返回三列: busy, total, checkpointed
	var busy, total, checkpointed int
	if result.Next() {
		if err := result.Scan(&busy, &total, &checkpointed); err == nil {
			if total > 0 {
				slog.Debug("WAL checkpoint completed",
					"busy", busy,
					"total", total,
					"checkpointed", checkpointed,
				)
			}
		}
	}
}

// ── 会话 CRUD ───────────────────────────────────────────────

const sessionColumns = `id, source, user_id, model, model_config, system_prompt, parent_session_id,
started_at, ended_at, end_reason, title,
message_count, tool_call_count, input_tokens, output_tokens,
cache_read_tokens, cache_write_tokens, reasoning_tokens, estimated_cost_usd, api_call_count`

// CreateSession 创建新的会话记录。
// 如果 session.ID 已存在（INSERT OR IGNORE），则不执行任何操作。
func (s *Store) CreateSession(ctx context.Context, session *Session) error {
	return s.executeWrite(ctx, func(db *sql.DB) error {
		startedAt := session.StartedAt
		if startedAt == 0 {
			startedAt = float64(time.Now().Unix())
		}
		_, err := db.ExecContext(ctx,
			`INSERT OR IGNORE INTO sessions
			 (id, source, user_id, model, model_config, system_prompt, parent_session_id, started_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			session.ID,
			session.Source,
			session.UserID,
			session.Model,
			session.ModelConfig,
			session.SystemPrompt,
			session.ParentSessionID,
			startedAt,
		)
		return err
	})
}

// GetSession 根据 ID 获取会话。
// 如果会话不存在则返回 nil。
func (s *Store) GetSession(ctx context.Context, id string) (*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.getSessionLocked(ctx, id)
}

// UpdateSession 更新会话的全部可变字段。
// 以 session.ID 为键，更新所有非零值字段。
func (s *Store) UpdateSession(ctx context.Context, session *Session) error {
	return s.executeWrite(ctx, func(db *sql.DB) error {
		_, err := db.ExecContext(ctx,
			`UPDATE sessions SET
			 source = ?, user_id = ?, model = ?, model_config = ?, system_prompt = ?,
			 parent_session_id = ?, ended_at = ?, end_reason = ?, title = ?,
			 message_count = ?, tool_call_count = ?, input_tokens = ?,
			 output_tokens = ?, cache_read_tokens = ?, cache_write_tokens = ?,
			 reasoning_tokens = ?, estimated_cost_usd = ?, api_call_count = ?
			 WHERE id = ?`,
			session.Source,
			session.UserID,
			session.Model,
			session.ModelConfig,
			session.SystemPrompt,
			session.ParentSessionID,
			session.EndedAt,
			session.EndReason,
			session.Title,
			session.MessageCount,
			session.ToolCallCount,
			session.InputTokens,
			session.OutputTokens,
			session.CacheReadTokens,
			session.CacheWriteTokens,
			session.ReasoningTokens,
			session.EstimatedCostUSD,
			session.APICallCount,
			session.ID,
		)
		return err
	})
}

// EndSession 将会话标记为已结束。
//
// 仅更新尚未结束的会话（ended_at IS NULL）。
// 第一个结束原因胜出 —— 如果会话已经是某种原因结束的
// （例如 'compression'），后续调用不会覆盖该原因。
func (s *Store) EndSession(ctx context.Context, id string, reason string) error {
	return s.executeWrite(ctx, func(db *sql.DB) error {
		_, err := db.ExecContext(ctx,
			`UPDATE sessions SET ended_at = ?, end_reason = ?
			 WHERE id = ? AND ended_at IS NULL`,
			time.Now().Unix(),
			reason,
			id,
		)
		return err
	})
}

// ListSessions 根据过滤条件查询会话列表。
//
// 结果按 started_at DESC 排序，支持分页。
func (s *Store) ListSessions(ctx context.Context, filter *SessionFilter) ([]*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var conditions []string
	var args []interface{}

	if filter != nil {
		if filter.Source != "" {
			conditions = append(conditions, "source = ?")
			args = append(args, filter.Source)
		}
		if filter.UserID != "" {
			conditions = append(conditions, "user_id = ?")
			args = append(args, filter.UserID)
		}
		if filter.Ended != nil {
			if *filter.Ended {
				conditions = append(conditions, "ended_at IS NOT NULL")
			} else {
				conditions = append(conditions, "ended_at IS NULL")
			}
		}
	}

	query := "SELECT " + sessionColumns + " FROM sessions"

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY started_at DESC"

	limit := 20
	offset := 0
	if filter != nil {
		if filter.Limit > 0 {
			limit = filter.Limit
		}
		if filter.Offset > 0 {
			offset = filter.Offset
		}
	}
	query += " LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.FileIO, "查询会话列表失败", err)
	}
	defer func() { _ = rows.Close() }()

	var sessions []*Session
	for rows.Next() {
		sess, err := scanSessionRow(rows)
		if err != nil {
			return nil, pkgerrors.Wrap(pkgerrors.FileIO, "扫描会话行失败", err)
		}
		sessions = append(sessions, sess)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if sessions == nil {
		return []*Session{}, nil
	}
	return sessions, nil
}

// GetCompressionTip 沿 parent_session_id 链向前遍历，找到最新的活跃后代。
//
// 压缩延续链的判定条件:
//  1. 父会话的 end_reason = 'compression'
//  2. 子会话在父会话结束后创建 (started_at >= ended_at)
//
// 第二个条件区分了压缩延续与委托子代理或分支子会话。
//
// 返回链中最新的会话 ID，如果不存在压缩链则返回输入的 session_id。
// 最多遍历 100 层以防意外循环。
func (s *Store) GetCompressionTip(ctx context.Context, id string) (*Session, error) {
	current := id
	s.mu.RLock()
	defer s.mu.RUnlock()

	for range 100 {
		var childID sql.NullString
		err := s.db.QueryRowContext(ctx,
			`SELECT id FROM sessions
			 WHERE parent_session_id = ?
			   AND started_at >= (
			       SELECT ended_at FROM sessions
			       WHERE id = ? AND end_reason = 'compression'
			   )
			 ORDER BY started_at DESC LIMIT 1`,
			current, current,
		).Scan(&childID)

		if err != nil || !childID.Valid {
			// 没有符合条件的子会话，返回当前会话
			return s.getSessionLocked(ctx, current)
		}
		current = childID.String
	}

	// 超过最大遍历深度
	slog.Warn("GetCompressionTip reached max traversal depth", "session_id", id)
	return s.getSessionLocked(ctx, current)
}

// getSessionLocked 在持有锁的情况下查询会话（内部使用）
func (s *Store) getSessionLocked(ctx context.Context, id string) (*Session, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+sessionColumns+" FROM sessions WHERE id = ?",
		id,
	)
	return scanSession(row)
}

