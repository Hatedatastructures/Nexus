package cron

import (
	"strings"
	"testing"
	"time"
)

// ═══════════════════════════════════════════════ parseDuration ═══════════════════════════════════════════════

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input   string
		expect  int
		wantErr bool
	}{
		{"30m", 30, false},
		{"2h", 120, false},
		{"1d", 1440, false},
		{"5min", 5, false},
		{"3hours", 180, false},
		{"2days", 2880, false},
		{"60 minutes", 60, false},
		{"1hr", 60, false},
		{"  2h  ", 120, false},
		{"", 0, true},
		{"abc", 0, true},
		{"30x", 0, true},
		{"m", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseDuration(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseDuration(%q) 应返回错误", tt.input)
				}
			} else {
				if err != nil {
					t.Fatalf("parseDuration(%q) 不应返回错误: %v", tt.input, err)
				}
				if got != tt.expect {
					t.Errorf("parseDuration(%q) = %d, 期望 %d", tt.input, got, tt.expect)
				}
			}
		})
	}
}

// ═══════════════════════════════════════════════ parseSchedule ═══════════════════════════════════════════════

func TestParseSchedule(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantKind     string
		wantMinutes  int
		wantCronExpr string
		wantErr      bool
	}{
		{"间隔 every 30m", "every 30m", "interval", 30, "", false},
		{"间隔 every 2h", "every 2h", "interval", 120, "", false},
		{"Cron 每小时", "0 * * * *", "cron", 0, "0 * * * *", false},
		{"Cron 每天 9 点", "0 9 * * *", "cron", 0, "0 9 * * *", false},
		{"Cron 步长 */5", "*/5 * * * *", "cron", 0, "*/5 * * * *", false},
		{"持续时间 30m (一次性)", "30m", "once", 30, "", false},
		{"持续时间 2h (一次性)", "2h", "once", 120, "", false},
		{"持续时间 1d (一次性)", "1d", "once", 1440, "", false},
		{"ISO 时间戳过去", "2020-01-01T00:00:00Z", "once", 0, "", false},
		{"无效调度", "invalid", "", 0, "", true},
		{"空字符串", "", "", 0, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind, minutes, cronExpr, err := parseSchedule(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseSchedule(%q) 应返回错误", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSchedule(%q) 不应返回错误: %v", tt.input, err)
			}
			if kind != tt.wantKind {
				t.Errorf("kind = %q, 期望 %q", kind, tt.wantKind)
			}
			if tt.wantKind != "cron" && minutes != tt.wantMinutes {
				t.Errorf("minutes = %d, 期望 %d", minutes, tt.wantMinutes)
			}
			if tt.wantCronExpr != "" && cronExpr != tt.wantCronExpr {
				t.Errorf("cronExpr = %q, 期望 %q", cronExpr, tt.wantCronExpr)
			}
		})
	}
}

// ═══════════════════════════════════════════════ matchCronField ═══════════════════════════════════════════════

func TestMatchCronField(t *testing.T) {
	tests := []struct {
		name   string
		value  int
		field  string
		expect bool
	}{
		{"通配符 *", 5, "*", true},
		{"精确匹配", 5, "5", true},
		{"精确不匹配", 5, "6", false},
		{"范围匹配内", 3, "1-5", true},
		{"范围匹配外", 6, "1-5", false},
		{"范围边界起", 1, "1-5", true},
		{"范围边界终", 5, "1-5", true},
		{"步长 */5 值 10", 10, "*/5", true},
		{"步长 */5 值 3", 3, "*/5", false},
		{"步长 */5 值 0", 0, "*/5", true},
		{"步长 */15 值 30", 30, "*/15", true},
		{"步长带基数 2/5 匹配 7", 7, "2/5", true},
		{"步长带基数不匹配 4", 4, "2/5", false},
		{"逗号匹配 1", 1, "1,3,5", true},
		{"逗号不匹配 2", 2, "1,3,5", false},
		{"逗号匹配 5", 5, "1,3,5", true},
		{"0 匹配", 0, "0", true},
		{"星期范围 3", 3, "1-5", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchCronField(tt.value, tt.field, 0, 59)
			if got != tt.expect {
				t.Errorf("matchCronField(%d, %q) = %v, 期望 %v",
					tt.value, tt.field, got, tt.expect)
			}
		})
	}
}

