// Package cron 提供定时作业调度功能。
// 支持一次性、间隔重复和 cron 表达式三种调度方式。
// 作业持久化到 ~/.nexus/cron/jobs.json，输出保存到 ~/.nexus/cron/output/。
package cron

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	pkgerrors "nexus-agent/internal/errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"nexus-agent/internal/state"
)

// ───────────────────────────── 作业数据模型 ─────────────────────────────

// Job 表示一个 cron 定时作业。
type Job struct {
	ID               string    `json:"id"`                           // 12 字符 hex ID
	Name             string    `json:"name"`                         // 友好名称
	Prompt           string    `json:"prompt"`                       // 执行的提示词
	Schedule         string    `json:"schedule"`                     // 调度配置字符串
	ScheduleKind     string    `json:"schedule_kind"`                // "once" / "interval" / "cron"
	ScheduleMinutes  int       `json:"schedule_minutes,omitempty"`   // 间隔模式的分钟数
	ScheduleCronExpr string    `json:"schedule_cron_expr,omitempty"` // cron 表达式
	Enabled          bool      `json:"enabled"`                      // 是否启用
	State            string    `json:"state"`                        // "scheduled" / "paused" / "completed" / "error"
	NextRunAt        time.Time `json:"next_run_at"`                  // 下次执行时间
	LastRunAt        time.Time `json:"last_run_at"`                  // 上次执行时间
	LastStatus       string    `json:"last_status"`                  // "ok" / "error" / ""
	LastError        string    `json:"last_error"`                   // 上次错误信息
	CreatedAt        time.Time `json:"created_at"`                   // 创建时间
}

// ───────────────────────────── 作业管理器 ─────────────────────────────

// JobManager 管理作业的全生命周期 (CRUD + 到期判定)。
// 作业数据持久化到磁盘 JSON 文件。
type JobManager struct {
	store *state.Store // 状态存储 (可选)
	dir   string       // 作业存储目录 (~/.nexus/cron)
	mu    sync.RWMutex // 并发保护
}

// NewJobManager 创建作业管理器。
func NewJobManager(store *state.Store, dir string) *JobManager {
	return &JobManager{
		store: store,
		dir:   dir,
	}
}

// Create 创建新作业并持久化。
// 会自动生成 12 字符 hex ID 并设置 created_at 时间戳。
func (m *JobManager) Create(ctx context.Context, job *Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 生成唯一 ID
	if job.ID == "" {
		job.ID = generateJobID()
	}

	// 设置创建时间
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now()
	}

	// 设置初始状态
	if job.State == "" {
		job.State = "scheduled"
	}
	job.Enabled = true

	// 解析调度配置
	kind, minutes, cronExpr, err := parseSchedule(job.Schedule)
	if err != nil {
		return pkgerrors.Wrap(pkgerrors.CronJob, "解析调度配置失败", err)
	}
	job.ScheduleKind = kind
	job.ScheduleMinutes = minutes
	job.ScheduleCronExpr = cronExpr

	// 计算首次执行时间
	now := time.Now()
	switch kind {
	case "once":
		// 对于一次性作业，next_run_at 由调度配置直接指定
		if job.NextRunAt.IsZero() {
			job.NextRunAt = now.Add(time.Duration(minutes) * time.Minute)
		}
	case "interval":
		job.NextRunAt = now.Add(time.Duration(minutes) * time.Minute)
	case "cron":
		job.NextRunAt = nextCronTime(cronExpr, now)
	}

	// 读取现有作业列表
	jobs, err := m.loadAll()
	if err != nil {
		return pkgerrors.Wrap(pkgerrors.CronJob, "读取作业列表失败", err)
	}

	jobs = append(jobs, job)
	if err := m.saveAll(jobs); err != nil {
		return pkgerrors.Wrap(pkgerrors.CronJob, "保存作业列表失败", err)
	}

	slog.Info("Cron: job created",
		"id", job.ID,
		"name", job.Name,
		"kind", kind,
	)

	return nil
}

// Get 根据 ID 获取作业。
func (m *JobManager) Get(ctx context.Context, id string) (*Job, error) {
	jobs, err := m.loadAll()
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.CronJob, "读取作业列表失败", err)
	}

	for _, job := range jobs {
		if job.ID == id {
			return job, nil
		}
	}

	return nil, pkgerrors.New(pkgerrors.CronJob, fmt.Sprintf("作业 '%s' 不存在", id))
}

// List 获取所有已启用的作业列表。
func (m *JobManager) List(ctx context.Context) ([]*Job, error) {
	jobs, err := m.loadAll()
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.CronJob, "读取作业列表失败", err)
	}

	var enabled []*Job
	for _, job := range jobs {
		if job.Enabled {
			enabled = append(enabled, job)
		}
	}
	return enabled, nil
}

