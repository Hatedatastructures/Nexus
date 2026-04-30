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
	manager   *JobManager          // 作业管理器
	executor  *Executor            // 作业执行器
	interval  time.Duration        // 检查间隔 (默认 60 秒)
	lockFile  string               // 文件锁路径
	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
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

// Run 启动调度循环。此方法会阻塞直到 ctx 被取消。
//
// 在每个 tick 中:
//  1. 获取文件锁 (防跨进程并发)
//  2. 获取到期作业列表
//  3. 对所有重复作业预计算下次执行时间 (at-most-once 语义)
//  4. 并发执行到期作业
//  5. 释放锁，等待下一个 interval
func (s *Scheduler) Run(ctx context.Context) error {
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

	slog.Info("Cron: 调度器启动", "interval", s.interval)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("Cron: 调度器停止")
			return ctx.Err()
		case <-ticker.C:
			if err := s.tick(ctx); err != nil {
				slog.Error("Cron: tick 失败", "error", err)
			}
		}
	}
}

// RunWithAdapters 以平台适配器启动调度器 (用于网关集成)。
// adapters 用于作业结果投递到消息平台。
func (s *Scheduler) RunWithAdapters(ctx context.Context, adapters map[platforms.Platform]platforms.PlatformAdapter) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = true

	ctx, s.cancel = context.WithCancel(ctx)
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	slog.Info("Cron: 调度器启动 (含适配器)", "interval", s.interval)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := s.tickWithAdapters(ctx, adapters); err != nil {
				slog.Error("Cron: tick 失败", "error", err)
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
//  4. 并发执行作业
//  5. 释放锁
func (s *Scheduler) tick(ctx context.Context) error {
	// 获取文件锁
	lock, err := s.acquireLock()
	if err != nil {
		slog.Debug("Cron: tick 跳过 — 锁已被占用")
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

	slog.Info("Cron: 到期作业", "count", len(dueJobs))

	// 预计算所有重复作业的下次执行时间
	for _, job := range dueJobs {
		if err := s.manager.AdvanceNextRun(ctx, job); err != nil {
			slog.Warn("Cron: 推进下次执行时间失败",
				"job_id", job.ID, "error", err,
			)
		}
	}

	// 顺序执行作业 (由调度器保证单线程)
	for _, job := range dueJobs {
		if err := s.executor.Execute(ctx, job); err != nil {
			slog.Error("Cron: 作业执行失败",
				"job_id", job.ID,
				"name", job.Name,
				"error", err,
			)
			job.LastStatus = "error"
			job.LastError = err.Error()
		}

		// 投递结果
		if err := DeliverResult(ctx, job, job.LastStatus, nil); err != nil {
			slog.Warn("Cron: 投递结果失败",
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

	slog.Info("Cron: 到期作业", "count", len(dueJobs))

	for _, job := range dueJobs {
		s.manager.AdvanceNextRun(ctx, job)

		if err := s.executor.Execute(ctx, job); err != nil {
			slog.Error("Cron: 作业执行失败",
				"job_id", job.ID,
				"error", err,
			)
			job.LastStatus = "error"
			job.LastError = err.Error()
		}

		DeliverResult(ctx, job, job.LastStatus, adapters)
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
		f.Close()
		return nil, err
	}

	return f, nil
}

// releaseLock 释放 tick 锁。
func (s *Scheduler) releaseLock(f *os.File) {
	if f != nil {
		unlockFile(f)
		f.Close()
	}
}
