// Package llm 提供 LLM 提供者抽象层。
// 包含统一的请求/响应类型、流式增量类型、以及提供者接口定义。
// 所有后端 (OpenAI / Anthropic / Gemini / Bedrock) 都通过统一的 Provider 接口访问。
package llm

// ───────────────────────────── 消息角色 ─────────────────────────────

// MessageRole 定义消息的角色类型
type MessageRole string

const (
	RoleSystem    MessageRole = "system"
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleTool      MessageRole = "tool"
)

// ───────────────────────────── 统一消息格式 ─────────────────────────────

// Message 是统一的消息格式，兼容 OpenAI Chat Completions 结构。
// 所有提供者的消息在进入代理核心之前，都会被转换为这种格式。
type Message struct {
	Role             MessageRole `json:"role"`                        // 消息角色
	Content          string      `json:"content,omitempty"`           // 消息正文 (文本或工具调用组装)
	ToolCalls        []ToolCall  `json:"tool_calls,omitempty"`        // 助理消息中的工具调用列表
	ToolCallID       string      `json:"tool_call_id,omitempty"`      // 工具消息关联的工具调用 ID
	Name             string      `json:"name,omitempty"`              // 可选的参与者名称
	ReasoningContent string      `json:"reasoning_content,omitempty"` // 推理内容 (DeepSeek 等模型需要传回)
}

// ───────────────────────────── 工具调用 ─────────────────────────────

// ToolCall 表示模型发起的工具调用请求
type ToolCall struct {
	ID        string         `json:"id"`              // 工具调用唯一标识
	Name      string         `json:"name"`            // 工具名称
	Arguments string         `json:"arguments"`       // JSON 编码的工具参数
	Extra     map[string]any `json:"extra,omitempty"` // 提供者特定附加数据
}

// ToolSchema 描述工具的 JSON Schema 定义，用于提交给 LLM
type ToolSchema struct {
	Name        string `json:"name"`                  // 工具名称 (模型将使用此名称调用)
	Description string `json:"description,omitempty"` // 工具简短描述
	Parameters  any    `json:"parameters,omitempty"`  // JSON Schema 对象 (map[string]any)
}

// ───────────────────────────── 请求/响应 ─────────────────────────────

// ChatRequest 是统一的聊天补全请求，适用于所有提供者
type ChatRequest struct {
	Model       string         `json:"model"`                 // 模型名称
	Messages    []Message      `json:"messages"`              // 消息历史
	Tools       []ToolSchema   `json:"tools,omitempty"`       // 可用工具列表
	MaxTokens   int            `json:"max_tokens,omitempty"`  // 最大生成 token 数 (0 = 使用模型默认值)
	Temperature float64        `json:"temperature,omitempty"` // 采样温度 (0 = 使用模型默认值)
	Metadata    map[string]any `json:"-"`                     // 提供者特定附加参数 (不序列化到 JSON)
}

// ChatResponse 是统一的聊天补全响应，适用于所有提供者
type ChatResponse struct {
	ID           string      `json:"id"`                   // 响应的唯一标识
	Model        string      `json:"model"`                // 实际使用的模型名称
	Content      string      `json:"content,omitempty"`    // 文本回复内容
	ToolCalls    []ToolCall  `json:"tool_calls,omitempty"` // 工具调用列表
	StopReason   string      `json:"stop_reason"`          // 停止原因: "end_turn" / "max_tokens" / "tool_use" / "content_filter"
	Usage        *TokenUsage `json:"usage,omitempty"`      // token 用量统计
	Reasoning    string      `json:"reasoning,omitempty"`  // 推理/思维链文本 (Claude/Gemini 扩展思考)
	CachedPrompt bool        `json:"cached_prompt"`        // 是否命中了提示缓存
}

// StreamDelta 是流式响应的单个增量
type StreamDelta struct {
	Content    string      `json:"content,omitempty"`     // 当前增量文本
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`  // 完整的工具调用 (在流结束时填充)
	Reasoning  string      `json:"reasoning,omitempty"`   // 推理过程增量
	Usage      *TokenUsage `json:"usage,omitempty"`       // 累积 token 用量 (流式结束时填充)
	StopReason string      `json:"stop_reason,omitempty"` // 停止原因 (end_turn, tool_use, max_tokens, stop)
	Done       bool        `json:"done"`                  // 是否为最终增量
	Error      error       `json:"-"`                     // 发生的错误 (不序列化)
}

// TokenUsage 统计一次 API 调用的 token 用量
type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`                // 输入 token 数
	CompletionTokens int `json:"completion_tokens"`            // 输出 token 数
	TotalTokens      int `json:"total_tokens"`                 // 总 token 数
	CacheReadTokens  int `json:"cache_read_tokens,omitempty"`  // 缓存读取 token 数 (Anthropic / OpenAI)
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"` // 缓存写入 token 数 (Anthropic / OpenAI)
}

// ───────────────────────────── 模型信息 ─────────────────────────────

// ModelInfo 描述一个可用的模型
type ModelInfo struct {
	ID           string `json:"id"`            // 模型标识
	Provider     string `json:"provider"`      // 提供者名称
	ContextLimit int    `json:"context_limit"` // 上下文窗口大小 (token 数)
	MaxOutput    int    `json:"max_output"`    // 最大输出 token 数
	Vision       bool   `json:"vision"`        // 是否支持视觉
	Reasoning    bool   `json:"reasoning"`     // 是否支持推理/思维链
	Deprecated   bool   `json:"deprecated"`    // 是否已弃用
}

// ───────────────────────────── 停止原因常量 ─────────────────────────────

const (
	StopEndTurn       = "end_turn"       // 模型自然结束对话
	StopMaxTokens     = "max_tokens"     // 达到最大 token 限制
	StopToolUse       = "tool_use"       // 模型请求工具调用
	StopContentFilter = "content_filter" // 内容过滤器触发
	StopLength        = "length"         // 已达到长度限制 (兼容旧格式)
	StopToolCalls     = "tool_calls"     // 工具调用触发 (兼容旧格式)
)