// ═══════════════════════════════════════════════ matchCron ═══════════════════════════════════════════════

func TestMatchCron(t *testing.T) {
	// 2026-01-01 是 Thursday (weekday=4)
	t1 := time.Date(2026, 1, 1, 9, 30, 0, 0, time.Local)

	tests := []struct {
		name   string
		time   time.Time
		expr   string
		expect bool
	}{
		{"精确匹配 9:30 每天", t1, "30 9 * * *", true},
		{"分钟不匹配", t1, "31 9 * * *", false},
		{"小时不匹配", t1, "30 10 * * *", false},
		{"全部通配", t1, "* * * * *", true},
		{"仅工作日 (周四=4)", t1, "30 9 * * 1-5", true},
		{"非工作日", t1, "30 9 * * 0,6", false},
		{"月份匹配", t1, "30 9 1 1 *", true},
		{"月份不匹配", t1, "30 9 1 2 *", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := strings.Fields(tt.expr)
			got := matchCron(tt.time, fields)
			if got != tt.expect {
				t.Errorf("matchCron(%v, %q) = %v, 期望 %v", tt.time, tt.expr, got, tt.expect)
			}
		})
	}
}

// ═══════════════════════════════════════════════ nextCronTime ═══════════════════════════════════════════════

func TestNextCronTime(t *testing.T) {
	t.Run("今天 9 点前", func(t *testing.T) {
		from := time.Date(2026, 1, 1, 8, 0, 0, 0, time.Local)
		r := nextCronTime("0 9 * * *", from)
		if r.Hour() != 9 || r.Minute() != 0 {
			t.Errorf("期望 09:00, 实际 %v", r.Format("15:04"))
		}
		if r.Day() != 1 {
			t.Errorf("应在今天触发, 实际日期 %d", r.Day())
		}
	})

	t.Run("今天 9 点后 → 明天", func(t *testing.T) {
		from := time.Date(2026, 1, 1, 10, 0, 0, 0, time.Local)
		r := nextCronTime("0 9 * * *", from)
		if r.Hour() != 9 || r.Minute() != 0 {
			t.Errorf("期望 09:00, 实际 %v", r.Format("15:04"))
		}
		if r.Day() != 2 {
			t.Errorf("应在明天触发, 实际日期 %d", r.Day())
		}
	})

	t.Run("每小时 :30", func(t *testing.T) {
		from := time.Date(2026, 1, 1, 9, 15, 0, 0, time.Local)
		r := nextCronTime("30 * * * *", from)
		if r.Minute() != 30 {
			t.Errorf("期望 :30, 实际 %v", r.Format("15:04"))
		}
		if r.Hour() != 9 {
			t.Errorf("应在同一小时触发, 实际 %d 时", r.Hour())
		}
	})

	t.Run("无效表达式返回 from+24h", func(t *testing.T) {
		from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
		r := nextCronTime("invalid", from)
		expected := from.Add(24 * time.Hour)
		if !r.Equal(expected) {
			t.Errorf("无效表达式应返回 from+24h, 实际 %v", r)
		}
	})

	t.Run("不返回 from 本身", func(t *testing.T) {
		from := time.Date(2026, 1, 1, 9, 0, 0, 0, time.Local)
		r := nextCronTime("0 9 * * *", from)
		if r.Equal(from) {
			t.Error("nextCronTime 不应返回 from 本身")
		}
	})
}

