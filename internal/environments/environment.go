// Package environments 提供 Agent 运行环境的抽象和具体实现。
// 环境定义了 Agent 可以执行的操作范围、观察结果的形式以及任务完成的评判标准。
// 不同的环境适用于不同类型的任务（如网页研究、软件工程、数据分析等）。
package environments

import (
	"context"
	"fmt"
	"log/slog"
)

// ───────────────────────────── 观察与动作 ─────────────────────────────

// Observation 表示环境在一次交互后返回的观察结果。
// 包含当前状态、奖励信号和是否完成的标志。
type Observation struct {
	// State 环境的当前状态描述
	State string
	// Reward 奖励值，用于评估动作质量 (范围: -1.0 到 1.0)
	Reward float64
	// Done 任务是否已完成
	Done bool
	// Info 附加信息，包含元数据和调试数据
	Info map[string]any
}

// Action 表示 Agent 向环境执行的动作。
type Action struct {
	// Type 动作类型，如 "read"、"write"、"search"、"test" 等
	Type string
	// Parameters 动作参数，键值对形式
	Parameters map[string]any
}

// ───────────────────────────── 环境接口 ─────────────────────────────

// Environment 定义了 Agent 运行环境的通用接口。
// 每个具体环境实现此接口以提供特定领域的交互能力。
type Environment interface {
	// Execute 执行一个动作并返回观察结果。
	// ctx 用于控制超时和取消。
	Execute(ctx context.Context, action Action) (*Observation, error)

	// Reset 将环境重置到初始状态。
	// 应在开始新任务时调用。
	Reset(ctx context.Context) error

	// Step 执行环境内部的一步推进逻辑。
	// 适用于需要分阶段推进的复杂任务。
	Step(ctx context.Context) (*Observation, error)

	// Render 返回环境的可渲染状态描述（用于系统提示词）。
	Render() string
}

// ───────────────────────────── 基础环境 ─────────────────────────────

// BaseEnvironment 提供了 Environment 接口的默认实现。
// 具体环境可以通过内嵌此结构体复用通用逻辑。
type BaseEnvironment struct {
	// Name 环境名称
	Name string
	// Description 环境描述
	Description string
	// state 内部状态
	state string
	// history 动作历史
	history []Action
	// done 是否已完成
	done bool
}

// NewBaseEnvironment 创建基础环境实例。
func NewBaseEnvironment(name, description string) *BaseEnvironment {
	return &BaseEnvironment{
		Name:        name,
		Description: description,
		state:       "initialized",
		history:     make([]Action, 0),
		done:        false,
	}
}

// Execute 执行动作。基础实现仅记录动作历史，返回默认观察。
// 具体环境应重写此方法。
func (b *BaseEnvironment) Execute(ctx context.Context, action Action) (*Observation, error) {
	// 检查 context 是否已取消
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("环境执行被取消: %w", ctx.Err())
	default:
	}

	// 记录动作历史
	b.history = append(b.history, action)

	// 更新状态
	b.state = fmt.Sprintf("executing: %s", action.Type)

	slog.Debug("environment: executing action",
		"name", b.Name,
		"action_type", action.Type,
		"history_len", len(b.history),
	)

	return &Observation{
		State:  b.state,
		Reward: 0.0,
		Done:   b.done,
		Info: map[string]any{
			"environment": b.Name,
			"step":        len(b.history),
		},
	}, nil
}

// Reset 将环境重置到初始状态。
func (b *BaseEnvironment) Reset(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return fmt.Errorf("环境重置被取消: %w", ctx.Err())
	default:
	}

	b.state = "reset"
	b.history = make([]Action, 0)
	b.done = false

	slog.Info("environment: reset", "name", b.Name)
	return nil
}

// Step 执行一步推进逻辑。基础实现返回当前状态。
func (b *BaseEnvironment) Step(ctx context.Context) (*Observation, error) {
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("环境步进被取消: %w", ctx.Err())
	default:
	}

	slog.Debug("environment: step", "name", b.Name, "state", b.state)
	return &Observation{
		State:  b.state,
		Reward: 0.0,
		Done:   b.done,
		Info: map[string]any{
			"environment": b.Name,
		},
	}, nil
}

// Render 返回环境的可渲染状态描述。
func (b *BaseEnvironment) Render() string {
	return fmt.Sprintf("Environment: %s\nDescription: %s\nState: %s\nSteps: %d\nDone: %v",
		b.Name, b.Description, b.state, len(b.history), b.done,
	)
}

// MarkDone 标记环境任务已完成（供子类调用）。
func (b *BaseEnvironment) MarkDone() {
	b.done = true
}

// State 获取当前状态字符串。
func (b *BaseEnvironment) State() string {
	return b.state
}

// SetState 设置当前状态字符串。
func (b *BaseEnvironment) SetState(s string) {
	b.state = s
}

// History 返回动作历史副本。
func (b *BaseEnvironment) History() []Action {
	cp := make([]Action, len(b.history))
	copy(cp, b.history)
	return cp
}
