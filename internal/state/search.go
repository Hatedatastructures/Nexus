// Package state FTS5 全文搜索。
//
// 提供跨会话消息的全文搜索，自动检测 CJK 字符并选择合适的 FTS 表:
//   - 拉丁语系查询: 使用 messages_fts (unicode61 tokenizer)
//   - 3+ CJK 字符: 使用 messages_fts_trigram (trigram tokenizer)
//   - 1-2 CJK 字符: 回退到 LIKE 子串搜索
package state

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
)

// ── FTS 表创建 ──────────────────────────────────────────────

// CreateFTSTables 创建 FTS5 虚拟表和同步触发器。
//
// 如果表已存在则不做任何操作。首次安装时同时创建两个 FTS 表:
//   - messages_fts: 默认 unicode61 tokenizer (拉丁语系)
//   - messages_fts_trigram: trigram tokenizer (中日韩语系)
//
// 同时创建 INSERT/DELETE/UPDATE 触发器以保持 FTS 索引与消息表同步。
func (s *Store) CreateFTSTables(ctx context.Context) error {
	return ensureFTS(ctx, s.db)
}

// ── FTS 搜索 ────────────────────────────────────────────────

// SearchMessages 使用 FTS5 全文搜索查询跨会话消息。
//
// 自动检测 CJK 字符以选择合适的搜索策略:
//   - 无 CJK 字符: 使用 messages_fts (unicode61 tokenizer)
//   - 3+ CJK 字符: 使用 messages_fts_trigram (trigram tokenizer)
//   - 1-2 CJK 字符: 回退到 LIKE 子串搜索
//
// 返回匹配的消息及其会话信息、内容片段和排名。
func (s *Store) SearchMessages(ctx context.Context, query string, limit int) ([]*SearchResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}

	if limit <= 0 {
		limit = 20
	}

	// 清理 FTS5 查询语法
	sanitized := sanitizeFTS5Query(query)
	if sanitized == "" {
		return nil, nil
	}

	// 检测 CJK 字符
	if containsCJK(sanitized) {
		return s.searchCJK(ctx, sanitized, limit)
	}
	return s.searchLatin(ctx, sanitized, limit)
}

// searchLatin 使用 unicode61 FTS 表搜索拉丁语系文本
func (s *Store) searchLatin(ctx context.Context, query string, limit int) ([]*SearchResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.QueryContext(ctx,
		`SELECT
		    m.id, m.session_id,
		    snippet(messages_fts, 0, '>>>', '<<<', '...', 40) AS snippet,
		    rank
		 FROM messages_fts
		 JOIN messages m ON m.id = messages_fts.rowid
		 JOIN sessions s ON s.id = m.session_id
		 WHERE messages_fts MATCH ?
		 ORDER BY rank
		 LIMIT ?`,
		query, limit,
	)
	if err != nil {
		// FTS5 查询语法错误 —— 返回空结果
		slog.Warn("FTS5 search syntax error", "query", query, "error", err)
		return nil, nil
	}
	defer rows.Close()

	return scanSearchResults(rows)
}

// searchCJK 使用 trigram FTS 表搜索包含 CJK 字符的文本
func (s *Store) searchCJK(ctx context.Context, rawQuery string, limit int) ([]*SearchResult, error) {
	cjkCount := countCJK(rawQuery)

	if cjkCount >= 3 {
		// Trigram FTS5 路径 —— 引用每个非布尔标记
		return s.searchTrigramFTS(ctx, rawQuery, limit)
	}

	// 短 CJK 查询 (1-2字符) —— trigram 需要 3+ CJK 字符，回退到 LIKE
	return s.searchCJKLike(ctx, rawQuery, limit)
}

// searchTrigramFTS 使用 trigram FTS5 表搜索
func (s *Store) searchTrigramFTS(ctx context.Context, rawQuery string, limit int) ([]*SearchResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 引用非布尔标记以处理 FTS5 特殊字符
	tokens := strings.Fields(rawQuery)
	var parts []string
	for _, tok := range tokens {
		upper := strings.ToUpper(tok)
		if upper == "AND" || upper == "OR" || upper == "NOT" {
			parts = append(parts, tok)
		} else {
			escaped := strings.ReplaceAll(tok, "\"", "\"\"")
			parts = append(parts, "\""+escaped+"\"")
		}
	}
	trigramQuery := strings.Join(parts, " ")

	rows, err := s.db.QueryContext(ctx,
		`SELECT
		    m.id, m.session_id,
		    snippet(messages_fts_trigram, 0, '>>>', '<<<', '...', 40) AS snippet,
		    rank
		 FROM messages_fts_trigram
		 JOIN messages m ON m.id = messages_fts_trigram.rowid
		 JOIN sessions s ON s.id = m.session_id
		 WHERE messages_fts_trigram MATCH ?
		 ORDER BY rank
		 LIMIT ?`,
		trigramQuery, limit,
	)
	if err != nil {
		slog.Warn("trigram FTS5 search error", "query", trigramQuery, "error", err)
		return nil, nil
	}
	defer rows.Close()

	return scanSearchResults(rows)
}

