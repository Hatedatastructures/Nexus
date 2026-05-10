// Package worker 提供 Worker 状态机，用于管理长时间运行操作的生命周期。
//
// 支持的状态: pending → running → {paused, completed, failed, cancelled}
// 所有状态转换均经过合法性校验，非法转换返回错误。
package worker

import "fmt"

// ───────────────────────────── 状态定义 ─────────────────────────────

// State 表示 Worker 的当前状态。
type State string

const (
	// StatePending 等待执行的初始状态。
	StatePending State = "pending"
	// StateRunning 正在执行中。
	StateRunning State = "running"
	// StatePaused 已暂停，可恢复。
	StatePaused State = "paused"
	// StateCompleted 成功完成（终态）。
	StateCompleted State = "completed"
	// StateFailed 执行失败（终态）。
	StateFailed State = "failed"
	// StateCancelled 已取消（终态）。
	StateCancelled State = "cancelled"
)

// ───────────────────────────── 转换规则 ─────────────────────────────

// validTransitions 定义每个状态的合法目标状态集合。
// 终态（completed / failed / cancelled）不可转换到任何其他状态。
var validTransitions = map[State][]State{
	StatePending:   {StateRunning, StateCancelled},
	StateRunning:   {StatePaused, StateCompleted, StateFailed, StateCancelled},
	StatePaused:    {StateRunning, StateCancelled},
	StateCompleted: {},
	StateFailed:    {},
	StateCancelled: {},
}

// ErrInvalidTransition 表示非法状态转换。
type ErrInvalidTransition struct {
	From State
	To   State
}

func (e *ErrInvalidTransition) Error() string {
	return fmt.Sprintf("worker: 非法状态转换 %s → %s", e.From, e.To)
}

// CanTransition 检查从 from 到 to 的转换是否合法。
func CanTransition(from, to State) bool {
	targets, ok := validTransitions[from]
	if !ok {
		return false
	}
	for _, s := range targets {
		if s == to {
			return true
		}
	}
	return false
}
