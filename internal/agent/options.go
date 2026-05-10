// Package agent 提供 AI 代理的构建选项。
// 使用函数式选项模式 (Functional Options Pattern) 配置 AIAgent。
// 每个 With* 函数返回一个 AgentOption，用于在 NewAgent 中设置对应字段。
package agent

import (
	"fmt"
	"net/http"
	"time"

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

// ───────────────────────────── 迭代预算 ─────────────────────────────

// IterationBudget 跟踪代理主循环的工具调用迭代次数。
// 每次工具调用消耗一次预算，防止无限循环。
type IterationBudget struct {
	max      int // 最大迭代次数 (默认 90)
	consumed int // 已消耗次数
}

// NewIterationBudget 创建迭代预算
func NewIterationBudget(max int) *IterationBudget {
	if max <= 0 {
		max = 90
	}
	return &IterationBudget{max: max}
}

// Consume 消耗一次迭代。返回 true 表示还有余额。
func (b *IterationBudget) Consume() bool {
	if b.consumed >= b.max {
		return false
	}
	b.consumed++
	return true
}

// Remaining 返回剩余可用迭代次数
func (b *IterationBudget) Remaining() int {
	remaining := b.max - b.consumed
	if remaining < 0 {
		return 0
	}
	return remaining
}

// Consumed 返回已消耗的迭代次数
func (b *IterationBudget) Consumed() int {
	return b.consumed
}

// ───────────────────────────── 推理配置 ─────────────────────────────

// ReasoningConfig 定义模型的推理/思维链配置
type ReasoningConfig struct {
	Effort      string // 推理努力程度: "low" / "medium" / "high" (OpenAI) 或 "" (Anthropic budget 模式)
	BudgetTokens int   // 推理 token 预算 (Anthropic: 1024-64000, 0 = 自动)
	Enabled     bool   // 是否启用推理
}

// ───────────────────────────── 对话结果 ─────────────────────────────

// TurnResult 是对话循环的完整返回结果
type TurnResult struct {
	FinalResponse string        // 最终回复文本
	Messages      []llm.Message // 完整消息历史 (包括本轮新增)
	APICalls      int           // API 调用次数
	ToolCalls     int           // 工具调用总数
	TotalTokens   int64         // 总 token 用量
	CachedTokens  int64         // 缓存命中 token 数
	CostUSD       float64       // 估算费用 (美元)
	Duration      time.Duration // 总耗时
	Completed     bool          // 是否正常完成 (false = 被中断或出错)
	Error         error         // 终止错误 (nil = 正常)
}

// ───────────────────────────── 函数式选项 ─────────────────────────────

// AgentOption 是 AIAgent 的函数式配置选项
type AgentOption func(*AIAgent)

// WithProvider 设置 LLM 提供者
func WithProvider(p llm.Provider) AgentOption {
	return func(a *AIAgent) { a.provider = p }
}

// WithModel 设置模型名称
func WithModel(model string) AgentOption {
	return func(a *AIAgent) { a.model = model }
}

// WithMaxTokens 设置最大生成 token 数
func WithMaxTokens(n int) AgentOption {
	return func(a *AIAgent) { a.maxTokens = n }
}

// WithMaxIterations 设置最大工具调用迭代次数
func WithMaxIterations(n int) AgentOption {
	return func(a *AIAgent) { a.iterationBudget = NewIterationBudget(n) }
}

// WithReasoningConfig 设置推理/思维链配置
func WithReasoningConfig(cfg *ReasoningConfig) AgentOption {
	return func(a *AIAgent) { a.reasoningCfg = cfg }
}

// WithStreamCallback 设置流式文本增量回调
func WithStreamCallback(fn func(delta string)) AgentOption {
	return func(a *AIAgent) { a.streamCallback = fn }
}

// WithToolCallback 设置工具执行进度回调
func WithToolCallback(fn func(name string, args map[string]any)) AgentOption {
	return func(a *AIAgent) { a.toolCallback = fn }
}

// WithStatusCallback 设置状态消息回调
func WithStatusCallback(fn func(msg string)) AgentOption {
	return func(a *AIAgent) { a.statusCallback = fn }
}

// WithReasoningCallback 设置推理过程增量回调
func WithReasoningCallback(fn func(reasoning string)) AgentOption {
	return func(a *AIAgent) { a.reasoningCallback = fn }
}

// WithClarifyCallback 设置澄清工具回调。
// 用于在不确定用户意图时请求澄清或决策。
func WithClarifyCallback(fn func(question string, choices []string) string) AgentOption {
	return func(a *AIAgent) {
		// 设置到工具包的全局回调
		tool.SetClarifyCallback(fn)
	}
}

// WithToolRegistry 设置工具注册中心
func WithToolRegistry(r *tool.Registry) AgentOption {
	return func(a *AIAgent) { a.registry = r }
}

// WithMemoryManager 设置记忆管理器
func WithMemoryManager(m *memory.Manager) AgentOption {
	return func(a *AIAgent) { a.memoryManager = m }
}

// WithSkillManager 设置技能管理器
func WithSkillManager(s *skill.Manager) AgentOption {
	return func(a *AIAgent) { a.skillManager = s }
}

// WithContextBuilder 设置系统提示词构建器
func WithContextBuilder(b *context.Builder) AgentOption {
	return func(a *AIAgent) { a.contextBuilder = b }
}

// WithCompressor 设置上下文压缩器
func WithCompressor(c *context.Compressor) AgentOption {
	return func(a *AIAgent) { a.compressor = c }
}

// WithState 设置状态持久化存储
func WithStateStore(s *state.Store) AgentOption {
	return func(a *AIAgent) { a.state = s }
}

// WithCredentialPool 设置凭证池
func WithCredentialPool(p *credential.Pool) AgentOption {
	return func(a *AIAgent) { a.credentialPool = p }
}

// WithApprovalChecker 设置命令审批检查器
func WithApprovalChecker(c *approval.Checker) AgentOption {
	return func(a *AIAgent) { a.approvalChecker = c }
}

// WithSandboxEnv 设置终端沙箱环境
func WithSandboxEnv(e sandbox.Environment) AgentOption {
	return func(a *AIAgent) { a.sandboxEnv = e }
}

// WithFileSafety 设置文件写入安全检查器。
// 用于在工具 dispatch 层面对 file_write/file_edit/patch 操作进行二次防护。
func WithFileSafety(fs *FileSafetyChecker) AgentOption {
	return func(a *AIAgent) { a.fileSafety = fs }
}

// WithSessionID 设置会话 ID
func WithSessionID(id string) AgentOption {
	return func(a *AIAgent) { a.sessionID = id }
}

// WithPlatform 设置运行平台标识
func WithPlatform(p string) AgentOption {
	return func(a *AIAgent) { a.platform = p }
}

// WithUserID 设置用户 ID (网关会话)
func WithUserID(id string) AgentOption {
	return func(a *AIAgent) { a.userID = id }
}

// WithChatID 设置聊天 ID (网关会话)
func WithChatID(id string) AgentOption {
	return func(a *AIAgent) { a.chatID = id }
}

// WithFallbackModel 设置备选故障转移模型
func WithFallbackModel(model string) AgentOption {
	return func(a *AIAgent) { a.fallbackModel = model }
}

// WithFallbackProvider 设置备选故障转移提供者
func WithFallbackProvider(p llm.Provider) AgentOption {
	return func(a *AIAgent) { a.fallbackProvider = p }
}

// WithGuardrails 设置工具调用安全护栏
func WithGuardrails(g *ToolCallGuardrails) AgentOption {
	return func(a *AIAgent) { a.guardrails = g }
}

// WithRouter 设置多提供者路由器。
// 当主提供者重试失败后，会委托 Router 进行按优先级的提供者切换。
func WithRouter(r *ProviderRouter) AgentOption {
	return func(a *AIAgent) { a.router = r }
}

// WithFallbackChain 设置回退链。
// 当 Router 也失败（或未配置 Router）时，按回退链优先级尝试备选提供者。
func WithFallbackChain(fc *FallbackChain) AgentOption {
	return func(a *AIAgent) { a.fallbackChain = fc }
}

// ───────────────────────────── 回调设置器 ─────────────────────────────

// SetStreamCallback 设置流式文本增量回调 (用于在缓存代理上动态设置)
func (a *AIAgent) SetStreamCallback(fn func(delta string)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.streamCallback = fn
}

// SetToolCallback 设置工具执行进度回调
func (a *AIAgent) SetToolCallback(fn func(name string, args map[string]any)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.toolCallback = fn
}

// SetStatusCallback 设置状态消息回调
func (a *AIAgent) SetStatusCallback(fn func(msg string)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.statusCallback = fn
}

// SetReasoningCallback 设置推理过程增量回调
func (a *AIAgent) SetReasoningCallback(fn func(reasoning string)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reasoningCallback = fn
}

// SetClarifyCallback 设置澄清工具回调。
// 同时设置到全局工具注册中心，供 ClarifyTool 使用。
func (a *AIAgent) SetClarifyCallback(fn func(question string, choices []string) string) {
	// 设置到工具包的全局回调
	tool.SetClarifyCallback(fn)
}

// WithConfig 从配置对象批量设置选项
func WithConfig(cfg *config.AgentConfig) AgentOption {
	return func(a *AIAgent) {
		if cfg.Model != "" {
			a.model = cfg.Model
		}
		if cfg.MaxTokens > 0 {
			a.maxTokens = cfg.MaxTokens
		}
		if cfg.MaxIterations > 0 {
			a.iterationBudget = NewIterationBudget(cfg.MaxIterations)
		}
		if cfg.FallbackModel != "" {
			a.fallbackModel = cfg.FallbackModel
		}
		// 回退链配置仅保存到 pendingFallbackChain，实际构建需要 providerMap
		if len(cfg.FallbackChain) > 0 {
			a.pendingFallbackChain = cfg.FallbackChain
		}
	}
}

// WithConfigProvider 从完整配置创建 LLM Provider 并注入。
// 需要额外传入 config.Config (而非 config.AgentConfig) 以便读取 Providers 配置。
func WithConfigProvider(cfg *config.Config) AgentOption {
	return func(a *AIAgent) {
		// 确定模型名称 (优先使用配置中的)
		if cfg.Agent.Model != "" {
			a.model = cfg.Agent.Model
		}

		// 确定提供者名称
		providerName := cfg.Agent.Provider
		if providerName == "" {
			// 从模型配置推断
			if mc, ok := cfg.ResolveModel(a.model); ok && mc.Provider != "" {
				providerName = mc.Provider
			} else {
				// 默认尝试第一个已配置的提供者
				for k := range cfg.Providers {
					providerName = k
					break
				}
			}
		}
		if providerName == "" {
			return // 无可用提供者
		}

		pc, err := cfg.ResolveProvider(providerName)
		if err != nil {
			return
		}

		provider, err := buildProviderFromConfig(providerName, pc)
		if err != nil {
			return
		}

		a.provider = provider
	}
}

// buildProviderFromConfig 根据 API 模式创建对应的 LLM Provider。
func buildProviderFromConfig(name string, pc config.ProviderConfig) (llm.Provider, error) {
	if pc.APIKey == "" {
		return nil, fmt.Errorf("提供者 %s 的 API Key 未设置", name)
	}

	httpClient := &http.Client{Timeout: 60 * time.Second}

	var p llm.Provider
	switch pc.APIMode {
	case "openai", "chat_completions":
		p = llm.NewOpenAIProvider(httpClient, pc.APIKey, "", pc.BaseURL)
	case "anthropic", "anthropic_messages":
		p = llm.NewAnthropicProvider(httpClient, pc.APIKey, "", pc.BaseURL)
	case "gemini":
		p = llm.NewGeminiProvider(httpClient, pc.APIKey, "", pc.BaseURL)
	case "bedrock", "bedrock_converse":
		p = llm.NewBedrockProvider(httpClient, pc.APIKey, "", "")
	default:
		// 默认使用 OpenAI 兼容模式
		p = llm.NewOpenAIProvider(httpClient, pc.APIKey, "", pc.BaseURL)
	}

	return p, nil
}
