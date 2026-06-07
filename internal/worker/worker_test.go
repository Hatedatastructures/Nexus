package worker

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// ───────────────────────────── TestWorkerTransition ─────────────────────────────

func TestWorkerTransition(t *testing.T) {
	tests := []struct {
		name    string
		from    State
		to      State
		wantErr bool
	}{
		// 合法转换
		{name: "Pending → Running", from: StatePending, to: StateRunning, wantErr: false},
		{name: "Pending → Cancelled", from: StatePending, to: StateCancelled, wantErr: false},
		{name: "Running → Paused", from: StateRunning, to: StatePaused, wantErr: false},
		{name: "Running → Completed", from: StateRunning, to: StateCompleted, wantErr: false},
		{name: "Running → Failed", from: StateRunning, to: StateFailed, wantErr: false},
		{name: "Running → Cancelled", from: StateRunning, to: StateCancelled, wantErr: false},
		{name: "Paused → Running", from: StatePaused, to: StateRunning, wantErr: false},
		{name: "Paused → Cancelled", from: StatePaused, to: StateCancelled, wantErr: false},

		// 非法转换
		{name: "Completed → Running (终态不可转换)", from: StateCompleted, to: StateRunning, wantErr: true},
		{name: "Completed → Pending", from: StateCompleted, to: StatePending, wantErr: true},
		{name: "Failed → Running", from: StateFailed, to: StateRunning, wantErr: true},
		{name: "Failed → Completed", from: StateFailed, to: StateCompleted, wantErr: true},
		{name: "Cancelled → Running", from: StateCancelled, to: StateRunning, wantErr: true},
		{name: "Cancelled → Paused", from: StateCancelled, to: StatePaused, wantErr: true},
		{name: "Pending → Completed (跳过 Running)", from: StatePending, to: StateCompleted, wantErr: true},
		{name: "Pending → Paused", from: StatePending, to: StatePaused, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := New("test-id", "test-task")

			// 通过合法路径到达 from 状态
			// Pending → Running 是必经之路，到达 Running 后可继续转换
			switch tt.from {
			case StateRunning:
				if err := w.Transition(StateRunning); err != nil {
					t.Fatalf("无法从 Pending 转换到 Running: %v", err)
				}
			case StatePaused:
				// Pending → Running → Paused
				_ = w.Transition(StateRunning)
				if err := w.Transition(StatePaused); err != nil {
					t.Fatalf("无法从 Running 转换到 Paused: %v", err)
				}
			case StateCompleted:
				// Pending → Running → Completed
				_ = w.Transition(StateRunning)
				if err := w.Transition(StateCompleted); err != nil {
					t.Fatalf("无法从 Running 转换到 Completed: %v", err)
				}
			case StateFailed:
				// Pending → Running → Failed
				_ = w.Transition(StateRunning)
				if err := w.Transition(StateFailed); err != nil {
					t.Fatalf("无法从 Running 转换到 Failed: %v", err)
				}
			case StateCancelled:
				// Pending → Running → Cancelled
				_ = w.Transition(StateRunning)
				if err := w.Transition(StateCancelled); err != nil {
					t.Fatalf("无法从 Running 转换到 Cancelled: %v", err)
				}
			}
			// StatePending: 已是初始状态，无需额外转换

			// 执行目标转换
			err := w.Transition(tt.to)
			if (err != nil) != tt.wantErr {
				t.Errorf("Transition(%s → %s) error = %v, wantErr %v", tt.from, tt.to, err, tt.wantErr)
			}

			// 验证非法转换不改变状态
			if tt.wantErr && w.State() != tt.from {
				t.Errorf("非法转换后状态应保持 %s, 实际为 %s", tt.from, w.State())
			}
		})
	}
}

// ───────────────────────────── TestWorkerTransitionDetails ─────────────────────────────

func TestWorkerTransitionDetails(t *testing.T) {
	t.Run("终态转换关闭 done channel", func(t *testing.T) {
		w := New("id", "task")
		_ = w.Transition(StateRunning)

		go func() {
			time.Sleep(10 * time.Millisecond)
			_ = w.Transition(StateCompleted)
		}()

		select {
		case <-w.Done():
			// 成功: done channel 已关闭
		case <-time.After(1 * time.Second):
			t.Error("Wait() 超时，done channel 未关闭")
		}
	})

	t.Run("SetResult 设置结果并转为 Completed", func(t *testing.T) {
		w := New("id", "task")
		_ = w.Transition(StateRunning)

		if err := w.SetResult("hello"); err != nil {
			t.Fatalf("SetResult() error: %v", err)
		}

		if w.State() != StateCompleted {
			t.Errorf("状态 = %v, want Completed", w.State())
		}
		if w.Result() != "hello" {
			t.Errorf("结果 = %v, want 'hello'", w.Result())
		}
	})

	t.Run("SetError 设置错误并转为 Failed", func(t *testing.T) {
		w := New("id", "task")
		_ = w.Transition(StateRunning)

		if err := w.SetError(fmt.Errorf("出错了")); err != nil {
			t.Fatalf("SetError() error: %v", err)
		}

		if w.State() != StateFailed {
			t.Errorf("状态 = %v, want Failed", w.State())
		}
		if w.Err() == nil || w.Err().Error() != "出错了" {
			t.Errorf("错误 = %v, want '出错了'", w.Err())
		}
	})
}

