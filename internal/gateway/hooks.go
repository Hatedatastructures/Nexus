// Package gateway 提供消息钩子系统。
// 钩子函数可以在消息投递前后执行自定义逻辑，如内容过滤、平台特定格式化等。
// 支持通配符匹配的事件类型 (如 command:* 匹配所有 command:xxx 事件)。
package gateway

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"nexus-agent/internal/gateway/platforms"
)

// ───────────────────────────── 钩子类型 ─────────────────────────────

// HookType 表示钩子的执行阶段。
type HookType string

const (
	HookPreDispatch   HookType = "pre_dispatch"   // 消息投递前执行
	HookPostDelivery  HookType = "post_delivery"   // 消息投递后执行
	HookGatewayStart  HookType = "gateway:startup" // 网关启动时执行
	HookSessionStart  HookType = "session:start"   // 会话开始时执行
	HookSessionEnd    HookType = "session:end"     // 会话结束时执行
	HookSessionReset  HookType = "session:reset"   // 会话重置时执行
	HookAgentStart    HookType = "agent:start"     // Agent 调用开始时执行
	HookAgentStep     HookType = "agent:step"      // Agent 每步执行时执行
	HookAgentEnd      HookType = "agent:end"       // Agent 调用结束时执行
	HookPreToolCall   HookType = "pre_tool_call"   // 工具调用前执行
	HookPostToolCall  HookType = "post_tool_call"  // 工具调用后执行
	HookOnSessionEnd  HookType = "on_session_end"  // 会话结束时执行 (别名)
	HookOnPreCompress HookType = "on_pre_compress" // 压缩前执行
)

// Hook 是消息钩子函数。
// 接收 MessageEvent，可以修改消息内容或中止投递。
// 返回 nil error 表示钩子执行成功。
type Hook func(ctx context.Context, event *platforms.MessageEvent) (*platforms.MessageEvent, error)

// HookResult 钩子执行结果，用于 EmitCollect 模式。
type HookResult struct {
	HookType HookType
	Index    int
	Event    *platforms.MessageEvent
	Error    error
}

// ───────────────────────────── 钩子注册表 ─────────────────────────────

// HookRegistry 管理和执行消息钩子。
// 钩子按类型分组，按注册顺序执行。
// 支持通配符匹配: "command:*" 匹配所有 "command:xxx" 事件。
type HookRegistry struct {
	mu    sync.RWMutex
	hooks map[HookType][]Hook
}

// NewHookRegistry 创建空的钩子注册表。
func NewHookRegistry() *HookRegistry {
	return &HookRegistry{
		hooks: make(map[HookType][]Hook),
	}
}

// Register 注册一个钩子函数。
// hookType 指定执行阶段。支持通配符模式 (如 "command:*")。
func (r *HookRegistry) Register(hookType HookType, hook Hook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks[hookType] = append(r.hooks[hookType], hook)
	slog.Debug("registered hook", "type", string(hookType))
}

// Run 执行指定类型的所有钩子。
// 钩子按注册顺序依次执行，每个钩子的输出作为下一个钩子的输入。
// 如果任何钩子返回错误，后续钩子不再执行。
// 支持通配符匹配: 执行 "command:*" 会匹配所有 "command:xxx" 事件。
func (r *HookRegistry) Run(ctx context.Context, hookType HookType, event *platforms.MessageEvent) (*platforms.MessageEvent, error) {
	r.mu.RLock()
	hooks := r.getMatchingHooks(hookType)
	r.mu.RUnlock()

	if len(hooks) == 0 {
		return event, nil
	}

	current := event
	for i, hook := range hooks {
		var err error
		current, err = hook(ctx, current)
		if err != nil {
			slog.Warn("hook execution failed",
				"type", string(hookType),
				"index", i,
				"err", err,
			)
			return current, err
		}
		if current == nil {
			return nil, nil
		}
	}

	return current, nil
}

// EmitCollect 执行所有匹配的钩子并收集结果。
// 与 Run 不同，EmitCollect 不会在第一个错误时停止，而是收集所有结果。
// 用于决策风格的钩子 (如审批)。
func (r *HookRegistry) EmitCollect(ctx context.Context, hookType HookType, event *platforms.MessageEvent) []HookResult {
	r.mu.RLock()
	hooks := r.getMatchingHooks(hookType)
	r.mu.RUnlock()

	if len(hooks) == 0 {
		return nil
	}

	results := make([]HookResult, 0, len(hooks))
	current := event

	for i, hook := range hooks {
		result, err := hook(ctx, current)
		results = append(results, HookResult{
			HookType: hookType,
			Index:    i,
			Event:    result,
			Error:    err,
		})
		if result != nil {
			current = result
		}
	}

	return results
}

// getMatchingHooks 获取所有匹配的钩子（包括通配符匹配）。
func (r *HookRegistry) getMatchingHooks(hookType HookType) []Hook {
	var matched []Hook

	// 精确匹配
	if hooks, ok := r.hooks[hookType]; ok {
		matched = append(matched, hooks...)
	}

	// 通配符匹配: "command:*" 匹配 "command:xxx"
	hookTypeStr := string(hookType)
	for pattern, hooks := range r.hooks {
		patternStr := string(pattern)
		if patternStr != hookTypeStr && isWildcardMatch(patternStr, hookTypeStr) {
			matched = append(matched, hooks...)
		}
	}

	return matched
}

// isWildcardMatch 检查事件类型是否匹配通配符模式。
// 模式 "command:*" 匹配 "command:xxx"、"command:yyy" 等。
func isWildcardMatch(pattern, eventType string) bool {
	if !strings.HasSuffix(pattern, ":*") {
		return false
	}
	prefix := strings.TrimSuffix(pattern, "*")
	return strings.HasPrefix(eventType, prefix)
}

// Count 返回注册的钩子总数。
func (r *HookRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	total := 0
	for _, hooks := range r.hooks {
		total += len(hooks)
	}
	return total
}

// ListTypes 返回所有已注册的钩子类型。
func (r *HookRegistry) ListTypes() []HookType {
	r.mu.RLock()
	defer r.mu.RUnlock()
	types := make([]HookType, 0, len(r.hooks))
	for t := range r.hooks {
		types = append(types, t)
	}
	return types
}

// HasType 检查是否有指定类型的钩子注册。
func (r *HookRegistry) HasType(hookType HookType) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.hooks[hookType]
	if ok {
		return true
	}
	hookTypeStr := string(hookType)
	for pattern := range r.hooks {
		if isWildcardMatch(string(pattern), hookTypeStr) {
			return true
		}
	}
	return false
}
