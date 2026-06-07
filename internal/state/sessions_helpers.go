package state

import (
	"database/sql"
	pkgerrors "nexus-agent/internal/errors"
)

// scanSession 从 sql.Row 扫描一个 Session 对象
func scanSession(row *sql.Row) (*Session, error) {
	session := &Session{}
	var endedAt, estimatedCost sql.NullFloat64
	var userID, model, modelConfig, systemPrompt, parentSessionID, endReason, title sql.NullString
	var messageCount, toolCallCount, inputTokens, outputTokens sql.NullInt64
	var cacheReadTokens, cacheWriteTokens, reasoningTokens, apiCallCount sql.NullInt64

	err := row.Scan(
		&session.ID, &session.Source,
		&userID, &model, &modelConfig, &systemPrompt, &parentSessionID,
		&session.StartedAt, &endedAt, &endReason, &title,
		&messageCount, &toolCallCount, &inputTokens, &outputTokens,
		&cacheReadTokens, &cacheWriteTokens, &reasoningTokens, &estimatedCost, &apiCallCount,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, pkgerrors.Wrap(pkgerrors.FileIO, "扫描会话失败", err)
	}

	session.UserID = nullStr(userID)
	session.Model = nullStr(model)
	session.ModelConfig = nullStr(modelConfig)
	session.SystemPrompt = nullStr(systemPrompt)
	session.ParentSessionID = nullStr(parentSessionID)
	session.EndedAt = nullFloat(endedAt)
	session.EndReason = nullStr(endReason)
	session.Title = nullStr(title)
	session.MessageCount = int(nullInt(messageCount))
	session.ToolCallCount = int(nullInt(toolCallCount))
	session.InputTokens = int(nullInt(inputTokens))
	session.OutputTokens = int(nullInt(outputTokens))
	session.CacheReadTokens = int(nullInt(cacheReadTokens))
	session.CacheWriteTokens = int(nullInt(cacheWriteTokens))
	session.ReasoningTokens = int(nullInt(reasoningTokens))
	session.APICallCount = int(nullInt(apiCallCount))
	session.EstimatedCostUSD = nullFloat(estimatedCost)

	return session, nil
}

// scanSessionRow 从 sql.Rows 扫描一个 Session 对象
func scanSessionRow(rows *sql.Rows) (*Session, error) {
	session := &Session{}
	var endedAt, estimatedCost sql.NullFloat64
	var userID, model, modelConfig, systemPrompt, parentSessionID, endReason, title sql.NullString
	var messageCount, toolCallCount, inputTokens, outputTokens sql.NullInt64
	var cacheReadTokens, cacheWriteTokens, reasoningTokens, apiCallCount sql.NullInt64

	err := rows.Scan(
		&session.ID, &session.Source,
		&userID, &model, &modelConfig, &systemPrompt, &parentSessionID,
		&session.StartedAt, &endedAt, &endReason, &title,
		&messageCount, &toolCallCount, &inputTokens, &outputTokens,
		&cacheReadTokens, &cacheWriteTokens, &reasoningTokens, &estimatedCost, &apiCallCount,
	)
	if err != nil {
		return nil, err
	}

	session.UserID = nullStr(userID)
	session.Model = nullStr(model)
	session.ModelConfig = nullStr(modelConfig)
	session.SystemPrompt = nullStr(systemPrompt)
	session.ParentSessionID = nullStr(parentSessionID)
	session.EndedAt = nullFloat(endedAt)
	session.EndReason = nullStr(endReason)
	session.Title = nullStr(title)
	session.MessageCount = int(nullInt(messageCount))
	session.ToolCallCount = int(nullInt(toolCallCount))
	session.InputTokens = int(nullInt(inputTokens))
	session.OutputTokens = int(nullInt(outputTokens))
	session.CacheReadTokens = int(nullInt(cacheReadTokens))
	session.CacheWriteTokens = int(nullInt(cacheWriteTokens))
	session.ReasoningTokens = int(nullInt(reasoningTokens))
	session.APICallCount = int(nullInt(apiCallCount))
	session.EstimatedCostUSD = nullFloat(estimatedCost)

	return session, nil
}

// ── 可空类型辅助 ────────────────────────────────────────────

func nullStr(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

func nullFloat(nf sql.NullFloat64) float64 {
	if nf.Valid {
		return nf.Float64
	}
	return 0
}

func nullInt(ni sql.NullInt64) int64 {
	if ni.Valid {
		return ni.Int64
	}
	return 0
}
