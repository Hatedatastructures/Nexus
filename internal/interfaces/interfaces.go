package interfaces

import (
	"context"
	"database/sql"
	"time"

	"nexus-agent/internal/approval"
	"nexus-agent/internal/credential"
	ictx "nexus-agent/internal/context"
	"nexus-agent/internal/llm"
	"nexus-agent/internal/memory"
	"nexus-agent/internal/skill"
	"nexus-agent/internal/state"
	"nexus-agent/internal/tool"
)

// ───────────────────────────── 外部依赖接口 ─────────────────────────────
// 这些接口抽象 AIAgent 所依赖的外部包具体类型。
// agent 包内部的类型 (ProviderRouter, FallbackChain, ToolCallGuardrails,
// RecoveryEngine, FileSafetyChecker 等) 保留为具体类型以避免循环导入。

// ToolRegistry 是工具注册中心的抽象接口。
// 对应具体类型: *tool.Registry
type ToolRegistry interface {
	Register(t tool.Tool)
	Dispatch(ctx context.Context, name string, args map[string]any) (string, error)
	GetDefinitions(toolNames []string) []*tool.ToolSchema
	GetEntry(name string) *tool.ToolEntry
	ListTools() []string
	RegisterToolsetAlias(alias, canonical string)
}

// MemoryManager 是记忆管理器的抽象接口。
// 对应具体类型: *memory.Manager
type MemoryManager interface {
	SetExternal(p memory.Provider)
	SystemPromptBlock() string
	PrefetchAll(ctx context.Context, query string) (string, error)
	SyncAll(ctx context.Context, userContent, assistantContent string) error
	GetToolSchemas() []llm.ToolSchema
	HandleToolCall(ctx context.Context, toolName string, args map[string]any) (string, error)
	InitializeAll(ctx context.Context, sessionID string) error
	ShutdownAll(ctx context.Context) error
}

// SkillManager 是技能管理器的抽象接口。
// 对应具体类型: *skill.Manager
type SkillManager interface {
	GetActiveSkills() []*skill.Skill
	GetActiveSkillsIndex() string
	LoadAll(ctx context.Context) error
	Get(name string) (*skill.Skill, error)
	Create(s *skill.Skill) error
	Update(name string, skill *skill.Skill) error
	Delete(name string) error
	Install(ctx context.Context, source, identifier string) error
	Disable(name string)
	Enable(name string)
}

// ContextBuilder 是系统提示词构建器的抽象接口。
// 对应具体类型: *context.Builder
type ContextBuilder interface {
	Build(ctx context.Context, opts *ictx.BuildOptions) (string, error)
}

// ContextCompressor 是上下文压缩器的抽象接口。
// 对应具体类型: *context.Compressor
type ContextCompressor interface {
	SetAuxProvider(p llm.Provider)
	TailTokenBudget() int
	SetThresholdPercent(pct float64)
	ShouldCompress(contextLimit, totalTokens int) bool
	Compress(ctx context.Context, messages []llm.Message, auxProvider llm.Provider, focusTopic string) ([]llm.Message, error)
}

// StateStore 是状态持久化存储的抽象接口。
// 对应具体类型: *state.Store
type StateStore interface {
	Close() error
	DB() *sql.DB
	CreateSession(ctx context.Context, session *state.Session) error
	GetSession(ctx context.Context, id string) (*state.Session, error)
	UpdateSession(ctx context.Context, session *state.Session) error
	EndSession(ctx context.Context, id string, reason string) error
	ListSessions(ctx context.Context, filter *state.SessionFilter) ([]*state.Session, error)
	GetCompressionTip(ctx context.Context, id string) (*state.Session, error)
	InsertMessage(ctx context.Context, msg *state.MessageRecord) error
	InsertMessagesBatch(ctx context.Context, msgs []*state.MessageRecord) error
	GetMessages(ctx context.Context, sessionID string, limit, offset int) ([]*state.MessageRecord, error)
	GetMessageCount(ctx context.Context, sessionID string) (int, error)
	CreateFTSTables(ctx context.Context) error
	SearchMessages(ctx context.Context, query string, limit int) ([]*state.SearchResult, error)
	ListRecentSessions(ctx context.Context, limit int) ([]*state.Session, error)
	AutoPrune(ctx context.Context, maxAgeDays int) (int, error)
	CheckpointWAL(ctx context.Context) error
}

// SessionPersister 是会话 JSONL 持久化器的抽象接口。
// 对应具体类型: *state.SessionPersister
type SessionPersister interface {
	Open() error
	RecordSessionMeta(session *state.Session) error
	RecordMessage(msg *state.MessageRecord) error
	RecordCompaction(messageCount int, tokensSaved int) error
	RecordPromptHistory(prompt string) error
	Close() error
}

// CredentialPool 是凭证池的抽象接口。
// 对应具体类型: *credential.Pool
type CredentialPool interface {
	SetStrategy(strategy string)
	SetExhaustCooldown(d time.Duration)
	ApplyConfig(cfg credential.CredentialConfig)
	Add(cred credential.Credential)
	Select() (credential.Credential, bool)
	MarkExhausted(ctx context.Context, statusCode int, errorMsg string) (credential.Credential, bool)
	Count() int
	Credentials() []credential.Credential
	UseCounts() []int
	MarshalJSON() ([]byte, error)
}

// ApprovalChecker 是命令审批检查器的抽象接口。
// 对应具体类型: *approval.Checker
type ApprovalChecker interface {
	Mode() string
	SetMode(mode string) bool
	Check(ctx context.Context, command string) (approval.Result, string)
	CheckTool(ctx context.Context, toolName string, args map[string]any) (approval.Result, string)
}
