package worker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/google/uuid"
)

// ───────────────────────────── Manager ─────────────────────────────

// Manager 管理多个 Worker 实例的生命周期。
//
// 并发安全:
//   - 使用 sync.RWMutex 保护 workers map
//   - 读操作使用 RLock，写操作使用 Lock
type Manager struct {
	mu      sync.RWMutex
	workers map[string]*Worker
}

// NewManager 创建一个新的 Manager。
func NewManager() *Manager {
	return &Manager{
		workers: make(map[string]*Worker),
	}
}

// ───────────────────────────── 提交任务 ─────────────────────────────

// Submit 提交一个新任务，返回对应的 Worker。
//
// 任务函数 fn 在独立 goroutine 中执行：
//   - fn 正常返回时，Worker 转为 Completed
//   - fn 返回错误时，Worker 转为 Failed
//   - ctx 被取消时，Worker 转为 Cancelled
func (m *Manager) Submit(ctx context.Context, taskName string, fn func(ctx context.Context) (any, error)) *Worker {
	id := uuid.New().String()
	w := New(id, taskName)

	// 创建可取消的子上下文
	taskCtx, cancel := context.WithCancel(ctx)
	w.SetCancel(cancel)

	// 注册到管理器
	m.mu.Lock()
	m.workers[id] = w
	m.mu.Unlock()

	// 启动执行 goroutine
	go m.run(taskCtx, w, fn)

	slog.Info("worker: task submitted", "id", id, "task", taskName)
	return w
}

// run 在 goroutine 中执行任务函数，处理结果和错误。
func (m *Manager) run(ctx context.Context, w *Worker, fn func(ctx context.Context) (any, error)) {
	// 转换为 Running 状态
	if err := w.Transition(StateRunning); err != nil {
		slog.Error("worker: unable to start task", "id", w.ID, "error", err)
		return
	}

	slog.Info("worker: task execution started", "id", w.ID, "task", w.TaskName)

	// 执行任务
	result, err := fn(ctx)

	// 检查是否已被取消（cancel 回调可能已将状态设为 Cancelled）
	if w.State() == StateCancelled {
		slog.Info("worker: task cancelled", "id", w.ID, "task", w.TaskName)
		return
	}

	// 根据结果设置终态
	if err != nil {
		if setErr := w.SetError(err); setErr != nil {
			slog.Error("worker: unable to set failed status", "id", w.ID, "error", setErr)
		} else {
			slog.Warn("worker: task execution failed", "id", w.ID, "task", w.TaskName, "error", err)
		}
	} else {
		if setErr := w.SetResult(result); setErr != nil {
			slog.Error("worker: unable to set completed status", "id", w.ID, "error", setErr)
		} else {
			slog.Info("worker: task execution completed", "id", w.ID, "task", w.TaskName)
		}
	}
}

// ───────────────────────────── 查询与控制 ─────────────────────────────

// Cancel 取消指定 ID 的任务。
// 如果任务不存在或已处于终态，返回错误。
func (m *Manager) Cancel(id string) error {
	m.mu.RLock()
	w, ok := m.workers[id]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("worker: 任务 %s 不存在", id)
	}

	state := w.State()
	if isTerminal(state) {
		return fmt.Errorf("worker: 任务 %s 已处于终态 %s", id, state)
	}

	w.Cancel()
	slog.Info("worker: task cancelled", "id", id, "task", w.TaskName)
	return nil
}

// GetStatus 返回指定 ID 的 Worker（只读快照）。
// 如果任务不存在返回 nil。
func (m *Manager) GetStatus(id string) *Worker {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.workers[id]
}

// ListActive 返回所有非终态的 Worker 列表。
func (m *Manager) ListActive() []*Worker {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var active []*Worker
	for _, w := range m.workers {
		if !isTerminal(w.State()) {
			active = append(active, w)
		}
	}
	return active
}

// ListAll 返回所有 Worker 列表。
func (m *Manager) ListAll() []*Worker {
	m.mu.RLock()
	defer m.mu.RUnlock()

	all := make([]*Worker, 0, len(m.workers))
	for _, w := range m.workers {
		all = append(all, w)
	}
	return all
}

// Count 返回当前管理的 Worker 总数。
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.workers)
}

// Remove 从管理器中移除已完成的 Worker。
// 只有终态的 Worker 才能被移除。
func (m *Manager) Remove(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	w, ok := m.workers[id]
	if !ok {
		return fmt.Errorf("worker: 任务 %s 不存在", id)
	}

	if !isTerminal(w.State()) {
		return fmt.Errorf("worker: 任务 %s 尚未完成 (状态: %s)，无法移除", id, w.State())
	}

	delete(m.workers, id)
	return nil
}
