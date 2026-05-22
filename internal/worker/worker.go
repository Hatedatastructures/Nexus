package worker

import (
	"context"
	"sync"
	"time"
)

// ───────────────────────────── Worker ─────────────────────────────

// Worker 代表一个可管理生命周期的任务单元。
//
// 并发安全:
//   - 所有字段读写均通过 mu 保护
//   - 状态转换经过合法性校验
//   - 完成/失败/取消时通过 done channel 通知等待者
type Worker struct {
	// ID 唯一标识符。
	ID string
	// TaskName 任务名称，用于日志和展示。
	TaskName string

	// 以下字段通过 mu 保护 ────────────────────────
	mu        sync.Mutex
	state     State
	startedAt time.Time
	updatedAt time.Time
	err       error
	result    any
	cancel    context.CancelFunc
	done      chan struct{} // 关闭时表示 Worker 已进入终态
}

// New 创建一个处于 Pending 状态的 Worker。
// 调用方需在适当时机调用 SetCancel 注册取消函数。
func New(id, taskName string) *Worker {
	now := time.Now()
	return &Worker{
		ID:        id,
		TaskName:  taskName,
		state:     StatePending,
		startedAt: now,
		updatedAt: now,
		done:      make(chan struct{}),
	}
}

// ───────────────────────────── 状态访问 ─────────────────────────────

// State 返回当前状态（并发安全）。
func (w *Worker) State() State {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.state
}

// StartedAt 返回任务开始时间。
func (w *Worker) StartedAt() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.startedAt
}

// UpdatedAt 返回最近一次状态更新时间。
func (w *Worker) UpdatedAt() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.updatedAt
}

// Err 返回任务错误（仅在 Failed 状态下有意义）。
func (w *Worker) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

// Result 返回任务结果（仅在 Completed 状态下有意义）。
func (w *Worker) Result() any {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.result
}

// Done 返回一个 channel，当 Worker 进入终态（completed / failed / cancelled）时关闭。
// 可用于 select 或 Wait 阻塞等待。
func (w *Worker) Done() <-chan struct{} {
	return w.done
}

// ───────────────────────────── 状态转换 ─────────────────────────────

// Transition 将 Worker 转换到目标状态 to。
//
// 如果转换不合法，返回 *ErrInvalidTransition。
// 终态转换会自动关闭 done channel 并调用 cancel 函数（如有）。
func (w *Worker) Transition(to State) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !CanTransition(w.state, to) {
		return &ErrInvalidTransition{From: w.state, To: to}
	}

	w.state = to
	w.updatedAt = time.Now()

	// 进入终态时通知等待者并取消上下文
	if isTerminal(to) {
		close(w.done)
		if w.cancel != nil {
			w.cancel()
		}
	}

	return nil
}

// SetResult 设置任务结果并转换为 Completed 状态。
// 如果转换不合法返回错误。
func (w *Worker) SetResult(result any) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !CanTransition(w.state, StateCompleted) {
		return &ErrInvalidTransition{From: w.state, To: StateCompleted}
	}

	w.result = result
	w.state = StateCompleted
	w.updatedAt = time.Now()
	close(w.done)
	if w.cancel != nil {
		w.cancel()
	}
	return nil
}

// SetError 设置任务错误并转换为 Failed 状态。
// 如果转换不合法返回错误。
func (w *Worker) SetError(err error) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !CanTransition(w.state, StateFailed) {
		return &ErrInvalidTransition{From: w.state, To: StateFailed}
	}

	w.err = err
	w.state = StateFailed
	w.updatedAt = time.Now()
	close(w.done)
	if w.cancel != nil {
		w.cancel()
	}
	return nil
}

// SetCancel 注册取消函数。由 Manager 在启动任务时调用。
func (w *Worker) SetCancel(cancel context.CancelFunc) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.cancel = cancel
}

// Cancel 请求取消任务。如果 Worker 处于 Running 或 Paused 状态，
// 会调用注册的 cancel 函数并转换为 Cancelled 状态。
func (w *Worker) Cancel() {
	w.mu.Lock()

	// 使用 CanTransition 检查状态转换合法性，确保 Worker 已处于终态时
	// 不会再次 close(done) 导致 panic（原实现直接检查状态，绕过了状态机校验）
	if !CanTransition(w.state, StateCancelled) {
		w.mu.Unlock()
		return
	}

	w.state = StateCancelled
	w.updatedAt = time.Now()
	cancel := w.cancel

	// 先关闭 done channel，再调用 cancel
	close(w.done)
	w.mu.Unlock()

	if cancel != nil {
		cancel()
	}
}

// Wait 阻塞直到 Worker 进入终态。
func (w *Worker) Wait() {
	<-w.done
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

// isTerminal 判断状态是否为终态。
func isTerminal(s State) bool {
	return s == StateCompleted || s == StateFailed || s == StateCancelled
}
