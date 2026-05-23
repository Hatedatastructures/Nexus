// Package agent 提供 AI 代理的核心实现。
// AIAgent 结构体管理对话循环、工具调用分发、状态更新和故障恢复。
// 这是 Nexus Agent 的中央编排器。
package agent

import (
	"log/slog"
	"sync"

	"nexus-agent/internal/approval"
	"nexus-agent/internal/config"
	"nexus-agent/internal/context"
	"nexus-agent/internal/credential"
	"nexus-agent/internal/llm"
	"nexus-agent/internal/memory"
	"nexus-agent/internal/sandbox"
	"nexus-agent/internal/skill"
	"nexus-agent/internal/state"
	"nexus-agent/internal/tool"
)

// ───────────────────────────── AIAgent 结构体 ─────────────────────────────

// AIAgent 是对话代理的核心结构体。
// 管理一次会话的完整生命周期: 系统提示词构建 → LLM 调用 → 工具执行 → 状态更新。
type AIAgent struct {
	// ── LLM 配置 ──
	provider      llm.Provider // LLM 提供者实例
	model         string       // 模型名称
	maxTokens     int          // 最大生成 token 数
	reasoningCfg  *ReasoningConfig // 推理/思维链配置

	// ── 子系统 ──
	registry        *tool.Registry      // 工具注册中心
	memoryManager   *memory.Manager     // 记忆管理器
	skillManager    *skill.Manager      // 技能管理器
	contextBuilder  *context.Builder    // 系统提示词构建器
	compressor      *context.Compressor // 上下文压缩器
	state           *state.Store              // 持久化存储
	persister       *state.SessionPersister   // 会话 JSONL 持久化
	credentialPool  *credential.Pool    // 凭证池
	approvalChecker *approval.Checker   // 命令审批
	sandboxEnv      sandbox.Environment // 沙箱环境
	fileSafety      *FileSafetyChecker  // 文件写入安全检查

	// ── 会话管理 ──
	sessionID string // 会话唯一标识
	platform  string // 平台: "cli" / "telegram" / "discord" ...
	userID    string // 用户 ID (网关会话)
	chatID    string // 聊天 ID (网关会话)

	// ── 回调函数 ──
	streamCallback    func(delta string)          // 文本增量回调
	toolCallback      func(name string, args map[string]any) // 工具进度回调
	statusCallback    func(msg string)            // 状态消息回调
	reasoningCallback func(reasoning string)      // 推理过程回调

	// ── 内部状态 ──
	mu                sync.Mutex           // 并发保护
	iterationBudget   *IterationBudget     // 迭代预算
	guardrails        *ToolCallGuardrails  // 工具调用安全护栏
		recoveryEngine    *RecoveryEngine      // 错误恢复引擎
	cachedSystemPrompt string              // 缓存的系统提示词
	messages          []llm.Message        // 当前对话消息列表
	maxRetries        int                  // 最大重试次数
	fallbackModel     string               // 备选模型
	fallbackProvider  llm.Provider         // 备选提供者
	router            *ProviderRouter      // 多提供者路由器 (优先级/健康检查)
	fallbackChain     *FallbackChain       // 回退链 (主提供者失败后的降级路径)
	pendingFallbackChain []config.FallbackEntryConfig // 待构建的回退链配置 (延迟解析)
	resumeMode        bool                 // 是否从历史会话恢复
}

// ───────────────────────────── 构造函数 ─────────────────────────────

// NewAgent 创建 AIAgent 实例。
// 使用函数式选项模式配置。
// 默认启用工具调用安全护栏 (可通过 WithGuardrails(nil) 禁用)。
func NewAgent(opts ...AgentOption) *AIAgent {
	a := &AIAgent{
		model:           "claude-sonnet-4-20250514",
		maxTokens:       4096,
		iterationBudget: NewIterationBudget(90),
		guardrails:      NewToolCallGuardrails(),
		recoveryEngine:  NewRecoveryEngine(),
		maxRetries:      3,
	}

	// 应用所有选项
	for _, opt := range opts {
		opt(a)
	}

	return a
}

// ───────────────────────────── 配置解析 ─────────────────────────────

// DefaultAgentFromConfig 从配置对象创建默认代理。
// 用于快速启动场景。
func DefaultAgentFromConfig(cfg *config.AgentConfig) *AIAgent {
	return NewAgent(WithConfig(cfg))
}

// Provider 返回当前 LLM 提供者 (用于外部检查)。
func (a *AIAgent) Provider() llm.Provider {
	return a.provider
}

// InitRouter 初始化多提供者路由器。
// 在所有 With* 选项应用后调用，使用已注册的提供者构建路由表。
func (a *AIAgent) InitRouter(entries []*ProviderEntry) {
	if len(entries) == 0 {
		return
	}
	a.router = NewProviderRouter(entries)
	slog.Info("AIAgent: ProviderRouter initialized", "entry_count", len(entries))
}

// InitFallbackChain 从延迟解析的配置构建回退链。
// providerMap 应包含所有可用的 LLM 提供者实例 (key = 提供者名称)。
// 通常在 WithConfigProvider 完成后调用。
func (a *AIAgent) InitFallbackChain(providerMap map[string]llm.Provider) {
	if len(a.pendingFallbackChain) == 0 {
		return
	}

	entries := make([]*FallbackEntry, 0, len(a.pendingFallbackChain))
	for _, ec := range a.pendingFallbackChain {
		entries = append(entries, &FallbackEntry{
			Provider: ec.Provider,
			Model:    ec.Model,
			Priority: ec.Priority,
		})
	}
	a.fallbackChain = NewFallbackChain(entries, providerMap)
	a.pendingFallbackChain = nil // 清理，避免重复初始化
}

// Router 返回多提供者路由器 (用于外部检查或高级配置)。
func (a *AIAgent) Router() *ProviderRouter {
	return a.router
}

// FallbackChain 返回回退链 (用于外部检查)。
func (a *AIAgent) FallbackChain() *FallbackChain {
	return a.fallbackChain
}

// Shutdown 清理 AIAgent 持有的资源。
// 在代理实例被缓存驱逐或会话结束时调用。
func (a *AIAgent) Shutdown() {
	a.mu.Lock()
	defer a.mu.Unlock()

	// 清空内部消息缓存
	a.messages = nil
	a.cachedSystemPrompt = ""
}
