// Package hooks 提供可复用的 Shell Hook 系统。
// 允许用户通过 shell 脚本拦截和控制工具调用行为。
// 使用 JSON stdin/stdout wire protocol 与 hook 脚本通信。
//
// 本包设计为独立于 agent 包，可被 gateway、skill 等其他模块复用。
package hooks

import (
	"context"
	"fmt"

	pkgerrors "nexus-agent/internal/errors"
	"regexp"
)

// ───────────────────────────── 事件常量 ─────────────────────────────

const (
	// EventPreToolCall 在工具调用前触发。
	EventPreToolCall = "pre_tool_call"
	// EventPostToolCall 在工具调用后触发。
	EventPostToolCall = "post_tool_call"
)

// ───────────────────────────── 数据结构 ─────────────────────────────

// HookEvent 表示发送给 hook 脚本的事件。
type HookEvent struct {
	EventName  string         `json:"event_name"`
	ToolName   string         `json:"tool_name"`
	ToolInput  map[string]any `json:"tool_input"`
	ToolOutput string         `json:"tool_output,omitempty"` // post_tool_call 时填充
	SessionID  string         `json:"session_id"`
	CWD        string         `json:"cwd"`
}

// HookResponse 表示 hook 脚本的响应。
type HookResponse struct {
	Decision string `json:"decision"` // allow / block / modify
	Reason   string `json:"reason"`   // 阻止原因 (block 时)
	Message  string `json:"message"`  // 替换消息 (modify 时)
}

// IsBlock 返回是否阻止工具调用。
func (r *HookResponse) IsBlock() bool {
	return r.Decision == "block"
}

// IsModify 返回是否修改工具输入。
func (r *HookResponse) IsModify() bool {
	return r.Decision == "modify"
}

// ───────────────────────────── HookSpec 配置结构 ─────────────────────────────

// HookSpec 定义一个 Shell Hook 的配置规格。
// 用于从 YAML/JSON 配置文件反序列化。
type HookSpec struct {
	Event      string `yaml:"event"      json:"event"`   // 事件类型: pre_tool_call / post_tool_call
	Command    string `yaml:"command"    json:"command"` // hook 脚本路径
	Matcher    string `yaml:"matcher"    json:"matcher"` // 工具名匹配正则 (空 = 匹配所有)
	TimeoutSec int    `yaml:"timeout"    json:"timeout"` // 超时秒数 (默认 60, 最大 300)
}

// ───────────────────────────── Hook 接口 ─────────────────────────────

// Hook 是单个 hook 的抽象接口。
// 实现者必须提供名称、事件类型、匹配逻辑和执行逻辑。
type Hook interface {
	// Name 返回 hook 的唯一名称，用于日志和 allowlist。
	Name() string

	// Event 返回 hook 监听的事件类型 (EventPreToolCall / EventPostToolCall)。
	Event() string

	// Match 判断此 hook 是否匹配给定的工具名。
	// 返回 true 表示应对此工具调用执行此 hook。
	Match(toolName string) bool

	// Execute 执行 hook 逻辑。
	// 对于 pre_tool_call 事件，返回的 HookResponse 决定是否允许/阻止/修改工具调用。
	// 对于 post_tool_call 事件，返回的 HookResponse 仅用于日志记录。
	Execute(ctx context.Context, event *HookEvent) (*HookResponse, error)
}

// ───────────────────────────── Manager 接口 ─────────────────────────────

// Manager 是 hook 管理器的抽象接口。
// 负责 hook 注册、匹配和链式执行。
type Manager interface {
	// Register 注册一个 hook。
	// hook 按注册顺序排列，执行时按注册顺序依次执行。
	Register(hook Hook) error

	// ExecutePreHooks 执行所有匹配的 pre_tool_call hook。
	// 返回:
	//   - *HookResponse: 最终的 hook 响应 (可能为 nil)
	//   - bool: 是否应阻止工具调用
	//   - error: 执行错误
	//
	// 链式语义: 按注册顺序执行，首个返回 block 的 hook 终止链。
	ExecutePreHooks(ctx context.Context, toolName string, input map[string]any) (*HookResponse, bool, error)

	// ExecutePostHooks 执行所有匹配的 post_tool_call hook。
	// post hook 链不会被中断，所有匹配的 hook 都会执行。
	ExecutePostHooks(ctx context.Context, toolName string, input map[string]any, output string) error
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

// CompileMatcher 编译工具名匹配正则。
// 空字符串返回 nil，表示匹配所有工具。
func CompileMatcher(pattern string) (*regexp.Regexp, error) {
	if pattern == "" {
		return nil, nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ConfigInvalid, "编译 hook matcher 失败", err)
	}
	return re, nil
}

// ValidateEvent 验证事件类型是否合法。
func ValidateEvent(event string) error {
	switch event {
	case EventPreToolCall, EventPostToolCall:
		return nil
	default:
		return pkgerrors.New(pkgerrors.ConfigInvalid, fmt.Sprintf("不支持的 hook 事件类型: %q (支持: pre_tool_call, post_tool_call)", event))
	}
}