// ───────────────────────────── TestManagerSubmit ─────────────────────────────

func TestManagerSubmit(t *testing.T) {
	tests := []struct {
		name      string
		taskFn    func(ctx context.Context) (any, error)
		wantState State
		wantErr   bool
	}{
		{
			name: "成功完成的任务",
			taskFn: func(_ context.Context) (any, error) {
				return "result-data", nil
			},
			wantState: StateCompleted,
		},
		{
			name: "返回错误的任务",
			taskFn: func(_ context.Context) (any, error) {
				return nil, fmt.Errorf("任务失败")
			},
			wantState: StateFailed,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr := NewManager()
			w := mgr.Submit(context.Background(), tt.name, tt.taskFn)

			// 等待完成
			select {
			case <-w.Done():
			case <-time.After(3 * time.Second):
				t.Fatal("任务超时")
			}

			if w.State() != tt.wantState {
				t.Errorf("状态 = %v, want %v", w.State(), tt.wantState)
			}

			if tt.wantErr && w.Err() == nil {
				t.Error("预期有错误，但 Err() 为 nil")
			}
			if !tt.wantErr && w.Err() != nil {
				t.Errorf("预期无错误，但 Err() = %v", w.Err())
			}
		})
	}
}

// ───────────────────────────── TestManagerCancel ─────────────────────────────

func TestManagerCancel(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{
			name:    "取消正在运行的任务",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr := NewManager()

			// 提交一个阻塞任务
			w := mgr.Submit(context.Background(), "blocking-task", func(ctx context.Context) (any, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			})

			// 等待任务进入 Running 状态
			time.Sleep(50 * time.Millisecond)

			// 取消任务
			err := mgr.Cancel(w.ID)
			if (err != nil) != tt.wantErr {
				t.Errorf("Cancel() error = %v, wantErr %v", err, tt.wantErr)
			}

			// 等待任务完成
			select {
			case <-w.Done():
			case <-time.After(2 * time.Second):
				t.Fatal("取消后任务未完成")
			}

			if w.State() != StateCancelled {
				t.Errorf("状态 = %v, want Cancelled", w.State())
			}
		})
	}

	t.Run("取消不存在的任务", func(t *testing.T) {
		mgr := NewManager()
		err := mgr.Cancel("nonexistent-id")
		if err == nil {
			t.Error("取消不存在的任务应返回错误")
		}
	})

	t.Run("取消已完成的任务", func(t *testing.T) {
		mgr := NewManager()
		w := mgr.Submit(context.Background(), "quick-task", func(_ context.Context) (any, error) {
			return "done", nil
		})

		select {
		case <-w.Done():
		case <-time.After(2 * time.Second):
			t.Fatal("任务超时")
		}

		err := mgr.Cancel(w.ID)
		if err == nil {
			t.Error("取消已完成的任务应返回错误")
		}
	})
}

// ───────────────────────────── TestManagerListActive ─────────────────────────────

func TestManagerListActive(t *testing.T) {
	tests := []struct {
		name       string
		submitN    int
		completeN  int
		wantActive int
	}{
		{name: "全部活跃", submitN: 3, completeN: 0, wantActive: 3},
		{name: "部分完成", submitN: 3, completeN: 1, wantActive: 2},
		{name: "全部完成", submitN: 2, completeN: 2, wantActive: 0},
		{name: "无任务", submitN: 0, completeN: 0, wantActive: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr := NewManager()

			var workers []*Worker
			for i := 0; i < tt.submitN; i++ {
				w := mgr.Submit(context.Background(), fmt.Sprintf("task-%d", i), func(ctx context.Context) (any, error) {
					<-ctx.Done()
					return nil, ctx.Err()
				})
				workers = append(workers, w)
			}

			// 等待所有任务进入 Running
			time.Sleep(50 * time.Millisecond)

			// 完成指定数量的任务 (通过取消)
			for i := 0; i < tt.completeN; i++ {
				workers[i].Cancel()
			}

			// 等待取消生效
			time.Sleep(50 * time.Millisecond)

			active := mgr.ListActive()
			if len(active) != tt.wantActive {
				t.Errorf("ListActive() 返回 %d 个, want %d", len(active), tt.wantActive)
			}
		})
	}
}
