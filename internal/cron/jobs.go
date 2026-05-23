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
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"nexus-agent/internal/state"
)

// 预编译正则表达式，避免每次函数调用时重复编译带来的性能开销
var (
	cronFieldRe = regexp.MustCompile(`^[\d\*\-,/]+$`)
	isoDateRe   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`)
	durationRe  = regexp.MustCompile(`^(\d+)\s*(m|min|mins|minute|minutes|h|hr|hrs|hour|hours|d|day|days)$`)
)

// ───────────────────────────── 常量 ─────────────────────────────

const (
	// oneshotGraceSeconds 一次性作业的宽限期 (允许创建后 2 分钟内运行)
	oneshotGraceSeconds = 120

	// minGraceSeconds 错过时间的最大容忍秒数下限
	minGraceSeconds = 120

	// maxGraceSeconds 错过时间的最大容忍秒数上限 (2 小时)
	maxGraceSeconds = 7200
)

// ───────────────────────────── 作业数据模型 ─────────────────────────────

// Job 表示一个 cron 定时作业。
type Job struct {
	ID           string    `json:"id"`            // 12 字符 hex ID
	Name         string    `json:"name"`          // 友好名称
	Prompt       string    `json:"prompt"`        // 执行的提示词
	Schedule     string    `json:"schedule"`      // 调度配置字符串
	ScheduleKind string    `json:"schedule_kind"` // "once" / "interval" / "cron"
	ScheduleMinutes int   `json:"schedule_minutes,omitempty"` // 间隔模式的分钟数
	ScheduleCronExpr string `json:"schedule_cron_expr,omitempty"` // cron 表达式
	Enabled      bool      `json:"enabled"`       // 是否启用
	State        string    `json:"state"`         // "scheduled" / "paused" / "completed" / "error"
	NextRunAt    time.Time `json:"next_run_at"`   // 下次执行时间
	LastRunAt    time.Time `json:"last_run_at"`   // 上次执行时间
	LastStatus   string    `json:"last_status"`   // "ok" / "error" / ""
	LastError    string    `json:"last_error"`    // 上次错误信息
	CreatedAt    time.Time `json:"created_at"`    // 创建时间
}

// ───────────────────────────── 作业管理器 ─────────────────────────────

// JobManager 管理作业的全生命周期 (CRUD + 到期判定)。
// 作业数据持久化到磁盘 JSON 文件。
type JobManager struct {
	store  *state.Store // 状态存储 (可选)
	dir    string       // 作业存储目录 (~/.nexus/cron)
	mu     sync.RWMutex // 并发保护
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
		return fmt.Errorf("解析调度配置失败: %w", err)
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
		return fmt.Errorf("读取作业列表失败: %w", err)
	}

	jobs = append(jobs, job)
	if err := m.saveAll(jobs); err != nil {
		return fmt.Errorf("保存作业列表失败: %w", err)
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
		return nil, fmt.Errorf("读取作业列表失败: %w", err)
	}

	for _, job := range jobs {
		if job.ID == id {
			return job, nil
		}
	}

	return nil, fmt.Errorf("作业 '%s' 不存在", id)
}

// List 获取所有已启用的作业列表。
func (m *JobManager) List(ctx context.Context) ([]*Job, error) {
	jobs, err := m.loadAll()
	if err != nil {
		return nil, fmt.Errorf("读取作业列表失败: %w", err)
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
	m.mu.Lock()
	defer m.mu.Unlock()

	jobs, err := m.loadAll()
	if err != nil {
		return fmt.Errorf("读取作业列表失败: %w", err)
	}

	found := false
	for i, existing := range jobs {
		if existing.ID == job.ID {
			// 重新解析调度配置
			kind, minutes, cronExpr, err := parseSchedule(job.Schedule)
			if err != nil {
				return fmt.Errorf("解析调度配置失败: %w", err)
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
		return fmt.Errorf("作业 '%s' 不存在", job.ID)
	}

	if err := m.saveAll(jobs); err != nil {
		return fmt.Errorf("保存作业列表失败: %w", err)
	}

	return nil
}

// Delete 根据 ID 删除作业。
func (m *JobManager) Delete(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	jobs, err := m.loadAll()
	if err != nil {
		return fmt.Errorf("读取作业列表失败: %w", err)
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
		return fmt.Errorf("作业 '%s' 不存在", id)
	}

	if err := m.saveAll(filtered); err != nil {
		return fmt.Errorf("保存作业列表失败: %w", err)
	}

	slog.Info("Cron: job deleted", "id", id)
	return nil
}

// GetDueJobs 获取所有到期的作业。
//
// 对于重复作业 (cron/interval)，如果错过的时间超过半个周期，
// 将跳过本次执行并快进到下次计划时间，避免在网关重启后出现批量执行。
func (m *JobManager) GetDueJobs(ctx context.Context) ([]*Job, error) {
	now := time.Now()
	jobs, err := m.loadAll()
	if err != nil {
		return nil, fmt.Errorf("读取作业列表失败: %w", err)
	}

	var due []*Job
	var needsUpdate []*Job

	for _, job := range jobs {
		if !job.Enabled {
			continue
		}
		if job.NextRunAt.IsZero() {
			continue
		}

		if job.NextRunAt.After(now) {
			continue
		}

		// 对于重复作业，检查是否错过太久
		kind := job.ScheduleKind
		if kind == "interval" || kind == "cron" {
			graceSeconds := computeGraceSeconds(job)
			missed := now.Sub(job.NextRunAt).Seconds()
			if missed > float64(graceSeconds) {
				// 错过时间 > 半个周期 — 快进
				newNext := advanceToNext(job, now)
				if newNext != nil {
					slog.Info("Cron: job missed schedule, fast-forwarding to next",
						"id", job.ID,
						"name", job.Name,
						"missed_seconds", int(missed),
						"grace_seconds", graceSeconds,
						"new_next", newNext.NextRunAt,
					)
					needsUpdate = append(needsUpdate, newNext)
				}
				continue
			}
		}

		due = append(due, job)
	}

	// 批量更新快进的作业
	if len(needsUpdate) > 0 {
		allJobs, _ := m.loadAll()
		for _, updated := range needsUpdate {
			for i, j := range allJobs {
				if j.ID == updated.ID {
					allJobs[i] = updated
					break
				}
			}
		}
		if err := m.saveAll(allJobs); err != nil {
			slog.Warn("Cron: batch update jobs failed", "error", err)
		}
	}

	return due, nil
}

// AdvanceNextRun 在执行前预计算下次执行时间 (用于 at-most-once 语义)。
// 必须在作业执行前调用，这样即使进程在执行中崩溃，
// 作业也不会在重启后重复触发。
// 返回 true 表示 next_run_at 已被更新。
func (m *JobManager) AdvanceNextRun(ctx context.Context, job *Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if job.ScheduleKind != "interval" && job.ScheduleKind != "cron" {
		return nil // 一次性作业不推进
	}

	jobs, err := m.loadAll()
	if err != nil {
		return fmt.Errorf("读取作业列表失败: %w", err)
	}

	now := time.Now()
	found := false

	for i, existing := range jobs {
		if existing.ID == job.ID {
			newNext := advanceToNext(existing, now)
			if newNext != nil {
				newNext.LastRunAt = now
				jobs[i] = newNext
				found = true
			}
			break
		}
	}

	if !found {
		return fmt.Errorf("作业 '%s' 不存在", job.ID)
	}

	if err := m.saveAll(jobs); err != nil {
		return fmt.Errorf("保存作业列表失败: %w", err)
	}

	return nil
}

// ───────────────────────────── 持久化 ─────────────────────────────

// loadAll 从磁盘加载所有作业。
func (m *JobManager) loadAll() ([]*Job, error) {
	if err := os.MkdirAll(m.dir, 0700); err != nil {
		return nil, fmt.Errorf("创建目录失败: %w", err)
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
		return nil, fmt.Errorf("解析 jobs.json 失败: %w", err)
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
		tmpFile.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return err
	}
	tmpFile.Close()

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}

	os.Chmod(path, 0600)
	return nil
}

// ───────────────────────────── 调度解析 ─────────────────────────────

// ParseSchedule 解析调度字符串。
//
// 支持的格式:
//   - "30m" / "2h" / "1d" — 从现在起的一次性延迟
//   - "every 30m" / "every 2h" — 固定间隔重复
//   - "0 9 * * *" — cron 表达式 (5 字段)
//   - "2026-02-03T14:00" — ISO 时间戳一次性执行
func ParseSchedule(schedule string) (kind string, nextRun time.Time, err error) {
	k, minutes, cronExpr, e := parseSchedule(schedule)
	if e != nil {
		return "", time.Time{}, e
	}
	now := time.Now()
	switch k {
	case "once":
		return "once", now.Add(time.Duration(minutes) * time.Minute), nil
	case "interval":
		return "interval", now.Add(time.Duration(minutes) * time.Minute), nil
	case "cron":
		return "cron", nextCronTime(cronExpr, now), nil
	default:
		return "", time.Time{}, fmt.Errorf("未知的调度类型: %s", k)
	}
}

// parseSchedule 解析调度字符串的内部实现。
func parseSchedule(s string) (kind string, minutes int, cronExpr string, err error) {
	s = strings.TrimSpace(s)
	lower := strings.ToLower(s)

	// "every X" 模式 → 间隔重复
	if strings.HasPrefix(lower, "every ") {
		durStr := strings.TrimSpace(s[6:])
		mins, err := parseDuration(durStr)
		if err != nil {
			return "", 0, "", fmt.Errorf("无效的间隔 '%s': %w", durStr, err)
		}
		return "interval", mins, "", nil
	}

	// Cron 表达式 (5 个由空格分隔的字段)
	parts := strings.Fields(s)
	if len(parts) >= 5 {
		allCronChars := true
		for _, p := range parts[:5] {
			if !cronFieldRe.MatchString(p) {
				allCronChars = false
				break
			}
		}
		if allCronChars {
			return "cron", 0, s, nil
		}
	}

	// ISO 时间戳
	if strings.Contains(s, "T") || isoDateRe.MatchString(s) {
		// 解析为一次性作业 — 计算距离现在的分钟数
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			// 尝试其他格式
			t, err = time.Parse("2006-01-02T15:04", s)
			if err != nil {
				return "", 0, "", fmt.Errorf("无效的时间戳 '%s': %w", s, err)
			}
		}
		delay := time.Until(t)
		if delay < 0 {
			delay = 0
		}
		return "once", int(delay.Minutes()), "", nil
	}

	// 持续时间 "30m", "2h", "1d" → 一次性
	mins, err := parseDuration(s)
	if err != nil {
		return "", 0, "", fmt.Errorf(
			"无效的调度 '%s'。支持的格式:\n"+
				"  - 持续时间: '30m', '2h', '1d' (一次性)\n"+
				"  - 间隔: 'every 30m', 'every 2h' (重复)\n"+
				"  - Cron: '0 9 * * *' (cron 表达式)\n"+
				"  - 时间戳: '2026-02-03T14:00' (一次性)", s,
		)
	}
	return "once", mins, "", nil
}

// parseDuration 解析持续时间字符串为分钟数。
// 支持: 30m / 2h / 1d 及其变体。
func parseDuration(s string) (int, error) {
	s = strings.TrimSpace(s)
	// 匹配模式: 数字 + 可选空格 + 单位
	matches := durationRe.FindStringSubmatch(strings.ToLower(s))
	if matches == nil {
		return 0, fmt.Errorf("无效的持续时间: '%s'。格式: '30m', '2h', '1d'", s)
	}

	value, _ := strconv.Atoi(matches[1])
	unit := matches[2][0] // 首字符: m, h, d

	multipliers := map[byte]int{
		'm': 1,
		'h': 60,
		'd': 1440,
	}

	return value * multipliers[unit], nil
}

// nextCronTime 计算 cron 表达式的下一个触发时间。
// 使用简单的 cron 表达式解析 (5 字段: minute hour day month weekday)。
func nextCronTime(expr string, from time.Time) time.Time {
	// 简单实现: 尝试每分钟递增，直到匹配 (最多搜索 366 天)
	// 生产环境建议集成 robfig/cron 库，但当前按用户要求自实现
	fields := strings.Fields(expr)
	if len(fields) < 5 {
		return from // 无效的 cron 表达式
	}

	maxIterations := 366 * 24 * 60 // 1 年
	current := from.Truncate(time.Minute).Add(time.Minute)

	for i := 0; i < maxIterations; i++ {
		if matchCron(current, fields) {
			return current
		}
		current = current.Add(time.Minute)
	}

	return from.Add(24 * time.Hour) // 兜底: 1 天后
}

// matchCron 检查给定时间是否匹配 cron 字段。
func matchCron(t time.Time, fields []string) bool {
	return matchCronField(t.Minute(), fields[0], 0, 59) &&
		matchCronField(t.Hour(), fields[1], 0, 23) &&
		matchCronField(t.Day(), fields[2], 1, 31) &&
		matchCronField(int(t.Month()), fields[3], 1, 12) &&
		matchCronField(int(t.Weekday()), fields[4], 0, 6)
}

// matchCronField 检查值是否匹配单个 cron 字段。
func matchCronField(value int, field string, min, max int) bool {
	if field == "*" {
		return true
	}

	// 处理逗号分隔的值
	parts := strings.Split(field, ",")
	for _, part := range parts {
		// 处理范围 (如 1-5)
		if strings.Contains(part, "-") {
			rangeParts := strings.SplitN(part, "-", 2)
			start, err1 := strconv.Atoi(rangeParts[0])
			end, err2 := strconv.Atoi(rangeParts[1])
			if err1 == nil && err2 == nil && value >= start && value <= end {
				return true
			}
			continue
		}

		// 处理步长 (如 */5)
		if strings.Contains(part, "/") {
			stepParts := strings.SplitN(part, "/", 2)
			step, err := strconv.Atoi(stepParts[1])
			if err == nil && step > 0 {
				if stepParts[0] == "*" && value%step == 0 {
					return true
				}
				base, err := strconv.Atoi(stepParts[0])
				if err == nil && value >= base && (value-base)%step == 0 {
					return true
				}
			}
			continue
		}

		// 单个值
		v, err := strconv.Atoi(part)
		if err == nil && v == value {
			return true
		}
	}

	return false
}

// computeGraceSeconds 计算作业错过执行时的容忍宽限期。
// 使用半个间隔周期，钳制在 [120, 7200] 秒之间。
func computeGraceSeconds(job *Job) int {
	switch job.ScheduleKind {
	case "interval":
		periodSeconds := job.ScheduleMinutes * 60
		grace := periodSeconds / 2
		if grace < minGraceSeconds {
			return minGraceSeconds
		}
		if grace > maxGraceSeconds {
			return maxGraceSeconds
		}
		return grace
	case "cron":
		// cron 作业前两次触发之间计算周期估计
		now := time.Now()
		n1 := nextCronTime(job.ScheduleCronExpr, now)
		n2 := nextCronTime(job.ScheduleCronExpr, n1)
		periodSeconds := int(n2.Sub(n1).Seconds())
		grace := periodSeconds / 2
		if grace < minGraceSeconds {
			return minGraceSeconds
		}
		if grace > maxGraceSeconds {
			return maxGraceSeconds
		}
		return grace
	default:
		return minGraceSeconds
	}
}

// advanceToNext 计算作业的下一次执行时间。
func advanceToNext(job *Job, now time.Time) *Job {
	updated := *job // 浅拷贝

	switch job.ScheduleKind {
	case "interval":
		updated.NextRunAt = now.Add(time.Duration(job.ScheduleMinutes) * time.Minute)
	case "cron":
		updated.NextRunAt = nextCronTime(job.ScheduleCronExpr, now)
	case "once":
		updated.NextRunAt = time.Time{} // 无下次执行
		updated.State = "completed"
	}

	return &updated
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