// Update 更新作业信息并重新计算下次执行时间。
func (m *JobManager) Update(ctx context.Context, job *Job) error {
	if job.ID == "" {
		return pkgerrors.New(pkgerrors.CronJob, "作业 ID 不能为空")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	jobs, err := m.loadAll()
	if err != nil {
		return pkgerrors.Wrap(pkgerrors.CronJob, "读取作业列表失败", err)
	}

	found := false
	for i, existing := range jobs {
		if existing.ID == job.ID {
			// 重新解析调度配置
			kind, minutes, cronExpr, err := parseSchedule(job.Schedule)
			if err != nil {
				return pkgerrors.Wrap(pkgerrors.CronJob, "解析调度配置失败", err)
			}
			job.ScheduleKind = kind
			job.ScheduleMinutes = minutes
			job.ScheduleCronExpr = cronExpr

			// 重新计算下次执行时间
			now := time.Now()
			switch kind {
			case "interval":
				job.NextRunAt = now.Add(time.Duration(minutes) * time.Minute)
			case "cron":
				job.NextRunAt = nextCronTime(cronExpr, now)
			}

			jobs[i] = job
			found = true
			break
		}
	}

	if !found {
		return pkgerrors.New(pkgerrors.CronJob, fmt.Sprintf("作业 '%s' 不存在", job.ID))
	}

	if err := m.saveAll(jobs); err != nil {
		return pkgerrors.Wrap(pkgerrors.CronJob, "保存作业列表失败", err)
	}

	return nil
}

// Delete 根据 ID 删除作业。
func (m *JobManager) Delete(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	jobs, err := m.loadAll()
	if err != nil {
		return pkgerrors.Wrap(pkgerrors.CronJob, "读取作业列表失败", err)
	}

	found := false
	var filtered []*Job
	for _, job := range jobs {
		if job.ID == id {
			found = true
		} else {
			filtered = append(filtered, job)
		}
	}

	if !found {
		return pkgerrors.New(pkgerrors.CronJob, fmt.Sprintf("作业 '%s' 不存在", id))
	}

	if err := m.saveAll(filtered); err != nil {
		return pkgerrors.Wrap(pkgerrors.CronJob, "保存作业列表失败", err)
	}

	slog.Info("Cron: job deleted", "id", id)
	return nil
}

// ───────────────────────────── 持久化 ─────────────────────────────

// loadAll 从磁盘加载所有作业。
func (m *JobManager) loadAll() ([]*Job, error) {
	if err := os.MkdirAll(m.dir, 0700); err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.CronJob, "创建目录失败", err)
	}

	path := filepath.Join(m.dir, "jobs.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var wrapper struct {
		Jobs []*Job `json:"jobs"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.CronJob, "解析 jobs.json 失败", err)
	}

	return wrapper.Jobs, nil
}

// saveAll 原子写入所有作业到磁盘。
func (m *JobManager) saveAll(jobs []*Job) error {
	if err := os.MkdirAll(m.dir, 0700); err != nil {
		return err
	}

	path := filepath.Join(m.dir, "jobs.json")

	wrapper := struct {
		Jobs      []*Job `json:"jobs"`
		UpdatedAt string `json:"updated_at"`
	}{
		Jobs:      jobs,
		UpdatedAt: time.Now().Format(time.RFC3339),
	}

	data, err := json.MarshalIndent(wrapper, "", "  ")
	if err != nil {
		return err
	}

	// 原子写入 (临时文件 + 重命名)
	tmpFile, err := os.CreateTemp(m.dir, ".jobs_*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	_ = tmpFile.Close()

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	_ = os.Chmod(path, 0600)
	return nil
}

// PersistJobStatus 持久化作业执行状态到磁盘。
// 只更新 LastRunAt, LastStatus, LastError 字段，不改变调度配置。
func (m *JobManager) PersistJobStatus(ctx context.Context, job *Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	jobs, err := m.loadAll()
	if err != nil {
		return pkgerrors.Wrap(pkgerrors.CronJob, "读取作业列表失败", err)
	}

	for i, existing := range jobs {
		if existing.ID == job.ID {
			jobs[i].LastRunAt = job.LastRunAt
			jobs[i].LastStatus = job.LastStatus
			jobs[i].LastError = job.LastError
			return m.saveAll(jobs)
		}
	}

	return pkgerrors.New(pkgerrors.CronJob, fmt.Sprintf("作业 '%s' 不存在", job.ID))
}

// generateJobID 使用加密安全的随机数生成 12 字符十六进制作业 ID。
// 原实现基于时间戳，完全可预测，存在碰撞和被猜测的风险。
func generateJobID() string {
	b := make([]byte, 6)
	// 使用 crypto/rand 生成不可预测的随机字节
	if _, err := rand.Read(b); err != nil {
		// 极端情况降级: crypto/rand 在正常系统上不会失败
		nano := time.Now().UnixNano()
		for i := range b {
			b[i] = byte(nano >> (i * 8))
		}
	}
	return fmt.Sprintf("%x", b)
}