// ═══════════════════════════════════════════════ generateJobID ═══════════════════════════════════════════════

func TestGenerateJobID(t *testing.T) {
	id := generateJobID()
	if len(id) != 12 {
		t.Fatalf("generateJobID 长度 = %d, 期望 12", len(id))
	}
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("ID 应为十六进制, 包含: %c", c)
		}
	}

	id2 := generateJobID()
	if id == id2 {
		t.Fatal("两次生成的 ID 不应相同")
	}
}

// ═══════════════════════════════════════════════ computeGraceSeconds ═══════════════════════════════════════════════

func TestComputeGraceSeconds(t *testing.T) {
	tests := []struct {
		name   string
		job    *Job
		expect int
	}{
		{"间隔 10 分钟 → 半周期 300s", &Job{ScheduleKind: "interval", ScheduleMinutes: 10}, 300},
		{"间隔 1 分钟 → 最小值 120s", &Job{ScheduleKind: "interval", ScheduleMinutes: 1}, 120},
		{"间隔 1 天 → 最大值 7200s", &Job{ScheduleKind: "interval", ScheduleMinutes: 1440}, 7200},
		{"未知类型 → 最小值 120s", &Job{ScheduleKind: "unknown"}, 120},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeGraceSeconds(tt.job)
			if got != tt.expect {
				t.Errorf("computeGraceSeconds = %d, 期望 %d", got, tt.expect)
			}
		})
	}
}

func TestComputeGraceSeconds_Cron(t *testing.T) {
	job := &Job{ScheduleKind: "cron", ScheduleCronExpr: "0 */6 * * *"}
	grace := computeGraceSeconds(job)
	if grace != 7200 {
		t.Errorf("cron 6h grace = %d, 期望 7200 (上限)", grace)
	}

	job2 := &Job{ScheduleKind: "cron", ScheduleCronExpr: "0 * * * *"}
	grace2 := computeGraceSeconds(job2)
	if grace2 != 1800 {
		t.Errorf("cron 1h grace = %d, 期望 1800", grace2)
	}
}

// ═══════════════════════════════════════════════ advanceToNext ═══════════════════════════════════════════════

func TestAdvanceToNext(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.Local)

	t.Run("interval 作业", func(t *testing.T) {
		job := &Job{ScheduleKind: "interval", ScheduleMinutes: 30, NextRunAt: now}
		next := advanceToNext(job, now)
		expected := now.Add(30 * time.Minute)
		if !next.NextRunAt.Equal(expected) {
			t.Errorf("NextRunAt = %v, 期望 %v", next.NextRunAt, expected)
		}
	})

	t.Run("cron 作业", func(t *testing.T) {
		job := &Job{ScheduleKind: "cron", ScheduleCronExpr: "0 9 * * *", NextRunAt: now}
		next := advanceToNext(job, now)
		if next.NextRunAt.Hour() != 9 {
			t.Errorf("cron advance Hour = %d, 期望 9", next.NextRunAt.Hour())
		}
	})

	t.Run("once 作业", func(t *testing.T) {
		job := &Job{ScheduleKind: "once", NextRunAt: now, State: "scheduled"}
		next := advanceToNext(job, now)
		if !next.NextRunAt.IsZero() {
			t.Errorf("一次性作业 NextRunAt 应为零值, 实际 %v", next.NextRunAt)
		}
		if next.State != "completed" {
			t.Errorf("State = %q, 期望 'completed'", next.State)
		}
	})

	t.Run("不修改原始 job", func(t *testing.T) {
		job := &Job{ScheduleKind: "interval", ScheduleMinutes: 30, NextRunAt: now, State: "scheduled"}
		advanceToNext(job, now)
		if !job.NextRunAt.Equal(now) {
			t.Error("advanceToNext 不应修改原始 job")
		}
		if job.State != "scheduled" {
			t.Error("advanceToNext 不应修改原始 job 的 State")
		}
	})
}
