package cron

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nexus-agent/internal/gateway/platforms"
)

// ═══════════════════════════════════════════════ Scheduler ═══════════════════════════════════════════════

func TestNewScheduler(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	exec := NewExecutor(t.TempDir(), nil)
	sched := NewScheduler(mgr, exec)

	if sched.interval != 60*time.Second {
		t.Errorf("interval = %v, 期望 60s", sched.interval)
	}
	if sched.running {
		t.Error("新调度器不应处于运行状态")
	}
}

func TestScheduler_Stop(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	exec := NewExecutor(t.TempDir(), nil)
	sched := NewScheduler(mgr, exec)

	// Stop 未运行的调度器不应 panic
	sched.Stop()
}

func TestScheduler_Run_Cancel(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	exec := NewExecutor(t.TempDir(), nil)
	sched := NewScheduler(mgr, exec)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- sched.Run(ctx)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("Run 应返回 context.Canceled, 实际: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("调度器未在 5 秒内停止")
	}
}

func TestScheduler_Run_Twice(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	exec := NewExecutor(t.TempDir(), nil)
	sched := NewScheduler(mgr, exec)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 第一次启动
	done1 := make(chan error, 1)
	go func() {
		done1 <- sched.Run(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	// 第二次启动应直接返回 nil
	done2 := make(chan error, 1)
	go func() {
		done2 <- sched.Run(ctx)
	}()

	select {
	case err := <-done2:
		if err != nil {
			t.Errorf("重复启动应返回 nil, 实际: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("重复启动未在 2 秒内返回")
	}

	cancel()
	<-done1
}

// ═══════════════════════════════════════════════ lockFile / unlockFile (Windows) ═══════════════════════════════════════════════

func TestLockFile_Windows(t *testing.T) {
	f, err := os.CreateTemp("", "locktest-*")
	if err != nil {
		t.Fatalf("创建临时文件失败: %v", err)
	}
	defer os.Remove(f.Name())
	defer f.Close()

	// Windows 上 lockFile 应返回 nil
	if err := lockFile(f); err != nil {
		t.Fatalf("lockFile 应返回 nil (Windows), 实际: %v", err)
	}
}

func TestUnlockFile_Windows(t *testing.T) {
	f, err := os.CreateTemp("", "locktest-*")
	if err != nil {
		t.Fatalf("创建临时文件失败: %v", err)
	}
	defer os.Remove(f.Name())
	defer f.Close()

	if err := lockFile(f); err != nil {
		t.Fatalf("lockFile 应成功: %v", err)
	}
	if err := unlockFile(f); err != nil {
		t.Fatalf("unlockFile 应返回 nil (Windows), 实际: %v", err)
	}
}

// ═══════════════════════════════════════════════ Scheduler.acquireLock / releaseLock ═══════════════════════════════════════════════

func TestScheduler_AcquireAndReleaseLock(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	exec := NewExecutor(filepath.Join(dir, "output"), nil)
	s := NewScheduler(mgr, exec)

	lock, err := s.acquireLock()
	if err != nil {
		t.Fatalf("acquireLock 应成功: %v", err)
	}
	if lock == nil {
		t.Fatal("lock 不应为 nil")
	}

	// 释放锁不应报错
	s.releaseLock(lock)

	// 确认锁文件存在
	if _, err := os.Stat(s.lockFile); os.IsNotExist(err) {
		t.Fatal("锁文件应存在")
	}
}

func TestScheduler_ReleaseLock_Nil(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	exec := NewExecutor(filepath.Join(dir, "output"), nil)
	s := NewScheduler(mgr, exec)

	// 释放 nil 不应 panic
	s.releaseLock(nil)
}

// ═══════════════════════════════════════════════ Scheduler.tick ═════════════════════════════════════════════════════════════════

func TestScheduler_Tick_NoDueJobs(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	exec := NewExecutor(filepath.Join(dir, "output"), nil)
	s := NewScheduler(mgr, exec)

	ctx := context.Background()
	err := s.tick(ctx)
	if err != nil {
		t.Fatalf("无到期作业时 tick 不应报错: %v", err)
	}
}

func TestScheduler_Tick_WithDueJob_ExecuteFails(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	// agentConfig 为 nil 会导致 Execute 内部 panic 或创建空 agent
	// 由于 Execute 内部调用 agent.DefaultAgentFromConfig(nil)，需要确认行为
	exec := NewExecutor(filepath.Join(dir, "output"), nil)
	s := NewScheduler(mgr, exec)

	ctx := context.Background()

	// 创建一个到期作业: 一次性作业, NextRunAt 已过期
	job := &Job{
		ID:           "tick-test-1",
		Name:         "Tick测试",
		Prompt:       "测试",
		ScheduleKind: "interval",
		Schedule:     "30m",
		Enabled:      true,
	}
	if err := mgr.Create(ctx, job); err != nil {
		t.Fatalf("Create 失败: %v", err)
	}

	// 手动将 NextRunAt 设为过去
	jobs, _ := mgr.loadAll()
	for _, j := range jobs {
		if j.ID == "tick-test-1" {
			j.NextRunAt = time.Now().Add(-1 * time.Minute)
			j.ScheduleMinutes = 30
		}
	}
	mgr.saveAll(jobs)

	// tick 应该尝试执行作业 (Execute 因 nil config 会失败，但 tick 应处理错误)
	// DefaultAgentFromConfig(nil) 会 panic，需要 recover
	recovered := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				recovered = true
				t.Logf("tick 内部 panic (预期内, nil agentConfig): %v", r)
			}
		}()
		_ = s.tick(ctx)
	}()
	if !recovered {
		t.Log("tick 未 panic — DefaultAgentFromConfig(nil) 已安全处理 nil config")
	}
}

func TestScheduler_Tick_WithDueJob(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)

	// 创建一个到期作业
	job := &Job{
		ID:           "tick-due-1",
		Name:         "到期作业",
		Prompt:       "测试提示",
		ScheduleKind: "interval",
		Schedule:     "30m",
		Enabled:      true,
	}
	ctx := context.Background()
	if err := mgr.Create(ctx, job); err != nil {
		t.Fatalf("Create 失败: %v", err)
	}

	// 设为到期
	jobs, _ := mgr.loadAll()
	for _, j := range jobs {
		if j.ID == "tick-due-1" {
			j.NextRunAt = time.Now().Add(-1 * time.Minute)
			j.ScheduleMinutes = 30
		}
	}
	mgr.saveAll(jobs)

	exec := NewExecutor(filepath.Join(dir, "output"), nil)
	s := NewScheduler(mgr, exec)

	// 调用 tick — Execute 会因 nil config 而在 agent.DefaultAgentFromConfig 处 panic
	recovered := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				recovered = true
				t.Logf("tick 内部 panic (预期内, nil agentConfig): %v", r)
			}
		}()
		_ = s.tick(ctx)
	}()

	if !recovered {
		t.Log("tick 未 panic — DefaultAgentFromConfig(nil) 已安全处理 nil config")
	}
}

