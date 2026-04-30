-- Hermes Agent 状态数据库模式定义
-- 版本: v11
-- 此文件通过 Go embed 嵌入到二进制中，作为数据库模式的唯一真相来源。
-- _reconcile_columns() 在每次启动时将此文件与实際表结构进行对比，
-- 自动添加缺失的列。

-- ── 模式版本表 ────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER NOT NULL
);

-- ── 会话表 ────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS sessions (
    id                 TEXT PRIMARY KEY,
    source             TEXT NOT NULL,
    user_id            TEXT,
    model              TEXT,
    model_config       TEXT,
    system_prompt      TEXT,
    parent_session_id  TEXT,
    started_at         REAL NOT NULL,
    ended_at           REAL,
    end_reason         TEXT,
    title              TEXT,
    message_count      INTEGER DEFAULT 0,
    tool_call_count    INTEGER DEFAULT 0,
    input_tokens       INTEGER DEFAULT 0,
    output_tokens      INTEGER DEFAULT 0,
    cache_read_tokens  INTEGER DEFAULT 0,
    cache_write_tokens INTEGER DEFAULT 0,
    reasoning_tokens   INTEGER DEFAULT 0,
    estimated_cost_usd REAL,
    api_call_count     INTEGER DEFAULT 0,
    FOREIGN KEY (parent_session_id) REFERENCES sessions(id)
);

-- ── 消息表 ────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS messages (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id      TEXT NOT NULL REFERENCES sessions(id),
    role            TEXT NOT NULL,
    content         TEXT,
    tool_call_id    TEXT,
    tool_calls      TEXT,
    tool_name       TEXT,
    timestamp       REAL NOT NULL,
    token_count     INTEGER,
    finish_reason   TEXT,
    reasoning       TEXT
);

-- ── FTS5 虚拟表 (默认 unicode61 tokenizer, 拉丁语系) ──────────

CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
    content
);

-- ── FTS5 虚拟表 (trigram tokenizer, 中日韩语系) ───────────────

CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts_trigram USING fts5(
    content,
    tokenize='trigram'
);

-- ── 索引 ──────────────────────────────────────────────────────

CREATE INDEX IF NOT EXISTS idx_sessions_source ON sessions(source);
CREATE INDEX IF NOT EXISTS idx_sessions_parent ON sessions(parent_session_id);
CREATE INDEX IF NOT EXISTS idx_sessions_started ON sessions(started_at DESC);
CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, timestamp);
CREATE UNIQUE INDEX IF NOT EXISTS idx_sessions_title_unique ON sessions(title) WHERE title IS NOT NULL;

-- ── FTS 同步触发器 (messages → messages_fts) ──────────────────

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

-- ── FTS 同步触发器 (messages → messages_fts_trigram) ──────────

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

-- ── KV 元数据表 ───────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS state_meta (
    key   TEXT PRIMARY KEY,
    value TEXT
);
