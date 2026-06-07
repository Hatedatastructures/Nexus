package cron

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// ═══════════════════════════════════════════════ Scheduler.Stop (非 nil cancel) ═══════════════════════════════════════════════

func TestScheduler_Stop_NonNilCancel(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	exec := NewExecutor(filepath.Join(dir, "output"), nil)
	s := NewScheduler(mgr, exec)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 在后台启动 scheduler
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.Run(ctx)
	}()

	// 等待 scheduler 真正启动
	time.Sleep(200 * time.Millisecond)

	// 调用 Stop (内部调用 cancel())
	s.Stop()

	// 等待 Run 返回
	select {
	case <-done:
		t.Log("Scheduler 已通过 Stop 停止")
	case <-time.After(3 * time.Second):
		t.Fatal("Scheduler 未在 3 秒内停止")
	}
}

// ═══════════════════════════════════════════════ Scheduler.run — already running ═══════════════════════════════════════════════

func TestScheduler_Run_AlreadyRunning(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	exec := NewExecutor(filepath.Join(dir, "output"), nil)
	s := NewScheduler(mgr, exec)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 第一次 Run 启动
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.Run(ctx)
	}()

	time.Sleep(200 * time.Millisecond)

	// 第二次 Run 应立即返回 nil
	err := s.Run(ctx)
	if err != nil {
		t.Fatalf("已在运行时 Run 应返回 nil, 实际: %v", err)
	}

	cancel()
	<-done
}

// ═══════════════════════════════════════════════ Scheduler.acquireLock — 目录创建失败 ═══════════════════════════════════════════════

func TestScheduler_AcquireLock_MkdirAllFail(t *testing.T) {
	// 使用一个不存在的驱动器路径来触发 MkdirAll 失败
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	exec := NewExecutor(filepath.Join(dir, "output"), nil)
	s := NewScheduler(mgr, exec)

	// 将 lockFile 设为无法创建的路径
	s.lockFile = filepath.Join("Z:\\nonexistent_drive_12345", ".tick.lock")

	_, err := s.acquireLock()
	if err == nil {
		t.Fatal("acquireLock 在无法创建目录时应返回错误")
	}
}

// ═══════════════════════════════════════════════ Scheduler.RunWithAdapters — 取消测试 ═══════════════════════════════════════════════

func TestScheduler_RunWithAdapters_Cancel(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	exec := NewExecutor(filepath.Join(dir, "output"), nil)
	s := NewScheduler(mgr, exec)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.RunWithAdapters(ctx, nil)
	}()

	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case <-done:
		t.Log("RunWithAdapters 已通过 context 取消停止")
	case <-time.After(3 * time.Second):
		t.Fatal("RunWithAdapters 未在 3 秒内停止")
	}
}

