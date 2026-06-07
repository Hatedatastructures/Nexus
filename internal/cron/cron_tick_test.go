package cron

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"nexus-agent/internal/agent"
	"nexus-agent/internal/gateway/platforms"
)


func TestScheduler_Tick_GetDueJobsError(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	exec := NewExecutor(dir, nil)

	// 使用一个损坏的 jobs.json 文件导致 GetDueJobs 失败
	badJSON := filepath.Join(dir, "jobs.json")
	os.WriteFile(badJSON, []byte("{bad json"), 0600)

	s := NewScheduler(mgr, exec)
	err := s.tick(context.Background())
	if err == nil {
		t.Fatal("GetDueJobs 失败应返回错误")
	}
}

func TestScheduler_TickWithAdapters_GetDueJobsError(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	exec := NewExecutor(dir, nil)

	// 损坏 jobs.json
	os.WriteFile(filepath.Join(dir, "jobs.json"), []byte("bad"), 0600)

	s := NewScheduler(mgr, exec)
	adapters := map[platforms.Platform]platforms.PlatformAdapter{}
	err := s.tickWithAdapters(context.Background(), adapters)
	if err == nil {
		t.Fatal("GetDueJobs 失败应返回错误")
	}
}

func TestScheduler_Tick_ExecuteFails(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	exec := NewExecutor(dir, nil).withRunner(&mockRunner{
		err: fmt.Errorf("execution failed"),
	})

	ctx := context.Background()

	job := &Job{
		ID:       "exec-fail",
		Name:     "exec fail test",
		Prompt:   "test",
		Schedule: "every 10m",
		Enabled:  true,
		State:    "scheduled",
	}
	mgr.Create(ctx, job)

	jobs, _ := mgr.loadAll()
	jobs[0].NextRunAt = time.Now().Add(-30 * time.Second)
	mgr.saveAll(jobs)

	s := NewScheduler(mgr, exec)
	err := s.tick(ctx)
	if err != nil {
		t.Fatalf("tick 不应返回执行错误 (应记录日志): %v", err)
	}

	// 验证作业状态被标记为 error
	updated, _ := mgr.Get(ctx, job.ID)
	if updated.LastStatus != "error" {
		t.Fatalf("LastStatus = %s, 期望 error", updated.LastStatus)
	}
}

func TestScheduler_Tick_AdvanceNextRunFail(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	exec := NewExecutor(dir, nil).withRunner(&mockRunner{
		result: &agent.TurnResult{FinalResponse: "ok"},
	})

	ctx := context.Background()

	job := &Job{
		ID:       "adv-fail",
		Name:     "advance fail",
		Prompt:   "test",
		Schedule: "every 10m",
		Enabled:  true,
		State:    "scheduled",
	}
	mgr.Create(ctx, job)

	// 设置 next_run_at 为过去 (30 秒前，在宽限期内，确保会被执行)
	jobs, _ := mgr.loadAll()
	jobs[0].NextRunAt = time.Now().Add(-30 * time.Second)
	mgr.saveAll(jobs)

	s := NewScheduler(mgr, exec)
	err := s.tick(ctx)
	if err != nil {
		t.Fatalf("tick 应成功: %v", err)
	}
}

	// ───────────────────────────── loadAll 损坏文件 ─────────────────────────────

func TestJobManager_LoadAll_BadJSON(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)

	os.WriteFile(filepath.Join(dir, "jobs.json"), []byte("not json"), 0600)

	_, err := mgr.loadAll()
	if err == nil {
		t.Fatal("损坏 JSON 应返回错误")
	}
}

func TestJobManager_LoadAll_ReadError(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)

	// 创建一个目录作为 jobs.json，导致 ReadFile 失败
	os.MkdirAll(filepath.Join(dir, "jobs.json"), 0700)

	_, err := mgr.loadAll()
	if err == nil {
		t.Fatal("读取目录应返回错误")
	}
}

// ───────────────────────────── DeliverResult 覆盖 ─────────────────────────────

func TestDeliverResult_NilAdapters(t *testing.T) {
	ctx := context.Background()
	job := &Job{
		ID:     "deliver-nil",
		Name:   "test",
		Prompt: "test",
	}

	// nil adapters 不应 panic
	err := DeliverResult(ctx, job, "ok", nil)
	if err != nil {
		t.Fatalf("DeliverResult with nil adapters 应返回 nil: %v", err)
	}
}

// ───────────────────────────── tickWithAdapters 成功路径 ─────────────────────────────

func TestScheduler_TickWithAdapters_Success(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	exec := NewExecutor(dir, nil).withRunner(&mockRunner{
		result: &agent.TurnResult{FinalResponse: "adapter result"},
	})

	ctx := context.Background()
	job := &Job{
		ID:       "adapter-ok",
		Name:     "adapter test",
		Prompt:   "test",
		Schedule: "every 30m",
		Enabled:  true,
		State:    "scheduled",
	}
	mgr.Create(ctx, job)

	jobs, _ := mgr.loadAll()
	jobs[0].NextRunAt = time.Now().Add(-10 * time.Second)
	mgr.saveAll(jobs)

	s := NewScheduler(mgr, exec)
	adapters := map[platforms.Platform]platforms.PlatformAdapter{}
	err := s.tickWithAdapters(ctx, adapters)
	if err != nil {
		t.Fatalf("tickWithAdapters 应成功: %v", err)
	}

	updated, _ := mgr.Get(ctx, job.ID)
	if updated.LastStatus != "ok" {
		t.Fatalf("LastStatus = %s, 期望 ok", updated.LastStatus)
	}
}

