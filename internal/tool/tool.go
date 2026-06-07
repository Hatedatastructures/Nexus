// Package tool 提供 AI 代理的工具系统核心抽象。
// 包含 Tool 接口、ToolSchema 定义、以及工具注册中心的公共类型。
// 每个工具文件通过 init() 函数自注册到全局注册中心。
package tool

import "context"

// ───────────────────────────── 工具接口 ─────────────────────────────

// Tool 是所有工具必须实现的核心接口。
// 每个工具负责一个独立的能力领域 (终端、浏览器、文件、网络等)。
type Tool interface {
	// Name 返回工具的唯一标识名。
	// 模型的 tool_calls 数组中的 name 字段使用此值。
	// 例如: "terminal", "web_search", "browser_navigate"
	Name() string

	// Description 返回工具的描述文本。
	// 此描述会被注入到系统提示词和工具 Schema 中，帮助模型选择合适的工具。
	Description() string

	// Schema 返回工具的 JSON Schema 结构。
	// 包含 name、description 和 parameters 字段。
	// parameters 遵循 JSON Schema 规范，使用 map[string]any 表达。
	Schema() *ToolSchema

	// Execute 执行工具逻辑。
	// args 是从模型工具调用中解析的参数键值对。
	// 无论成功或失败，始终返回 JSON 格式的字符串结果。
	// 所有工具结果格式: {"output": "...", ...} 或 {"error": "...", ...}
	Execute(ctx context.Context, args map[string]any) (string, error)

	// Toolset 返回此工具所属的工具集名称。
	// 工具集用于分组和启用/禁用控制。
	// 例如: "terminal", "web", "browser", "file", "memory"
	Toolset() string

	// IsAvailable 检测工具在当前环境下是否可用。
	// 通常检查: API key 是否存在、二进制文件是否在 PATH 中、系统能力等。
	// 返回 false 的工具不会被发送给模型。
	IsAvailable() bool

	// Emoji 返回工具的展示图标 (可选)。
	// 用于终端 UI 和日志中的快速识别。
	Emoji() string

	// MaxResultChars 返回工具结果的最大字符数。
	// 返回 0 表示使用系统默认值 (通常 50000)。
	// 超过此限制的结果会被截断。
	MaxResultChars() int
}

// ───────────────────────────── 工具 Schema ─────────────────────────────

// ToolSchema 定义工具的 JSON Schema 结构体，用于提交给 LLM。
type ToolSchema struct {
	Name        string `json:"name"`                  // 工具名称
	Description string `json:"description,omitempty"` // 工具描述
	Parameters  any    `json:"parameters"`            // JSON Schema 对象 (map[string]any)
}

// ───────────────────────────── 工具条目 ─────────────────────────────

// ToolEntry 是注册中心存储的工具条目。
type ToolEntry struct {
	Tool           Tool // 工具实例
	IsAsync        bool // 是否需要异步执行包装
	MaxResultChars int  // 结果最大字符数 (0 = 默认)
}

// ───────────────────────────── 工具集信息 ─────────────────────────────

// ToolsetInfo 描述一个工具集的元信息
type ToolsetInfo struct {
	Name        string   // 工具集名称
	Description string   // 工具集描述
	Tools       []string // 包含的工具名称列表
}
