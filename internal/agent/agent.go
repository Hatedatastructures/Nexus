// Package agent 提供 AI 代理的核心实现。
// AIAgent 结构体管理对话循环、工具调用分发、状态更新和故障恢复。
// 这是 Nexus Agent 的中央编排器。
package agent

import (
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
	state           *state.Store        // 持久化存储
	credentialPool  *credential.Pool    // 凭证池
	approvalChecker *approval.Checker   // 命令审批
	sandboxEnv      sandbox.Environment // 沙箱环境

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
	mu                sync.Mutex       // 并发保护
	iterationBudget   *IterationBudget // 迭代预算
	cachedSystemPrompt string          // 缓存的系统提示词
	messages          []llm.Message    // 当前对话消息列表
	maxRetries        int              // 最大重试次数
	fallbackModel     string           // 备选模型
	fallbackProvider  llm.Provider     // 备选提供者
}

// ───────────────────────────── 构造函数 ─────────────────────────────

// NewAgent 创建 AIAgent 实例。
// 使用函数式选项模式配置。
func NewAgent(opts ...AgentOption) *AIAgent {
	a := &AIAgent{
		model:           "claude-sonnet-4-20250514",
		maxTokens:       4096,
		iterationBudget: NewIterationBudget(90),
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