// ───────────────────────────── tickWithAdapters 执行失败路径 ─────────────────────────────

func TestScheduler_TickWithAdapters_ExecuteFails(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	exec := NewExecutor(dir, nil).withRunner(&mockRunner{
		err: fmt.Errorf("adapter exec failed"),
	})

	ctx := context.Background()
	job := &Job{
		ID:       "adapter-fail",
		Name:     "adapter fail test",
		Prompt:   "test",
		Schedule: "every 30m",
		Enabled:  true,
		State:    "scheduled",
	}
	mgr.Create(ctx, job)

	jobs, _ := mgr.loadAll()
	jobs[0].NextRunAt = time.Now().Add(-10 * time.Second)
	mgr.saveAll(jobs)

	s := NewScheduler(mgr, exec)
	adapters := map[platforms.Platform]platforms.PlatformAdapter{}
	err := s.tickWithAdapters(ctx, adapters)
	if err != nil {
		t.Fatalf("tickWithAdapters 不应返回错误: %v", err)
	}

	updated, _ := mgr.Get(ctx, job.ID)
	if updated.LastStatus != "error" {
		t.Fatalf("LastStatus = %s, 期望 error", updated.LastStatus)
	}
}

// ───────────────────────────── RunWithAdapters 循环 ─────────────────────────────

func TestScheduler_RunWithAdapters_StopsOnCancel(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	exec := NewExecutor(dir, nil)

	s := NewScheduler(mgr, exec)
	adapters := map[platforms.Platform]platforms.PlatformAdapter{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- s.RunWithAdapters(ctx, adapters)
	}()

	// 等待调度器启动后取消
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("RunWithAdapters 应返回 context 取消错误")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunWithAdapters 应在取消后停止")
	}
}

// ───────────────────────────── acquireLock 错误路径 ─────────────────────────────

func TestScheduler_AcquireLock_BadPath(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	exec := NewExecutor(dir, nil)

	s := NewScheduler(mgr, exec)
	// 使用无效路径作为锁文件
	s.lockFile = filepath.Join(string(rune(0)), "invalid", "lock")

	lock, err := s.acquireLock()
	if err == nil {
		t.Fatal("无效路径应导致锁获取失败")
		if lock != nil {
			lock.Close()
		}
	}
}

// ───────────────────────────── saveOutput 写入错误路径 ─────────────────────────────

func TestExecutor_SaveOutput_WriteFail(t *testing.T) {
	dir := t.TempDir()
	// 创建一个只读目录导致 CreateTemp 失败
	exec := NewExecutor(dir, nil)
	job := &Job{ID: "write-fail"}
	ts := time.Now()

	// 在输出目录创建一个文件阻塞 MkdirAll 的子目录创建
	blockFile := filepath.Join(dir, sanitizeJobID(job.ID))
	os.WriteFile(blockFile, []byte("block"), 0600)

	err := exec.saveOutput(job, "test", ts)
	if err == nil {
		t.Fatal("写入失败应返回错误")
	}
}

// ───────────────────────────── PersistJobStatus 作业不存在 ─────────────────────────────

func TestJobManager_PersistJobStatus_NotFound(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	ctx := context.Background()

	job := &Job{ID: "nonexistent", LastStatus: "ok"}
	err := mgr.PersistJobStatus(ctx, job)
	if err == nil {
		t.Fatal("不存在的作业应返回错误")
	}
}

// ───────────────────────────── tick 锁已占用 (跳过) ─────────────────────────────

func TestScheduler_Tick_LockHeld(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 文件锁为空操作，跳过锁竞争测试")
	}

	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	exec := NewExecutor(dir, nil).withRunner(&mockRunner{
		result: &agent.TurnResult{FinalResponse: "ok"},
	})

	ctx := context.Background()
	job := &Job{
		ID:       "lock-held",
		Name:     "lock test",
		Prompt:   "test",
		Schedule: "every 30m",
		Enabled:  true,
		State:    "scheduled",
	}
	mgr.Create(ctx, job)

	jobs, _ := mgr.loadAll()
	jobs[0].NextRunAt = time.Now().Add(-10 * time.Second)
	mgr.saveAll(jobs)

	s := NewScheduler(mgr, exec)

	// 手动获取锁，使 tick 的锁获取失败
	lockFile := filepath.Join(dir, ".tick.lock")
	os.MkdirAll(dir, 0700)
	f, err := os.OpenFile(lockFile, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		t.Fatalf("无法打开锁文件: %v", err)
	}
	lockFile_f := f
	defer func() { unlockFile(lockFile_f); lockFile_f.Close() }()

	err = s.tick(ctx)
	if err != nil {
		t.Fatalf("锁被占用时 tick 应返回 nil: %v", err)
	}

	// 作业不应被执行
	updated, _ := mgr.Get(ctx, job.ID)
	if updated.LastStatus == "ok" {
		t.Fatal("锁被占用时不应执行作业")
	}
}


