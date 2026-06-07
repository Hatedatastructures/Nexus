package cron

import (
	"context"
	"fmt"
	"log/slog"
	pkgerrors "nexus-agent/internal/errors"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// 预编译正则表达式，避免每次函数调用时重复编译带来的性能开销
var (
	cronFieldRe = regexp.MustCompile(`^[\d\*\-,/]+$`)
	isoDateRe   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}`)
	durationRe  = regexp.MustCompile(`^(\d+)\s*(m|min|mins|minute|minutes|h|hr|hrs|hour|hours|d|day|days)$`)
)

// ───────────────────────────── 常量 ─────────────────────────────

const (
	// minGraceSeconds 错过时间的最大容忍秒数下限
	minGraceSeconds = 120

	// maxGraceSeconds 错过时间的最大容忍秒数上限 (2 小时)
	maxGraceSeconds = 7200
)

// ───────────────────────────── 作业到期判定 ─────────────────────────────

// GetDueJobs 获取所有到期的作业。
//
// 对于重复作业 (cron/interval)，如果错过的时间超过半个周期，
// 将跳过本次执行并快进到下次计划时间，避免在网关重启后出现批量执行。
func (m *JobManager) GetDueJobs(ctx context.Context) ([]*Job, error) {
	now := time.Now()
	jobs, err := m.loadAll()
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.CronJob, "读取作业列表失败", err)
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
		allJobs, err := m.loadAll()
		if err != nil {
			slog.Warn("Cron: loadAll for batch update failed", "error", err)
			return due, nil
		}
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
		return pkgerrors.Wrap(pkgerrors.CronJob, "读取作业列表失败", err)
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
		return pkgerrors.New(pkgerrors.CronJob, fmt.Sprintf("作业 '%s' 不存在", job.ID))
	}

	if err := m.saveAll(jobs); err != nil {
		return pkgerrors.Wrap(pkgerrors.CronJob, "保存作业列表失败", err)
	}

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
		return "", time.Time{}, pkgerrors.New(pkgerrors.CronJob, fmt.Sprintf("未知的调度类型: %s", k))
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
			return "", 0, "", pkgerrors.Wrap(pkgerrors.CronJob, fmt.Sprintf("无效的间隔 '%s'", durStr), err)
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
				return "", 0, "", pkgerrors.Wrap(pkgerrors.CronJob, fmt.Sprintf("无效的时间戳 '%s'", s), err)
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
		return "", 0, "", pkgerrors.New(pkgerrors.CronJob, fmt.Sprintf(
			"无效的调度 '%s'。支持的格式:\n"+
				"  - 持续时间: '30m', '2h', '1d' (一次性)\n"+
				"  - 间隔: 'every 30m', 'every 2h' (重复)\n"+
				"  - Cron: '0 9 * * *' (cron 表达式)\n"+
				"  - 时间戳: '2026-02-03T14:00' (一次性)", s,
		))
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
		return 0, pkgerrors.New(pkgerrors.CronJob, fmt.Sprintf("无效的持续时间: '%s'。格式: '30m', '2h', '1d'", s))
	}

	value, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, pkgerrors.Wrap(pkgerrors.CronJob, fmt.Sprintf("无效的数字: '%s'", matches[1]), err)
	}
	unit := matches[2][0] // 首字符: m, h, d

	multipliers := map[byte]int{
		'm': 1,
		'h': 60,
		'd': 1440,
	}

	mult, ok := multipliers[unit]
	if !ok {
		return 0, pkgerrors.New(pkgerrors.CronJob, fmt.Sprintf("不支持的时间单位: '%c'", unit))
	}
	return value * mult, nil
}

// ───────────────────────────── Cron 表达式计算 ─────────────────────────────

// nextCronTime 计算 cron 表达式的下一个触发时间。
// 使用简单的 cron 表达式解析 (5 字段: minute hour day month weekday)。
func nextCronTime(expr string, from time.Time) time.Time {
	// 简单实现: 尝试每分钟递增，直到匹配 (最多搜索 366 天)
	// 生产环境建议集成 robfig/cron 库，但当前按用户要求自实现
	fields := strings.Fields(expr)
	if len(fields) < 5 {
		return from.Add(24 * time.Hour) // 无效的 cron 表达式，兜底 24 小时后
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
