// Package state 提供基于 SQLite + FTS5 的状态持久化。
// 存储会话、消息历史、全文搜索索引。
// 使用 modernc.org/sqlite (纯 Go，无 CGo) 实现。
package state

import (
	"database/sql"
	"sync"

	_ "modernc.org/sqlite" // SQLite 纯 Go 驱动注册
)

// ───────────────────────────── 数据模型 ─────────────────────────────

// Session 表示一个对话会话的元数据
type Session struct {
	ID               string  `json:"id"`
	Source           string  `json:"source"`
	UserID           string  `json:"user_id"`
	Model            string  `json:"model"`
	ModelConfig      string  `json:"model_config"`
	SystemPrompt     string  `json:"system_prompt"`
	ParentSessionID  string  `json:"parent_session_id"`
	StartedAt        float64 `json:"started_at"`
	EndedAt          float64 `json:"ended_at"`
	EndReason        string  `json:"end_reason"`
	Title            string  `json:"title"`
	MessageCount     int     `json:"message_count"`
	ToolCallCount    int     `json:"tool_call_count"`
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	CacheReadTokens  int     `json:"cache_read_tokens"`
	CacheWriteTokens int     `json:"cache_write_tokens"`
	ReasoningTokens  int     `json:"reasoning_tokens"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`
	APICallCount     int     `json:"api_call_count"`
}

// MessageRecord 表示一条持久化的消息
type MessageRecord struct {
	ID           int64   `json:"id"`
	SessionID    string  `json:"session_id"`
	Role         string  `json:"role"`
	Content      string  `json:"content"`
	ToolCallID   string  `json:"tool_call_id"`
	ToolCalls    string  `json:"tool_calls"` // JSON 编码
	ToolName     string  `json:"tool_name"`
	Timestamp    float64 `json:"timestamp"`
	TokenCount   int     `json:"token_count"`
	FinishReason string  `json:"finish_reason"`
	Reasoning    string  `json:"reasoning"`
}

// SearchResult 表示 FTS5 搜索结果
type SearchResult struct {
	MessageID int64   `json:"message_id"`
	SessionID string  `json:"session_id"`
	Content   string  `json:"content"`
	Rank      float64 `json:"rank"`
}

// SessionFilter 定义会话查询的过滤条件
type SessionFilter struct {
	Source string // 按来源过滤
	UserID string // 按用户过滤
	Ended  *bool  // 按是否结束过滤
	Limit  int    // 最大返回数
	Offset int    // 偏移量
}

// ───────────────────────────── 存储 ─────────────────────────────

// Store 是状态持久化的主入口。
// 基于 SQLite (WAL 模式)，包含 FTS5 全文搜索。
type Store struct {
	db         *sql.DB
	mu         sync.RWMutex
	writeCount int  // 写操作计数器，用于定期触发 WAL checkpoint（受 mu 保护）
	closed     bool // 防止 Close 后继续访问 db
}

// NewStore 创建或打开 SQLite 数据库。
func NewStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite 单写者限制
	return &Store{db: db}, nil
}

// Close 安全关闭数据库连接。
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.db.Close()
}

// DB 返回底层数据库句柄 (供子包使用)。
func (s *Store) DB() *sql.DB {
	return s.db
}