// searchCJKLike 短 CJK 查询回退到 LIKE 子串搜索
func (s *Store) searchCJKLike(ctx context.Context, rawQuery string, limit int) ([]*SearchResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rawQuery = strings.Trim(rawQuery, "\"")
	escaped := escapeLikePattern(rawQuery)

	rows, err := s.db.QueryContext(ctx,
		`SELECT
		    m.id, m.session_id,
		    substr(m.content,
		           max(1, instr(m.content, ?) - 40),
		           120) AS snippet,
		    1.0 AS rank
		 FROM messages m
		 JOIN sessions s ON s.id = m.session_id
		 WHERE (m.content LIKE ? ESCAPE '\'
		     OR m.tool_name LIKE ? ESCAPE '\'
		     OR m.tool_calls LIKE ? ESCAPE '\')
		 ORDER BY m.timestamp DESC
		 LIMIT ?`,
		rawQuery,
		"%"+escaped+"%",
		"%"+escaped+"%",
		"%"+escaped+"%",
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("CJK LIKE 搜索失败: %w", err)
	}
	defer rows.Close()

	return scanSearchResults(rows)
}

// scanSearchResults 从查询结果行扫描 SearchResult 列表
func scanSearchResults(rows *sql.Rows) ([]*SearchResult, error) {
	var results []*SearchResult
	for rows.Next() {
		result := &SearchResult{}
		var snippet sql.NullString
		var rank sql.NullFloat64

		if err := rows.Scan(&result.MessageID, &result.SessionID, &snippet, &rank); err != nil {
			return nil, fmt.Errorf("扫描搜索结果失败: %w", err)
		}

		if snippet.Valid {
			result.Content = snippet.String
		}
		if rank.Valid {
			result.Rank = rank.Float64
		}

		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if results == nil {
		return []*SearchResult{}, nil
	}
	return results, nil
}

// ── 最近会话 ────────────────────────────────────────────────

// ListRecentSessions 列出最近活跃的会话。
//
// 按最后活动时间 (最新消息时间戳) 降序排列。包含 last_active 计算列。
func (s *Store) ListRecentSessions(ctx context.Context, limit int) ([]*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 {
		limit = 20
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT s.id, s.source, s.user_id, s.model, s.system_prompt, s.parent_session_id,
		        s.started_at, s.ended_at, s.end_reason, s.title,
		        s.message_count, s.tool_call_count, s.input_tokens, s.output_tokens,
		        s.cache_read_tokens, s.cache_write_tokens, s.estimated_cost_usd, s.api_call_count
		 FROM sessions s
		 LEFT JOIN (
		     SELECT session_id, MAX(timestamp) AS last_active
		     FROM messages GROUP BY session_id
		 ) m ON m.session_id = s.id
		 ORDER BY COALESCE(m.last_active, s.started_at) DESC,
		          s.started_at DESC, s.id DESC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("查询最近会话失败: %w", err)
	}
	defer rows.Close()

	var sessions []*Session
	for rows.Next() {
		sess := &Session{}
		var endedAt, estimatedCost sql.NullFloat64
		var userID, model, systemPrompt, parentSessionID, endReason, title sql.NullString
		var messageCount, toolCallCount, inputTokens, outputTokens sql.NullInt64
		var cacheReadTokens, cacheWriteTokens, apiCallCount sql.NullInt64

		if err := rows.Scan(
			&sess.ID, &sess.Source,
			&userID, &model, &systemPrompt, &parentSessionID,
			&sess.StartedAt, &endedAt, &endReason, &title,
			&messageCount, &toolCallCount, &inputTokens, &outputTokens,
			&cacheReadTokens, &cacheWriteTokens, &estimatedCost, &apiCallCount,
		); err != nil {
			return nil, fmt.Errorf("扫描会话行失败: %w", err)
		}

		sess.UserID = nullStr(userID)
		sess.Model = nullStr(model)
		sess.SystemPrompt = nullStr(systemPrompt)
		sess.ParentSessionID = nullStr(parentSessionID)
		sess.EndedAt = nullFloat(endedAt)
		sess.EndReason = nullStr(endReason)
		sess.Title = nullStr(title)
		sess.MessageCount = int(nullInt(messageCount))
		sess.ToolCallCount = int(nullInt(toolCallCount))
		sess.InputTokens = int(nullInt(inputTokens))
		sess.OutputTokens = int(nullInt(outputTokens))
		sess.CacheReadTokens = int(nullInt(cacheReadTokens))
		sess.CacheWriteTokens = int(nullInt(cacheWriteTokens))
		sess.APICallCount = int(nullInt(apiCallCount))
		sess.EstimatedCostUSD = nullFloat(estimatedCost)

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

// ── FTS5 查询清理 ───────────────────────────────────────────

// sanitizeFTS5Query 清理用户输入以适应 FTS5 MATCH 查询。
//
// FTS5 有自己的查询语法，其中 ", (, ), +, *, {, } 和
// AND, OR, NOT 等布尔操作符具有特殊含义。
// 直接传入用户输入会导致 sqlite3.OperationalError。
//
// 策略:
//   - 保留正确配对的引用短语 ("exact phrase")
//   - 去除不匹配的 FTS5 特殊字符
//   - 将无引号连字符/点分隔的术语包裹在引号中
func sanitizeFTS5Query(query string) string {
	// 步骤 1: 提取平衡的双引号短语并用占位符保护
	type quotePart struct {
		text string
	}
	quotedParts := []quotePart{}

	quoteRe := regexp.MustCompile(`"[^"]*"`)
	sanitized := quoteRe.ReplaceAllStringFunc(query, func(m string) string {
		idx := len(quotedParts)
		quotedParts = append(quotedParts, quotePart{text: m})
		return fmt.Sprintf("\x00Q%d\x00", idx)
	})

	// 步骤 2: 去除剩余的 FTS5 特殊字符
	specialRe := regexp.MustCompile(`[+{}()"^]`)
	sanitized = specialRe.ReplaceAllString(sanitized, " ")

	// 步骤 3: 折叠重复的 *
	multiStarRe := regexp.MustCompile(`\*+`)
	sanitized = multiStarRe.ReplaceAllString(sanitized, "*")

	// 步骤 4: 去除开头和空格后的 *
	leadingStarRe := regexp.MustCompile(`(^|\s)\*`)
	sanitized = leadingStarRe.ReplaceAllString(sanitized, "$1")

	// 步骤 5: 去除开头/结尾的悬挂布尔操作符
	danglingStart := regexp.MustCompile(`(?i)^(AND|OR|NOT)\b\s*`)
	sanitized = danglingStart.ReplaceAllString(strings.TrimSpace(sanitized), "")
	danglingEnd := regexp.MustCompile(`(?i)\s+(AND|OR|NOT)\s*$`)
	sanitized = danglingEnd.ReplaceAllString(strings.TrimSpace(sanitized), "")

	// 步骤 6: 用双引号包裹无引号连字符/点分隔的术语
	dotDashRe := regexp.MustCompile(`\b(\w+(?:[._-]\w+)+\b)`)
	sanitized = dotDashRe.ReplaceAllString(sanitized, `"$1"`)

	// 步骤 7: 恢复保护的引用短语
	for i, qp := range quotedParts {
		sanitized = strings.ReplaceAll(sanitized, fmt.Sprintf("\x00Q%d\x00", i), qp.text)
	}

	return strings.TrimSpace(sanitized)
}

// ── CJK 检测 ────────────────────────────────────────────────

// isCJKCodepoint 判断 Unicode 码点是否为 CJK 字符
func isCJKCodepoint(cp rune) bool {
	return (cp >= 0x4E00 && cp <= 0x9FFF) ||   // CJK 统一表意文字
		(cp >= 0x3400 && cp <= 0x4DBF) ||       // CJK 扩展 A
		(cp >= 0x20000 && cp <= 0x2A6DF) ||     // CJK 扩展 B
		(cp >= 0x3000 && cp <= 0x303F) ||       // CJK 符号
		(cp >= 0x3040 && cp <= 0x309F) ||       // 平假名
		(cp >= 0x30A0 && cp <= 0x30FF) ||       // 片假名
		(cp >= 0xAC00 && cp <= 0xD7AF)          // 韩文音节
}

// containsCJK 检查文本是否包含 CJK 字符
func containsCJK(text string) bool {
	for _, ch := range text {
		if isCJKCodepoint(ch) {
			return true
		}
	}
	return false
}

// countCJK 统计文本中的 CJK 字符数
func countCJK(text string) int {
	count := 0
	for _, ch := range text {
		if isCJKCodepoint(ch) {
			count++
		}
	}
	return count
}

// escapeLikePattern 转义 LIKE 搜索模式中的特殊字符
func escapeLikePattern(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
	return s
}

