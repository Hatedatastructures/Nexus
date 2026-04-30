// Package state 消息持久化。
//
// 提供消息的单条和批量插入、按会话查询以及计数功能。
// 消息插入时会自动更新所属会话的 message_count 和 tool_call_count 计数器。
package state

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// ── 消息 CRUD ───────────────────────────────────────────────

// InsertMessage 插入一条消息记录并更新会话计数器。
//
// 自动:
//   - 设置 timestamp 为当前 Unix 时间
//   - 递增会话的 message_count
//   - 如果消息包含工具调用，递增会话的 tool_call_count
func (s *Store) InsertMessage(ctx context.Context, msg *MessageRecord) error {
	return s.executeWrite(ctx, func(db *sql.DB) error {
		// 计算工具调用数量
		toolCallIncrement := 0
		if msg.ToolCalls != "" && msg.ToolCalls != "null" && msg.ToolCalls != "[]" {
			toolCallIncrement = 1
		}

		// 插入消息
		ts := msg.Timestamp
		if ts == 0 {
			ts = float64(time.Now().Unix())
		}

		result, err := db.ExecContext(ctx,
			`INSERT INTO messages
			 (session_id, role, content, tool_call_id, tool_calls, tool_name,
			  timestamp, token_count, finish_reason, reasoning)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			msg.SessionID,
			msg.Role,
			msg.Content,
			nullStrOrNil(msg.ToolCallID),
			nullStrOrNil(msg.ToolCalls),
			nullStrOrNil(msg.ToolName),
			ts,
			nullIntOrNil(msg.TokenCount),
			nullStrOrNil(msg.FinishReason),
			nullStrOrNil(msg.Reasoning),
		)
		if err != nil {
			return fmt.Errorf("插入消息失败: %w", err)
		}

		msgID, _ := result.LastInsertId()
		msg.ID = msgID

		// 更新会话计数器
		if toolCallIncrement > 0 {
			_, err = db.ExecContext(ctx,
				`UPDATE sessions SET
				 message_count = message_count + 1,
				 tool_call_count = tool_call_count + ?
				 WHERE id = ?`,
				toolCallIncrement, msg.SessionID,
			)
		} else {
			_, err = db.ExecContext(ctx,
				`UPDATE sessions SET message_count = message_count + 1
				 WHERE id = ?`,
				msg.SessionID,
			)
		}

		return err
	})
}

// InsertMessagesBatch 批量插入消息记录。
//
// 所有消息在同一个写事务中插入，保证原子性。
// 每条消息单独更新会话计数器。
func (s *Store) InsertMessagesBatch(ctx context.Context, msgs []*MessageRecord) error {
	if len(msgs) == 0 {
		return nil
	}

	return s.executeWrite(ctx, func(db *sql.DB) error {
		for _, msg := range msgs {
			toolCallIncrement := 0
			if msg.ToolCalls != "" && msg.ToolCalls != "null" && msg.ToolCalls != "[]" {
				toolCallIncrement = 1
			}

			ts := msg.Timestamp
			if ts == 0 {
				ts = float64(time.Now().Unix())
			}

			_, err := db.ExecContext(ctx,
				`INSERT INTO messages
				 (session_id, role, content, tool_call_id, tool_calls, tool_name,
				  timestamp, token_count, finish_reason, reasoning)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				msg.SessionID,
				msg.Role,
				msg.Content,
				nullStrOrNil(msg.ToolCallID),
				nullStrOrNil(msg.ToolCalls),
				nullStrOrNil(msg.ToolName),
				ts,
				nullIntOrNil(msg.TokenCount),
				nullStrOrNil(msg.FinishReason),
				nullStrOrNil(msg.Reasoning),
			)
			if err != nil {
				return fmt.Errorf("批量插入消息失败(session=%s): %w", msg.SessionID, err)
			}

			// 更新会话计数器
			if toolCallIncrement > 0 {
				_, err = db.ExecContext(ctx,
					`UPDATE sessions SET
					 message_count = message_count + 1,
					 tool_call_count = tool_call_count + ?
					 WHERE id = ?`,
					toolCallIncrement, msg.SessionID,
				)
			} else {
				_, err = db.ExecContext(ctx,
					`UPDATE sessions SET message_count = message_count + 1
					 WHERE id = ?`,
					msg.SessionID,
				)
			}
			if err != nil {
				return fmt.Errorf("更新会话计数器失败(session=%s): %w", msg.SessionID, err)
			}
		}
		return nil
	})
}

// GetMessages 获取指定会话的消息列表，按时间戳排序。
//
// 支持分页: limit 控制返回条数，offset 控制偏移。
// limit <= 0 时默认返回 100 条。
func (s *Store) GetMessages(ctx context.Context, sessionID string, limit, offset int) ([]*MessageRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 100
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, role, content, tool_call_id, tool_calls, tool_name,
		        timestamp, token_count, finish_reason, reasoning
		 FROM messages WHERE session_id = ?
		 ORDER BY timestamp ASC, id ASC
		 LIMIT ? OFFSET ?`,
		sessionID, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("查询消息失败: %w", err)
	}
	defer rows.Close()

	var messages []*MessageRecord
	for rows.Next() {
		msg := &MessageRecord{}
		var toolCallID, toolCalls, toolName, finishReason, reasoning sql.NullString
		var tokenCount sql.NullInt64
		var content sql.NullString

		if err := rows.Scan(
			&msg.ID, &msg.SessionID, &msg.Role, &content,
			&toolCallID, &toolCalls, &toolName,
			&msg.Timestamp, &tokenCount, &finishReason, &reasoning,
		); err != nil {
			return nil, fmt.Errorf("扫描消息行失败: %w", err)
		}

		msg.Content = nullStr(content)
		msg.ToolCallID = nullStr(toolCallID)
		msg.ToolCalls = nullStr(toolCalls)
		msg.ToolName = nullStr(toolName)
		msg.TokenCount = int(nullInt(tokenCount))
		msg.FinishReason = nullStr(finishReason)
		msg.Reasoning = nullStr(reasoning)

		messages = append(messages, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if messages == nil {
		return []*MessageRecord{}, nil
	}
	return messages, nil
}

// GetMessageCount 获取指定会话的消息总数。
func (s *Store) GetMessageCount(ctx context.Context, sessionID string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var count int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM messages WHERE session_id = ?",
		sessionID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("查询消息计数失败: %w", err)
	}
	return count, nil
}

// ── 辅助函数 ────────────────────────────────────────────────

// nullStrOrNil 将空字符串转换为 SQL NULL
func nullStrOrNil(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// nullIntOrNil 将零值 int 转换为 SQL NULL
func nullIntOrNil(n int) interface{} {
	if n == 0 {
		return nil
	}
	return n
}
