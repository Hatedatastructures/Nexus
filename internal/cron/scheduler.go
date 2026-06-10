// Package cron 提供 cron 调度器，负责周期性检查并执行到期作业。
// 使用文件锁防止跨进程并发执行。
package cron

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"nexus-agent/internal/gateway/platforms"
)

// ───────────────────────────── 调度器 ─────────────────────────────

// Scheduler 周期性检查到期作业并执行。
//
// 并发安全:
//   - 使用文件锁 (~/.nexus/cron/.tick.lock) 防跨进程并发
//   - 同一时刻只有一个 Scheduler 实例执行 tick
type Scheduler struct {
	manager  *JobManager   // 作业管理器
	executor *Executor     // 作业执行器
	interval time.Duration // 检查间隔 (默认 60 秒)
	lockFile string        // 文件锁路径
	mu       sync.Mutex
	running  bool
	cancel   context.CancelFunc
}

// NewScheduler 创建调度器。
func NewScheduler(manager *JobManager, executor *Executor) *Scheduler {
	return &Scheduler{
		manager:  manager,
		executor: executor,
		interval: 60 * time.Second,
		lockFile: filepath.Join(manager.dir, ".tick.lock"),
	}
}

// tickFn 定义单次调度检查的函数签名
type tickFn func(ctx context.Context) error

// Run 启动调度循环。此方法会阻塞直到 ctx 被取消。
func (s *Scheduler) Run(ctx context.Context) error {
	return s.run(ctx, s.tick, "基础")
}

// RunWithAdapters 以平台适配器启动调度器。
// adapters 用于作业结果投递到消息平台。
func (s *Scheduler) RunWithAdapters(ctx context.Context, adapters map[platforms.Platform]platforms.PlatformAdapter) error {
	return s.run(ctx, func(ctx context.Context) error {
		return s.tickWithAdapters(ctx, adapters)
	}, "含适配器")
}

// run 是调度循环的核心实现，由 Run 和 RunWithAdapters 共享。
// 含适配器的逻辑差异通过 fn 参数抽象。
func (s *Scheduler) run(ctx context.Context, fn tickFn, logMsg string) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil // 已经在运行
	}
	s.running = true
	ctx, s.cancel = context.WithCancel(ctx)
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	// 确保锁目录存在
	if err := os.MkdirAll(filepath.Dir(s.lockFile), 0700); err != nil {
		return err
	}

	slog.Info("Cron: scheduler started", "mode", logMsg, "interval", s.interval)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("Cron: scheduler stopped", "mode", logMsg)
			return ctx.Err()
		case <-ticker.C:
			if err := fn(ctx); err != nil {
				slog.Error("Cron: tick failed", "mode", logMsg, "error", err)
			}
		}
	}
}

// Stop 停止调度器。
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}

// ───────────────────────────── 调度 tick ─────────────────────────────

// tick 执行单次调度检查。
//
// 流程:
//  1. 获取文件锁 (非阻塞 — 如果已被锁定则跳过)
//  2. 获取到期作业
//  3. 为所有重复作业预计算下次执行时间
//  4. 顺序执行作业
//  5. 持久化执行状态
//  6. 投递结果
//  7. 释放锁
func (s *Scheduler) tick(ctx context.Context) error {
	// 获取文件锁
	lock, err := s.acquireLock()
	if err != nil {
		slog.Debug("Cron: tick skipped — lock already held")
		return nil
	}
	defer s.releaseLock(lock)

	// 获取到期作业
	dueJobs, err := s.manager.GetDueJobs(ctx)
	if err != nil {
		return err
	}

	if len(dueJobs) == 0 {
		return nil
	}

	slog.Info("Cron: due jobs", "count", len(dueJobs))

	// 预计算所有重复作业的下次执行时间
	for _, job := range dueJobs {
		if err := s.manager.AdvanceNextRun(ctx, job); err != nil {
			slog.Warn("Cron: failed to advance next run time",
				"job_id", job.ID, "error", err,
			)
		}
	}

	// 顺序执行作业 (由调度器保证单线程)
	for _, job := range dueJobs {
		if err := s.executor.Execute(ctx, job); err != nil {
			slog.Error("Cron: job execution failed",
				"job_id", job.ID,
				"name", job.Name,
				"error", err,
			)
			job.LastStatus = "error"
			job.LastError = err.Error()
		}

		// 持久化执行状态到磁盘
		if err := s.manager.PersistJobStatus(ctx, job); err != nil {
			slog.Warn("Cron: failed to persist job status",
				"job_id", job.ID, "error", err,
			)
		}

		// 投递结果
		if err := DeliverResult(ctx, job, job.LastStatus, nil); err != nil {
			slog.Warn("Cron: result delivery failed",
				"job_id", job.ID, "error", err,
			)
		}
	}

	return nil
}

// tickWithAdapters 执行单次调度检查 (带平台适配器用于投递)。
func (s *Scheduler) tickWithAdapters(ctx context.Context, adapters map[platforms.Platform]platforms.PlatformAdapter) error {
	lock, err := s.acquireLock()
	if err != nil {
		slog.Warn("scheduler: failed to acquire lock, skipping tick", "err", err)
		return nil
	}
	defer s.releaseLock(lock)

	dueJobs, err := s.manager.GetDueJobs(ctx)
	if err != nil {
		return err
	}

	if len(dueJobs) == 0 {
		return nil
	}

	slog.Info("Cron: due jobs", "count", len(dueJobs))

	for _, job := range dueJobs {
		if err := s.manager.AdvanceNextRun(ctx, job); err != nil {
			slog.Warn("Cron: failed to advance next run time",
				"job_id", job.ID, "error", err,
			)
		}

		if err := s.executor.Execute(ctx, job); err != nil {
			slog.Error("Cron: job execution failed",
				"job_id", job.ID,
				"error", err,
			)
			job.LastStatus = "error"
			job.LastError = err.Error()
		}

		// 持久化执行状态到磁盘
		if err := s.manager.PersistJobStatus(ctx, job); err != nil {
			slog.Warn("Cron: failed to persist job status",
				"job_id", job.ID, "error", err,
			)
		}

		if err := DeliverResult(ctx, job, job.LastStatus, adapters); err != nil {
			slog.Warn("Cron: result delivery failed",
				"job_id", job.ID, "error", err,
			)
		}
	}

	return nil
}

// ───────────────────────────── 文件锁 ─────────────────────────────

// acquireLock 获取 tick 文件锁 (非阻塞)。
// 返回文件句柄或错误 (如果锁已被占用)。
func (s *Scheduler) acquireLock() (*os.File, error) {
	// 确保锁目录存在
	dir := filepath.Dir(s.lockFile)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}

	// 打开或创建锁文件
	f, err := os.OpenFile(s.lockFile, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}

	// 获取排他锁 (非阻塞)
	if err := lockFile(f); err != nil {
		_ = f.Close()
		return nil, err
	}

	return f, nil
}

// releaseLock 释放 tick 锁。
func (s *Scheduler) releaseLock(f *os.File) {
	if f != nil {
		_ = unlockFile(f)
		_ = f.Close()
	}
}