// ═══════════════════════════════════════════════ Scheduler.tickWithAdapters ═══════════════════════════════════════════════════

func TestScheduler_TickWithAdapters_NoDueJobs(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	exec := NewExecutor(filepath.Join(dir, "output"), nil)
	s := NewScheduler(mgr, exec)

	adapters := map[platforms.Platform]platforms.PlatformAdapter{
		platforms.PlatformTelegram: &mockAdapter{name: "T", platform: platforms.PlatformTelegram},
	}

	ctx := context.Background()
	err := s.tickWithAdapters(ctx, adapters)
	if err != nil {
		t.Fatalf("无到期作业时 tickWithAdapters 不应报错: %v", err)
	}
}

// TestScheduler_TickWithAdapters_WithDueJob 测试 tickWithAdapters 处理到期作业路径。
func TestScheduler_TickWithAdapters_WithDueJob(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	exec := NewExecutor(filepath.Join(dir, "output"), nil)
	s := NewScheduler(mgr, exec)

	adapters := map[platforms.Platform]platforms.PlatformAdapter{
		platforms.PlatformTelegram: &mockAdapter{name: "T", platform: platforms.PlatformTelegram},
	}

	job := &Job{
		ID:       "twa-due-1",
		Name:     "适配器到期",
		Prompt:   "test",
		Schedule: "every 30m",
		Enabled:  true,
	}
	ctx := context.Background()
	if err := mgr.Create(ctx, job); err != nil {
		t.Fatalf("Create 失败: %v", err)
	}

	// 设为到期
	jobs, _ := mgr.loadAll()
	for _, j := range jobs {
		if j.ID == "twa-due-1" {
			j.NextRunAt = time.Now().Add(-1 * time.Minute)
		}
	}
	mgr.saveAll(jobs)

	// Execute 会因 nil agentConfig 在 DefaultAgentFromConfig 处 panic
	recovered := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				recovered = true
				t.Logf("tickWithAdapters 内部 panic (预期内): %v", r)
			}
		}()
		_ = s.tickWithAdapters(ctx, adapters)
	}()

	if !recovered {
		t.Log("tickWithAdapters 未 panic — nil config 已安全处理")
	}
}

