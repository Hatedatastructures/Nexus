// Package gateway 提供消息钩子系统。
// 钩子函数可以在消息投递前后执行自定义逻辑，如内容过滤、平台特定格式化等。
package gateway

import (
	"context"
	"log/slog"

	"nexus-agent/internal/gateway/platforms"
)

// ───────────────────────────── 钩子类型 ─────────────────────────────

// HookType 表示钩子的执行阶段。
type HookType string

const (
	HookPreDispatch  HookType = "pre_dispatch"  // 消息投递前执行
	HookPostDelivery HookType = "post_delivery" // 消息投递后执行
)

// Hook 是消息钩子函数。
// 接收 MessageEvent，可以修改消息内容或中止投递。
// 返回 nil error 表示钩子执行成功。
type Hook func(ctx context.Context, event *platforms.MessageEvent) (*platforms.MessageEvent, error)

// ───────────────────────────── 钩子注册表 ─────────────────────────────

// HookRegistry 管理和执行消息钩子。
// 钩子按类型分组，按注册顺序执行。
type HookRegistry struct {
	hooks map[HookType][]Hook
}

// NewHookRegistry 创建空的钩子注册表。
func NewHookRegistry() *HookRegistry {
	return &HookRegistry{
		hooks: make(map[HookType][]Hook),
	}
}

// Register 注册一个钩子函数。
// hookType 指定执行阶段 (pre_dispatch / post_delivery)。
func (r *HookRegistry) Register(hookType HookType, hook Hook) {
	r.hooks[hookType] = append(r.hooks[hookType], hook)
	slog.Debug("registered hook", "type", string(hookType))
}

// Run 执行指定类型的所有钩子。
// 钩子按注册顺序依次执行，每个钩子的输出作为下一个钩子的输入。
// 如果任何钩子返回错误，后续钩子不再执行。
func (r *HookRegistry) Run(ctx context.Context, hookType HookType, event *platforms.MessageEvent) (*platforms.MessageEvent, error) {
	hooks, ok := r.hooks[hookType]
	if !ok || len(hooks) == 0 {
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
			// 钩子返回 nil 表示中止投递
			return nil, nil
		}
	}

	return current, nil
}

// Count 返回注册的钩子总数。
func (r *HookRegistry) Count() int {
	total := 0
	for _, hooks := range r.hooks {
		total += len(hooks)
	}
	return total
}
