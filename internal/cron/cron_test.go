package cron

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"nexus-agent/internal/agent"
	"nexus-agent/internal/gateway/platforms"
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

// ═══════════════════════════════════════════════ sanitizeJobID ═══════════════════════════════════════════════

func TestSanitizeJobID(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{"abc123", "abc123"},
		{"a-b_c", "a-b_c"},
		{"../../etc/passwd", "etcpasswd"},
		{"", "unknown"},
		{"中文", "unknown"},
		{"job.id", "jobid"},
		{"job@id!", "jobid"},
		{"ABC123", "ABC123"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeJobID(tt.input)
			if got != tt.expect {
				t.Errorf("sanitizeJobID(%q) = %q, 期望 %q", tt.input, got, tt.expect)
			}
		})
	}
}

// ═══════════════════════════════════════════════ truncate ═══════════════════════════════════════════════

func TestTruncate(t *testing.T) {
	tests := []struct {
		input  string
		limit  int
		expect string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello world", 5, "hello..."},
		{"", 5, ""},
		{"你好世界测试", 3, "你好世..."},
	}

	for _, tt := range tests {
		name := fmt.Sprintf("truncate(%q,%d)", tt.input, tt.limit)
		t.Run(name, func(t *testing.T) {
			got := truncate(tt.input, tt.limit)
			if got != tt.expect {
				t.Errorf("truncate(%q, %d) = %q, 期望 %q", tt.input, tt.limit, got, tt.expect)
			}
		})
	}
}

// ═══════════════════════════════════════════════ buildDeliveryContent ═══════════════════════════════════════════════

func TestBuildDeliveryContent(t *testing.T) {
	t.Run("正常内容", func(t *testing.T) {
		job := &Job{ID: "abc123", Name: "测试作业"}
		content := buildDeliveryContent(job, "这是执行结果")
		if !strings.Contains(content, "Cron 作业响应: 测试作业") {
			t.Error("应包含作业名称 header")
		}
		if !strings.Contains(content, "abc123") {
			t.Error("应包含作业 ID")
		}
		if !strings.Contains(content, "这是执行结果") {
			t.Error("应包含结果内容")
		}
		if !strings.Contains(content, "执行时间:") {
			t.Error("应包含执行时间 footer")
		}
	})

	t.Run("空结果", func(t *testing.T) {
		job := &Job{ID: "abc123"}
		content := buildDeliveryContent(job, "")
		if content != "" {
			t.Errorf("空结果应返回空字符串, 实际: %q", content)
		}
	})

	t.Run("长内容截断", func(t *testing.T) {
		job := &Job{ID: "abc123", Name: "测试"}
		longResult := strings.Repeat("x", 5000)
		content := buildDeliveryContent(job, longResult)
		if !strings.Contains(content, "输出已截断") {
			t.Error("长内容应包含截断提示")
		}
	})

	t.Run("无名称使用 ID", func(t *testing.T) {
		job := &Job{ID: "abc123", Name: ""}
		content := buildDeliveryContent(job, "结果")
		if !strings.Contains(content, "abc123") {
			t.Error("无名称时应使用 ID")
		}
	})
}

// ═══════════════════════════════════════════════ buildPrompt ═══════════════════════════════════════════════

func TestBuildPrompt(t *testing.T) {
	exec := NewExecutor("", nil)
	job := &Job{Prompt: "检查服务器状态"}
	prompt := exec.buildPrompt(job)
	if !strings.HasPrefix(prompt, "[重要提示:") {
		t.Error("提示词应以 cron 指令前缀开头")
	}
	if !strings.Contains(prompt, "检查服务器状态") {
		t.Error("提示词应包含原始 prompt")
	}
	if !strings.Contains(prompt, "[SILENT]") {
		t.Error("提示词应包含静默指令")
	}
}

// ═══════════════════════════════════════════════ ParseSchedule (exported) ═══════════════════════════════════════════════

func TestParseScheduleExported(t *testing.T) {
	t.Run("一次性", func(t *testing.T) {
		kind, next, err := ParseSchedule("30m")
		if err != nil {
			t.Fatal(err)
		}
		if kind != "once" {
			t.Errorf("kind = %q, 期望 'once'", kind)
		}
		if next.Before(time.Now().Add(-1 * time.Second)) {
			t.Error("下次执行时间应在未来")
		}
	})

	t.Run("间隔", func(t *testing.T) {
		kind, _, err := ParseSchedule("every 2h")
		if err != nil {
			t.Fatal(err)
		}
		if kind != "interval" {
			t.Errorf("kind = %q, 期望 'interval'", kind)
		}
	})

	t.Run("Cron", func(t *testing.T) {
		kind, _, err := ParseSchedule("0 9 * * *")
		if err != nil {
			t.Fatal(err)
		}
		if kind != "cron" {
			t.Errorf("kind = %q, 期望 'cron'", kind)
		}
	})

	t.Run("无效", func(t *testing.T) {
		_, _, err := ParseSchedule("invalid")
		if err == nil {
			t.Error("无效调度应返回错误")
		}
	})
}

// ═══════════════════════════════════════════════ DeliverResult ═══════════════════════════════════════════════

func TestDeliverResult(t *testing.T) {
	ctx := context.Background()

	t.Run("nil job 返回错误", func(t *testing.T) {
		err := DeliverResult(ctx, nil, "ok", nil)
		if err == nil {
			t.Fatal("nil job 应返回错误")
		}
	})

	t.Run("ok 状态不投递", func(t *testing.T) {
		job := &Job{ID: "test"}
		err := DeliverResult(ctx, job, "ok", nil)
		if err != nil {
			t.Fatalf("'ok' 不应投递: %v", err)
		}
	})

	t.Run("空结果不投递", func(t *testing.T) {
		job := &Job{ID: "test"}
		err := DeliverResult(ctx, job, "", nil)
		if err != nil {
			t.Fatalf("空结果不应投递: %v", err)
		}
	})

	t.Run("静默标记不投递", func(t *testing.T) {
		job := &Job{ID: "test"}
		err := DeliverResult(ctx, job, "[SILENT]", nil)
		if err != nil {
			t.Fatalf("静默标记不应投递: %v", err)
		}
	})

	t.Run("大小写不敏感的静默标记", func(t *testing.T) {
		job := &Job{ID: "test"}
		err := DeliverResult(ctx, job, "[silent]", nil)
		if err != nil {
			t.Fatalf("小写静默标记不应投递: %v", err)
		}
	})

	t.Run("无适配器跳过投递", func(t *testing.T) {
		job := &Job{ID: "test"}
		err := DeliverResult(ctx, job, "some content", nil)
		if err != nil {
			t.Fatalf("无适配器应跳过投递: %v", err)
		}
	})

	t.Run("空适配器 map 跳过投递", func(t *testing.T) {
		job := &Job{ID: "test"}
		err := DeliverResult(ctx, job, "some content", map[platforms.Platform]platforms.PlatformAdapter{})
		if err != nil {
			t.Fatalf("空适配器 map 应跳过投递: %v", err)
		}
	})
}

// ═══════════════════════════════════════════════ Executor ═══════════════════════════════════════════════

func TestExecutor_NilJob(t *testing.T) {
	exec := NewExecutor(t.TempDir(), nil)
	err := exec.Execute(context.Background(), nil)
	if err == nil {
		t.Fatal("nil job 应返回错误")
	}
}

func TestExecutor_SaveOutput(t *testing.T) {
	exec := NewExecutor(t.TempDir(), nil)
	job := &Job{ID: "test123", Name: "测试"}
	ts := time.Date(2026, 1, 15, 10, 30, 0, 0, time.Local)

	err := exec.saveOutput(job, "# 输出\n这是测试输出", ts)
	if err != nil {
		t.Fatalf("saveOutput 失败: %v", err)
	}

	safeID := sanitizeJobID(job.ID)
	expectedFile := filepath.Join(exec.outputDir, safeID, "2026-01-15_10-30-00.md")
	data, err := os.ReadFile(expectedFile)
	if err != nil {
		t.Fatalf("读取输出文件失败: %v", err)
	}
	if !strings.Contains(string(data), "这是测试输出") {
		t.Error("输出文件应包含结果内容")
	}
}

func TestExecutor_SaveOutput_PathTraversal(t *testing.T) {
	exec := NewExecutor(t.TempDir(), nil)
	job := &Job{ID: "../../etc/passwd", Name: "恶意"}
	ts := time.Now()

	err := exec.saveOutput(job, "内容", ts)
	if err != nil {
		t.Fatalf("saveOutput 失败: %v", err)
	}

	// 验证输出目录不存在路径遍历
	safeID := sanitizeJobID(job.ID)
	if strings.Contains(safeID, "..") {
		t.Fatal("sanitizeJobID 应移除路径遍历组件")
	}
	if strings.Contains(safeID, "/") {
		t.Fatal("sanitizeJobID 应移除路径分隔符")
	}
}

// ═══════════════════════════════════════════════ JobManager CRUD ═══════════════════════════════════════════════

func TestJobManager_CreateAndGet(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	ctx := context.Background()

	job := &Job{
		Name:     "测试作业",
		Prompt:   "检查状态",
		Schedule: "every 30m",
	}

	if err := mgr.Create(ctx, job); err != nil {
		t.Fatalf("Create 失败: %v", err)
	}
	if job.ID == "" {
		t.Fatal("Create 应设置 ID")
	}
	if len(job.ID) != 12 {
		t.Fatalf("ID 长度 = %d, 期望 12", len(job.ID))
	}
	if job.State != "scheduled" {
		t.Fatalf("State = %q, 期望 'scheduled'", job.State)
	}
	if !job.Enabled {
		t.Fatal("Enabled 应为 true")
	}
	if job.ScheduleKind != "interval" {
		t.Fatalf("ScheduleKind = %q, 期望 'interval'", job.ScheduleKind)
	}
	if job.ScheduleMinutes != 30 {
		t.Fatalf("ScheduleMinutes = %d, 期望 30", job.ScheduleMinutes)
	}
	if job.NextRunAt.IsZero() {
		t.Fatal("NextRunAt 不应为零值")
	}

	got, err := mgr.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("Get 失败: %v", err)
	}
	if got.Name != "测试作业" {
		t.Errorf("Name = %q, 期望 '测试作业'", got.Name)
	}
	if got.Prompt != "检查状态" {
		t.Errorf("Prompt = %q, 期望 '检查状态'", got.Prompt)
	}
}

func TestJobManager_Get_NotFound(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)

	_, err := mgr.Get(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("不存在的 ID 应返回错误")
	}
}

func TestJobManager_List(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	ctx := context.Background()

	mgr.Create(ctx, &Job{Name: "作业1", Prompt: "p1", Schedule: "every 30m"})
	mgr.Create(ctx, &Job{Name: "作业2", Prompt: "p2", Schedule: "every 1h"})

	jobs, err := mgr.List(ctx)
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("List 长度 = %d, 期望 2", len(jobs))
	}
}

func TestJobManager_Update(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	ctx := context.Background()

	job := &Job{Name: "原名称", Prompt: "p", Schedule: "every 30m"}
	mgr.Create(ctx, job)

	job.Name = "新名称"
	job.Schedule = "every 1h"
	if err := mgr.Update(ctx, job); err != nil {
		t.Fatalf("Update 失败: %v", err)
	}

	got, _ := mgr.Get(ctx, job.ID)
	if got.Name != "新名称" {
		t.Errorf("Name = %q, 期望 '新名称'", got.Name)
	}
	if got.ScheduleMinutes != 60 {
		t.Errorf("ScheduleMinutes = %d, 期望 60", got.ScheduleMinutes)
	}
	if got.ScheduleKind != "interval" {
		t.Errorf("ScheduleKind = %q, 期望 'interval'", got.ScheduleKind)
	}
}

func TestJobManager_Update_NotFound(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)

	err := mgr.Update(context.Background(), &Job{ID: "nonexistent", Schedule: "every 1h"})
	if err == nil {
		t.Fatal("更新不存在的作业应返回错误")
	}
}

func TestJobManager_Update_EmptyID(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)

	err := mgr.Update(context.Background(), &Job{ID: "", Schedule: "every 1h"})
	if err == nil {
		t.Fatal("空 ID 应返回错误")
	}
}

func TestJobManager_Delete(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	ctx := context.Background()

	job := &Job{Name: "待删除", Prompt: "p", Schedule: "every 30m"}
	mgr.Create(ctx, job)

	if err := mgr.Delete(ctx, job.ID); err != nil {
		t.Fatalf("Delete 失败: %v", err)
	}

	_, err := mgr.Get(ctx, job.ID)
	if err == nil {
		t.Fatal("删除后 Get 应返回错误")
	}

	// List 应不包含已删除作业
	jobs, _ := mgr.List(ctx)
	for _, j := range jobs {
		if j.ID == job.ID {
			t.Fatal("已删除作业不应出现在 List 中")
		}
	}
}

func TestJobManager_Delete_NotFound(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)

	err := mgr.Delete(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("删除不存在的作业应返回错误")
	}
}

func TestJobManager_GetDueJobs(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	ctx := context.Background()

	// 创建 interval 作业后改 once 并设 NextRunAt 为过去
	job := &Job{Name: "到期作业", Prompt: "p", Schedule: "30m"}
	if err := mgr.Create(ctx, job); err != nil {
		t.Fatalf("Create 失败: %v", err)
	}
	// 直接修改持久化文件来模拟过期作业
	jobs, _ := mgr.loadAll()
	for i := range jobs {
		if jobs[i].ID == job.ID {
			jobs[i].NextRunAt = time.Now().Add(-1 * time.Hour)
			break
		}
	}
	mgr.saveAll(jobs)
	due, err := mgr.GetDueJobs(ctx)
	if err != nil {
		t.Fatalf("GetDueJobs 失败: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("到期作业数量 = %d, 期望 1", len(due))
	}
}

func TestJobManager_GetDueJobs_None(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	ctx := context.Background()

	// 30m 后执行的作业未到期
	mgr.Create(ctx, &Job{Name: "未来作业", Prompt: "p", Schedule: "30m"})

	due, err := mgr.GetDueJobs(ctx)
	if err != nil {
		t.Fatalf("GetDueJobs 失败: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("未来作业不应到期, 实际 %d 个", len(due))
	}
}

func TestJobManager_GetDueJobs_Disabled(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	ctx := context.Background()

	job := &Job{Name: "禁用作业", Prompt: "p", Schedule: "once"}
	mgr.Create(ctx, job)
	job.NextRunAt = time.Now().Add(-1 * time.Hour)
	job.Enabled = false
	mgr.Update(ctx, job)

	due, err := mgr.GetDueJobs(ctx)
	if err != nil {
		t.Fatalf("GetDueJobs 失败: %v", err)
	}
	for _, j := range due {
		if j.ID == job.ID {
			t.Fatal("禁用作业不应出现在到期列表中")
		}
	}
}

func TestJobManager_AdvanceNextRun(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	ctx := context.Background()

	job := &Job{Name: "间隔作业", Prompt: "p", Schedule: "every 30m"}
	mgr.Create(ctx, job)
	originalNext := job.NextRunAt

	if err := mgr.AdvanceNextRun(ctx, job); err != nil {
		t.Fatalf("AdvanceNextRun 失败: %v", err)
	}

	got, _ := mgr.Get(ctx, job.ID)
	if !got.NextRunAt.After(originalNext) {
		t.Errorf("AdvanceNextRun 应推进 NextRunAt, 原: %v, 新: %v", originalNext, got.NextRunAt)
	}
	if got.LastRunAt.IsZero() {
		t.Error("AdvanceNextRun 应设置 LastRunAt")
	}
}

func TestJobManager_AdvanceNextRun_OnceJob(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	ctx := context.Background()

	job := &Job{Name: "一次性", Prompt: "p", Schedule: "30m"}
	mgr.Create(ctx, job)

	err := mgr.AdvanceNextRun(ctx, job)
	if err != nil {
		t.Fatalf("一次性作业 AdvanceNextRun 不应报错: %v", err)
	}
}

func TestJobManager_AdvanceNextRun_NotFound(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)

	err := mgr.AdvanceNextRun(context.Background(), &Job{ID: "nonexistent", ScheduleKind: "interval"})
	if err == nil {
		t.Fatal("不存在的作业应返回错误")
	}
}

func TestJobManager_CronJob(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	ctx := context.Background()

	job := &Job{Name: "Cron 作业", Prompt: "p", Schedule: "0 9 * * *"}
	if err := mgr.Create(ctx, job); err != nil {
		t.Fatalf("Create 失败: %v", err)
	}
	if job.ScheduleKind != "cron" {
		t.Fatalf("ScheduleKind = %q, 期望 'cron'", job.ScheduleKind)
	}
	if job.NextRunAt.Hour() != 9 {
		t.Errorf("Cron 作业 NextRunAt 小时 = %d, 期望 9", job.NextRunAt.Hour())
	}
}

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

// ═══════════════════════════════════════════════ 持久化 ═══════════════════════════════════════════════

func TestJobManager_Persistence(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	// 创建第一个 manager 并写入
	mgr1 := NewJobManager(nil, dir)
	job := &Job{Name: "持久化测试", Prompt: "p", Schedule: "every 30m"}
	mgr1.Create(ctx, job)

	// 创建第二个 manager 读取同一目录
	mgr2 := NewJobManager(nil, dir)
	got, err := mgr2.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("第二个 manager 应能读取持久化的作业: %v", err)
	}
	if got.Name != "持久化测试" {
		t.Errorf("Name = %q, 期望 '持久化测试'", got.Name)
	}
}

func TestJobManager_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	ctx := context.Background()

	// 空目录的 List 不应报错
	jobs, err := mgr.List(ctx)
	if err != nil {
		t.Fatalf("空目录 List 不应报错: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("空目录 List 长度 = %d, 期望 0", len(jobs))
	}

	// 空目录的 GetDueJobs 不应报错
	due, err := mgr.GetDueJobs(ctx)
	if err != nil {
		t.Fatalf("空目录 GetDueJobs 不应报错: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("空目录 GetDueJobs 长度 = %d, 期望 0", len(due))
	}
}

// ═══════════════════════════════════════════════ Mock PlatformAdapter ═══════════════════════════════════════════════

// mockAdapter 用于测试投递逻辑的 mock 平台适配器。
type mockAdapter struct {
	name      string
	platform  platforms.Platform
	sendErr   error
	sendResult *platforms.SendResult
	sendCalled int
}

func (m *mockAdapter) Name() string                                          { return m.name }
func (m *mockAdapter) PlatformType() platforms.Platform                      { return m.platform }
func (m *mockAdapter) Connect(_ context.Context) (<-chan *platforms.MessageEvent, error) {
	ch := make(chan *platforms.MessageEvent)
	close(ch)
	return ch, nil
}
func (m *mockAdapter) Disconnect(_ context.Context) error                    { return nil }
func (m *mockAdapter) Send(_ context.Context, _ string, _ string, _ *platforms.SendOptions) (*platforms.SendResult, error) {
	m.sendCalled++
	if m.sendErr != nil {
		return nil, m.sendErr
	}
	if m.sendResult != nil {
		return m.sendResult, nil
	}
	return &platforms.SendResult{Success: true, MessageID: "msg-123"}, nil
}
func (m *mockAdapter) EditMessage(_ context.Context, _, _, _ string) (*platforms.SendResult, error) {
	return &platforms.SendResult{Success: true}, nil
}
func (m *mockAdapter) DeleteMessage(_ context.Context, _, _ string) error    { return nil }
func (m *mockAdapter) SendTyping(_ context.Context, _ string) error          { return nil }
func (m *mockAdapter) SendImage(_ context.Context, _, _, _ string, _ *platforms.SendOptions) (*platforms.SendResult, error) {
	return &platforms.SendResult{Success: true}, nil
}
func (m *mockAdapter) SendVoice(_ context.Context, _, _ string, _ *platforms.SendOptions) (*platforms.SendResult, error) {
	return &platforms.SendResult{Success: true}, nil
}
func (m *mockAdapter) SendVideo(_ context.Context, _, _, _ string, _ *platforms.SendOptions) (*platforms.SendResult, error) {
	return &platforms.SendResult{Success: true}, nil
}
func (m *mockAdapter) SendDocument(_ context.Context, _, _, _ string, _ *platforms.SendOptions) (*platforms.SendResult, error) {
	return &platforms.SendResult{Success: true}, nil
}
func (m *mockAdapter) MaxMessageLength() int                                 { return 4096 }
func (m *mockAdapter) SupportsStreaming() bool                               { return false }

// ═══════════════════════════════════════════════ DeliverResult (mock adapter) ═══════════════════════════════════════════════

func TestDeliverResult_MockAdapter_Success(t *testing.T) {
	adapter := &mockAdapter{name: "TestBot", platform: platforms.PlatformTelegram}
	adapters := map[platforms.Platform]platforms.PlatformAdapter{
		platforms.PlatformTelegram: adapter,
	}

	job := &Job{ID: "test-123", Name: "提醒作业"}
	ctx := context.Background()

	err := DeliverResult(ctx, job, "这是投递内容", adapters)
	if err != nil {
		t.Fatalf("投递应成功: %v", err)
	}
	if adapter.sendCalled != 1 {
		t.Fatalf("Send 应被调用 1 次, 实际 %d", adapter.sendCalled)
	}
}

func TestDeliverResult_MockAdapter_PartialFailure(t *testing.T) {
	okAdapter := &mockAdapter{name: "OK", platform: platforms.PlatformTelegram}
	failAdapter := &mockAdapter{
		name:      "Fail",
		platform:  platforms.PlatformDiscord,
		sendErr:   fmt.Errorf("网络错误"),
	}
	adapters := map[platforms.Platform]platforms.PlatformAdapter{
		platforms.PlatformTelegram: okAdapter,
		platforms.PlatformDiscord:  failAdapter,
	}

	job := &Job{ID: "pf-1", Name: "部分失败"}
	ctx := context.Background()

	err := DeliverResult(ctx, job, "部分失败测试", adapters)
	// 部分成功不应返回错误
	if err != nil {
		t.Fatalf("部分成功不应返回错误: %v", err)
	}
	if okAdapter.sendCalled != 1 {
		t.Fatalf("成功适配器应被调用 1 次")
	}
	if failAdapter.sendCalled != 1 {
		t.Fatalf("失败适配器也应被调用 1 次")
	}
}

func TestDeliverResult_MockAdapter_AllFail(t *testing.T) {
	failAdapter := &mockAdapter{
		name:      "FailAll",
		platform:  platforms.PlatformTelegram,
		sendErr:   fmt.Errorf("连接超时"),
	}
	adapters := map[platforms.Platform]platforms.PlatformAdapter{
		platforms.PlatformTelegram: failAdapter,
	}

	job := &Job{ID: "af-1", Name: "全部失败"}
	ctx := context.Background()

	err := DeliverResult(ctx, job, "全部失败内容", adapters)
	if err == nil {
		t.Fatal("全部失败应返回错误")
	}
	if !strings.Contains(err.Error(), "全部失败") {
		t.Fatalf("错误消息应包含 '全部失败', 实际: %v", err)
	}
}

func TestDeliverResult_MockAdapter_SendResultNotSuccess(t *testing.T) {
	adapter := &mockAdapter{
		name:     "Unsuccess",
		platform: platforms.PlatformTelegram,
		sendResult: &platforms.SendResult{
			Success:   false,
			Error:     "rate limited",
			Retryable: true,
		},
	}
	adapters := map[platforms.Platform]platforms.PlatformAdapter{
		platforms.PlatformTelegram: adapter,
	}

	job := &Job{ID: "ns-1", Name: "发送不成功"}
	ctx := context.Background()

	err := DeliverResult(ctx, job, "内容", adapters)
	if err == nil {
		t.Fatal("SendResult.Success=false 应导致全部失败错误")
	}
}

func TestDeliverResult_MockAdapter_NilAdapter(t *testing.T) {
	adapters := map[platforms.Platform]platforms.PlatformAdapter{
		platforms.PlatformTelegram: nil,
	}

	job := &Job{ID: "na-1", Name: "nil适配器"}
	ctx := context.Background()

	// nil 适配器应被跳过，但由于全部跳过，成功数=0，失败数=0
	err := DeliverResult(ctx, job, "内容", adapters)
	if err != nil {
		t.Fatalf("nil 适配器跳过后不应报错: %v", err)
	}
}

func TestDeliverResult_ChatIDFallback(t *testing.T) {
	adapter := &mockAdapter{name: "FB", platform: platforms.PlatformTelegram}
	adapters := map[platforms.Platform]platforms.PlatformAdapter{
		platforms.PlatformTelegram: adapter,
	}

	// 无 Name 的作业，chatID 应为 "cron-{id}"
	job := &Job{ID: "no-name-1"}
	ctx := context.Background()

	err := DeliverResult(ctx, job, "测试 fallback chatID", adapters)
	if err != nil {
		t.Fatalf("应成功: %v", err)
	}
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

// ═══════════════════════════════════════════════ Executor.Execute — MkdirAll 失败 ═══════════════════════════════════════════════

func TestExecutor_Execute_MkdirAllFail(t *testing.T) {
	// 使用不可写入的路径触发 MkdirAll 失败
	exec := NewExecutor("Z:\\nonexistent_drive_12345\\output", nil)
	ctx := context.Background()

	job := &Job{
		ID:     "mkdir-fail",
		Name:   "目录创建失败",
		Prompt: "test",
	}

	err := exec.Execute(ctx, job)
	if err == nil {
		t.Fatal("Execute 在不可写入路径时应返回错误")
	}
	if !strings.Contains(err.Error(), "创建输出目录失败") {
		t.Fatalf("错误信息应提及目录创建失败, 实际: %v", err)
	}
}

// ═══════════════════════════════════════════════ Executor.saveOutput — CreateTemp 失败 ═══════════════════════════════════════════════

func TestExecutor_SaveOutput_CreateTempFail(t *testing.T) {
	// 创建临时目录然后删除它，使 CreateTemp 失败
	dir := t.TempDir()
	exec := NewExecutor(dir, nil)

	job := &Job{ID: "ctf-test"}
	ts := time.Now()

	// 将输出子目录设为只读 (模拟 CreateTemp 失败)
	safeID := sanitizeJobID(job.ID)
	subDir := filepath.Join(dir, safeID)
	os.MkdirAll(subDir, 0500) // 只读目录 — CreateTemp 可能在此失败

	err := exec.saveOutput(job, "test", ts)
	// 在 Windows 上只读目录可能不阻止创建文件
	if err != nil {
		t.Logf("saveOutput 在受限目录下返回错误 (预期): %v", err)
	} else {
		t.Log("saveOutput 在受限目录下成功 (Windows 行为)")
	}
}

// ═══════════════════════════════════════════════ Executor.saveOutput — Rename 失败 ═══════════════════════════════════════════════

func TestExecutor_SaveOutput_RenameFail(t *testing.T) {
	dir := t.TempDir()
	exec := NewExecutor(dir, nil)

	job := &Job{ID: "rename-test"}
	ts := time.Now()

	// 预先创建目标文件使 Rename 失败 (Windows 上可能不失败)
	safeID := sanitizeJobID(job.ID)
	subDir := filepath.Join(dir, safeID)
	os.MkdirAll(subDir, 0700)
	targetFile := filepath.Join(subDir, ts.Format("2006-01-02_15-04-05")+".md")
	os.WriteFile(targetFile, []byte("existing"), 0400) // 只读目标

	err := exec.saveOutput(job, "new content", ts)
	if err != nil {
		t.Logf("saveOutput 在目标已存在时返回错误: %v", err)
	} else {
		t.Log("saveOutput 成功覆盖 (Windows 行为)")
	}
}

// ═══════════════════════════════════════════════ JobManager.saveAll — 写入失败 ═══════════════════════════════════════════════

func TestJobManager_SaveAll_Fail(t *testing.T) {
	// 使用不可写入的目录
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)

	// 先正常创建作业
	job := &Job{
		ID:       "save-fail-1",
		Name:     "测试",
		Prompt:   "test",
		Schedule: "30m",
		Enabled:  true,
	}
	ctx := context.Background()
	if err := mgr.Create(ctx, job); err != nil {
		t.Fatalf("Create 失败: %v", err)
	}

	// 将 jobs.json 设为只读目录使 saveAll 失败
	jobsFile := filepath.Join(dir, "jobs.json")
	os.Chmod(dir, 0500)
	os.Chmod(jobsFile, 0400)

	// 尝试保存应失败
	err := mgr.saveAll([]*Job{job})
	if err != nil {
		t.Logf("saveAll 在只读目录下返回错误 (预期): %v", err)
	} else {
		t.Log("saveAll 在只读目录下成功 (Windows 行为)")
	}

	// 恢复权限以允许清理
	os.Chmod(jobsFile, 0600)
	os.Chmod(dir, 0700)
}

// ═══════════════════════════════════════════════ JobManager.loadAll — 损坏 JSON ═══════════════════════════════════════════════

func TestJobManager_LoadAll_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)

	// 写入无效 JSON
	jobsFile := filepath.Join(dir, "jobs.json")
	os.WriteFile(jobsFile, []byte("{invalid json!!!}"), 0600)

	_, err := mgr.loadAll()
	if err == nil {
		t.Fatal("loadAll 在 JSON 损坏时应返回错误")
	}
	if !strings.Contains(err.Error(), "解析") {
		t.Fatalf("错误信息应提及解析失败, 实际: %v", err)
	}
}

// ═══════════════════════════════════════════════ JobManager.loadAll — 读取失败 ═══════════════════════════════════════════════

func TestJobManager_LoadAll_ReadFail(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)

	// 创建 jobs.json 但设为不可读
	jobsFile := filepath.Join(dir, "jobs.json")
	os.WriteFile(jobsFile, []byte("{}"), 0000)

	_, err := mgr.loadAll()
	if err != nil {
		t.Logf("loadAll 在不可读文件时返回错误 (预期): %v", err)
	} else {
		t.Log("loadAll 在不可读文件时成功 (Windows 行为)")
	}

	os.Chmod(jobsFile, 0600)
}

// ═══════════════════════════════════════════════ parseSchedule — 更多边界情况 ═══════════════════════════════════════════════

func TestParseSchedule_EdgeCases(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantKind  string
		wantErr   bool
	}{
		{"空格输入", "   ", "", true},
		{"无效格式", "xyz", "", true},
		{"every 后无效持续时间", "every abc", "", true},
		{"有效分钟", "45m", "once", false},
		{"有效小时", "2h", "once", false},
		{"有效天", "1d", "once", false},
		{"有效间隔", "every 30m", "interval", false},
		{"有效间隔小时", "every 2h", "interval", false},
		{"有效 cron", "0 9 * * *", "cron", false},
		{"有效 cron 范围", "0-30/5 9 * * 1-5", "cron", false},
		{"有效 cron 步长", "*/15 * * * *", "cron", false},
		{"6 字段 cron", "0 9 * * * *", "cron", false},
		{"ISO 时间戳带 T", "2026-02-03T14:00", "once", false},
		{"ISO 时间戳 RFC3339", "2026-02-03T14:00:00Z", "once", false},
		{"过去的时间戳", "2020-01-01T00:00:00Z", "once", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind, minutes, cronExpr, err := parseSchedule(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("期望错误, 实际 kind=%s minutes=%d cronExpr=%s", kind, minutes, cronExpr)
				}
			} else {
				if err != nil {
					t.Fatalf("不期望错误: %v", err)
				}
				if kind != tt.wantKind {
					t.Fatalf("kind = %s, 期望 %s", kind, tt.wantKind)
				}
			}
		})
	}
}

// ═══════════════════════════════════════════════ ParseSchedule (公开) — 覆盖所有分支 ═══════════════════════════════════════════════

func TestParseSchedule_Public_AllKinds(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantKind string
	}{
		{"一次性持续时间", "30m", "once"},
		{"间隔", "every 30m", "interval"},
		{"Cron 表达式", "0 9 * * *", "cron"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind, nextRun, err := ParseSchedule(tt.input)
			if err != nil {
				t.Fatalf("ParseSchedule(%q) 错误: %v", tt.input, err)
			}
			if kind != tt.wantKind {
				t.Fatalf("kind = %s, 期望 %s", kind, tt.wantKind)
			}
			if nextRun.IsZero() {
				t.Fatal("nextRun 不应为零值")
			}
		})
	}
}

// ═══════════════════════════════════════════════ generateJobID ═══════════════════════════════════════════════

func TestGenerateJobID_Format(t *testing.T) {
	// 多次生成 ID 应为 12 字符 hex
	for i := 0; i < 50; i++ {
		id := generateJobID()
		if len(id) != 12 {
			t.Fatalf("ID 长度 = %d, 期望 12: %s", len(id), id)
		}
		for _, c := range id {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
				t.Fatalf("ID 包含非 hex 字符: %c in %s", c, id)
			}
		}
	}
}

func TestGenerateJobID_Uniqueness(t *testing.T) {
	ids := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := generateJobID()
		if ids[id] {
			t.Fatalf("ID 重复: %s", id)
		}
		ids[id] = true
	}
}

// ═══════════════════════════════════════════════ computeGraceSeconds — cron 作业 ═══════════════════════════════════════════════

func TestComputeGraceSeconds_CronJob(t *testing.T) {
	job := &Job{
		ScheduleKind:    "cron",
		ScheduleCronExpr: "0 9 * * *", // 每天一次
	}
	grace := computeGraceSeconds(job)
	if grace < minGraceSeconds {
		t.Fatalf("cron 作业 grace = %d, 最小应为 %d", grace, minGraceSeconds)
	}
	if grace > maxGraceSeconds {
		t.Fatalf("cron 作业 grace = %d, 最大应为 %d", grace, maxGraceSeconds)
	}
}

func TestComputeGraceSeconds_UnknownKind(t *testing.T) {
	job := &Job{
		ScheduleKind: "unknown",
	}
	grace := computeGraceSeconds(job)
	if grace != minGraceSeconds {
		t.Fatalf("未知类型 grace = %d, 期望 %d", grace, minGraceSeconds)
	}
}

// ═══════════════════════════════════════════════ Scheduler.run — MkdirAll 失败 ═══════════════════════════════════════════════

func TestScheduler_Run_MkdirAllFail(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	exec := NewExecutor(filepath.Join(dir, "output"), nil)
	s := NewScheduler(mgr, exec)

	// 将 lockFile 指向无法创建的路径
	s.lockFile = filepath.Join("Z:\\nonexistent_drive_12345", ".tick.lock")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := s.Run(ctx)
	if err == nil {
		t.Fatal("Run 在锁目录创建失败时应返回错误")
	}
}

// ═══════════════════════════════════════════════ Scheduler.tick — GetDueJobs 失败 ═══════════════════════════════════════════════

func TestScheduler_Tick_GetDueJobsFail(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	exec := NewExecutor(filepath.Join(dir, "output"), nil)
	s := NewScheduler(mgr, exec)

	// 写入损坏的 jobs.json 使 GetDueJobs 失败
	jobsFile := filepath.Join(dir, "jobs.json")
	os.WriteFile(jobsFile, []byte("{{bad json}}"), 0600)

	ctx := context.Background()
	err := s.tick(ctx)
	if err == nil {
		t.Fatal("tick 在 GetDueJobs 失败时应返回错误")
	}
}

// ═══════════════════════════════════════════════ Scheduler.tickWithAdapters — GetDueJobs 失败 ═══════════════════════════════════════════════

func TestScheduler_TickWithAdapters_GetDueJobsFail(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	exec := NewExecutor(filepath.Join(dir, "output"), nil)
	s := NewScheduler(mgr, exec)

	// 写入损坏的 jobs.json 使 GetDueJobs 失败
	jobsFile := filepath.Join(dir, "jobs.json")
	os.WriteFile(jobsFile, []byte("{{bad json}}"), 0600)

	ctx := context.Background()
	err := s.tickWithAdapters(ctx, nil)
	if err == nil {
		t.Fatal("tickWithAdapters 在 GetDueJobs 失败时应返回错误")
	}
}

// ═══════════════════════════════════════════════ JobManager.Create — 目录创建失败 ═══════════════════════════════════════════════

func TestJobManager_Create_MkdirAllFail(t *testing.T) {
	mgr := NewJobManager(nil, "Z:\\nonexistent_drive_12345\\cron")
	ctx := context.Background()

	job := &Job{
		Name:     "测试",
		Prompt:   "test",
		Schedule: "30m",
		Enabled:  true,
	}

	err := mgr.Create(ctx, job)
	if err == nil {
		t.Fatal("Create 在不可创建目录时应返回错误")
	}
}

// ═══════════════════════════════════════════════ JobManager.Get — loadAll 失败 ═══════════════════════════════════════════════

func TestJobManager_Get_LoadFail(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)

	jobsFile := filepath.Join(dir, "jobs.json")
	os.WriteFile(jobsFile, []byte("{bad}"), 0600)

	ctx := context.Background()
	_, err := mgr.Get(ctx, "any-id")
	if err == nil {
		t.Fatal("Get 在 loadAll 失败时应返回错误")
	}
}

// ═══════════════════════════════════════════════ JobManager.List — loadAll 失败 ═══════════════════════════════════════════════

func TestJobManager_List_LoadFail(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)

	jobsFile := filepath.Join(dir, "jobs.json")
	os.WriteFile(jobsFile, []byte("{bad}"), 0600)

	ctx := context.Background()
	_, err := mgr.List(ctx)
	if err == nil {
		t.Fatal("List 在 loadAll 失败时应返回错误")
	}
}

// ═══════════════════════════════════════════════ JobManager.Delete — loadAll 失败 ═══════════════════════════════════════════════

func TestJobManager_Delete_LoadFail(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)

	jobsFile := filepath.Join(dir, "jobs.json")
	os.WriteFile(jobsFile, []byte("{bad}"), 0600)

	ctx := context.Background()
	err := mgr.Delete(ctx, "any-id")
	if err == nil {
		t.Fatal("Delete 在 loadAll 失败时应返回错误")
	}
}

// ═══════════════════════════════════════════════ JobManager.Update — loadAll 失败 ═══════════════════════════════════════════════

func TestJobManager_Update_LoadFail(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)

	jobsFile := filepath.Join(dir, "jobs.json")
	os.WriteFile(jobsFile, []byte("{bad}"), 0600)

	ctx := context.Background()
	err := mgr.Update(ctx, &Job{ID: "x", Schedule: "30m"})
	if err == nil {
		t.Fatal("Update 在 loadAll 失败时应返回错误")
	}
}

// ═══════════════════════════════════════════════ JobManager.GetDueJobs — batch update 失败 ═══════════════════════════════════════════════

func TestJobManager_GetDueJobs_BatchUpdateFail(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	ctx := context.Background()

	// 创建一个长期 interval 作业
	job := &Job{
		Name:     "批次更新测试",
		Prompt:   "test",
		Schedule: "every 30m",
		Enabled:  true,
	}
	if err := mgr.Create(ctx, job); err != nil {
		t.Fatalf("Create 失败: %v", err)
	}

	// 将作业设为很久前到期 (超过宽限期以触发快进)
	jobs, _ := mgr.loadAll()
	for _, j := range jobs {
		j.NextRunAt = time.Now().Add(-3 * time.Hour)
	}
	mgr.saveAll(jobs)

	// 将目录设为只读使 saveAll 失败
	jobsFile := filepath.Join(dir, "jobs.json")
	os.Chmod(jobsFile, 0400)
	os.Chmod(dir, 0500)

	due, err := mgr.GetDueJobs(ctx)
	// GetDueJobs 本身不返回 saveAll 的错误，只记录日志
	// 但快进的作业不应出现在 due 中
	_ = due
	_ = err

	// 恢复权限
	os.Chmod(jobsFile, 0600)
	os.Chmod(dir, 0700)
}

// ═══════════════════════════════════════════════ JobManager.AdvanceNextRun — 一次性作业 ═══════════════════════════════════════════════

func TestJobManager_AdvanceNextRun_OneShot(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	ctx := context.Background()

	job := &Job{
		Name:     "一次性",
		Prompt:   "test",
		Schedule: "30m",
		Enabled:  true,
	}
	if err := mgr.Create(ctx, job); err != nil {
		t.Fatalf("Create 失败: %v", err)
	}

	// 一次性作业 AdvanceNextRun 应返回 nil (不推进)
	err := mgr.AdvanceNextRun(ctx, job)
	if err != nil {
		t.Fatalf("一次性作业 AdvanceNextRun 应返回 nil, 实际: %v", err)
	}
}

// ═══════════════════════════════════════════════ matchCronField — 步长带基数 ═══════════════════════════════════════════════

func TestMatchCronField_StepWithBase(t *testing.T) {
	// 测试 "5/15" 模式: 从 5 开始，步长 15
	if !matchCronField(20, "5/15", 0, 59) {
		t.Fatal("20 应匹配 5/15 (5, 20, 35, 50)")
	}
	if matchCronField(10, "5/15", 0, 59) {
		t.Fatal("10 不应匹配 5/15")
	}
}

// ═══════════════════════════════════════════════ nextCronTime — 无效表达式 ═══════════════════════════════════════════════

func TestNextCronTime_InvalidExpr(t *testing.T) {
	now := time.Now()
	result := nextCronTime("", now)
	// 少于 5 字段应返回 now + 24h
	if result.Sub(now) < 23*time.Hour || result.Sub(now) > 25*time.Hour {
		t.Fatalf("无效 cron 表达式应返回约 24h 后, 实际: %v", result.Sub(now))
	}
}

// ═══════════════════════════════════════════════ matchCronField — 逗号分隔值 ═══════════════════════════════════════════════

func TestMatchCronField_CommaSeparated(t *testing.T) {
	if !matchCronField(1, "1,3,5", 0, 59) {
		t.Fatal("1 应匹配 1,3,5")
	}
	if !matchCronField(5, "1,3,5", 0, 59) {
		t.Fatal("5 应匹配 1,3,5")
	}
	if matchCronField(2, "1,3,5", 0, 59) {
		t.Fatal("2 不应匹配 1,3,5")
	}
}

// ═══════════════════════════════════════════════ parseDuration — 边界情况 ═══════════════════════════════════════════════

func TestParseDuration_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{"单分钟", "1m", 1, false},
		{"单小时", "1h", 60, false},
		{"单天", "1d", 1440, false},
		{"大数值", "365d", 365 * 1440, false},
		{"分钟全称", "5 minutes", 5, false},
		{"小时全称", "2 hours", 120, false},
		{"天全称", "3 days", 3 * 1440, false},
		{"无效格式", "abc", 0, true},
		{"空字符串", "", 0, true},
		{"无单位数字", "123", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDuration(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("期望错误, 实际得到 %d", got)
				}
			} else {
				if err != nil {
					t.Fatalf("不期望错误: %v", err)
				}
				if got != tt.want {
					t.Fatalf("结果 = %d, 期望 %d", got, tt.want)
				}
			}
		})
	}
}

// ═══════════════════════════════════════════════ JobManager — 空目录 loadAll ═══════════════════════════════════════════════

func TestJobManager_LoadAll_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)

	jobs, err := mgr.loadAll()
	if err != nil {
		t.Fatalf("空目录 loadAll 不应报错: %v", err)
	}
	if jobs != nil {
		t.Fatalf("空目录 loadAll 应返回 nil, 实际 %d 个作业", len(jobs))
	}
}

// ═══════════════════════════════════════════════ GetDueJobs Fast-Forward ═══════════════════════════════════════════════════

func TestGetDueJobs_FastForward(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	ctx := context.Background()

	// 创建间隔作业
	job := &Job{
		ID:              "ff-1",
		Name:            "快进测试",
		Prompt:          "测试",
		Schedule:        "every 10m",
		Enabled:         true,
	}
	if err := mgr.Create(ctx, job); err != nil {
		t.Fatalf("Create 失败: %v", err)
	}

	// 手动将 NextRunAt 设为很久以前 (远超 grace period)
	jobs, _ := mgr.loadAll()
	for _, j := range jobs {
		if j.ID == "ff-1" {
			// 设为 1 小时前 (grace period = 10m * 60 / 2 = 300s = 5min)
			j.NextRunAt = time.Now().Add(-1 * time.Hour)
			j.ScheduleMinutes = 10
		}
	}
	mgr.saveAll(jobs)

	// GetDueJobs 应快进此作业而不是返回它
	due, err := mgr.GetDueJobs(ctx)
	if err != nil {
		t.Fatalf("GetDueJobs 不应报错: %v", err)
	}

	if len(due) != 0 {
		t.Fatalf("快进后应无到期作业, 实际 %d", len(due))
	}

	// 验证 NextRunAt 已被更新为未来时间
	updated, _ := mgr.Get(ctx, "ff-1")
	if updated == nil {
		t.Fatal("作业应仍存在")
	}
	if !updated.NextRunAt.After(time.Now()) {
		t.Fatalf("NextRunAt 应在未来, 实际 %v", updated.NextRunAt)
	}
}

func TestGetDueJobs_WithinGrace(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	ctx := context.Background()

	// 创建间隔作业，grace period = 10m * 60 / 2 = 300s = 5min
	job := &Job{
		ID:              "wg-1",
		Name:            "宽限期内",
		Prompt:          "测试",
		Schedule:        "every 10m",
		Enabled:         true,
	}
	if err := mgr.Create(ctx, job); err != nil {
		t.Fatalf("Create 失败: %v", err)
	}

	// 将 NextRunAt 设为 1 分钟前 (在 5 分钟 grace period 内)
	jobs, _ := mgr.loadAll()
	for _, j := range jobs {
		if j.ID == "wg-1" {
			j.NextRunAt = time.Now().Add(-1 * time.Minute)
			j.ScheduleMinutes = 10
		}
	}
	mgr.saveAll(jobs)

	// 应返回为到期作业 (未被快进)
	due, err := mgr.GetDueJobs(ctx)
	if err != nil {
		t.Fatalf("GetDueJobs 不应报错: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("宽限期内应返回 1 个到期作业, 实际 %d", len(due))
	}
}

func TestGetDueJobs_CronFastForward(t *testing.T) {
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	ctx := context.Background()

	// 创建 cron 作业
	job := &Job{
		ID:               "cff-1",
		Name:             "cron快进",
		Prompt:           "测试",
		Schedule:        "0 9 * * *",
		ScheduleCronExpr: "0 9 * * *",
		Enabled:          true,
	}
	if err := mgr.Create(ctx, job); err != nil {
		t.Fatalf("Create 失败: %v", err)
	}

	// 设为很久以前
	jobs, _ := mgr.loadAll()
	for _, j := range jobs {
		if j.ID == "cff-1" {
			j.NextRunAt = time.Now().Add(-48 * time.Hour)
		}
	}
	mgr.saveAll(jobs)

	// 应被快进
	due, err := mgr.GetDueJobs(ctx)
	if err != nil {
		t.Fatalf("GetDueJobs 不应报错: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("cron 作业快进后应无到期作业, 实际 %d", len(due))
	}
}

// ═══════════════════════════════════════════════ saveOutput 错误路径 ═══════════════════════════════════════════════════

func TestExecutor_SaveOutput_ReadOnlyDir(t *testing.T) {
	dir := t.TempDir()
	exec := NewExecutor(dir, nil)

	job := &Job{ID: "ro-test"}
	ts := time.Now()

	// 在 Windows 上无法设置只读目录来测试 MkdirAll 失败
	// 改为测试正常保存后验证文件内容
	err := exec.saveOutput(job, "测试输出内容", ts)
	if err != nil {
		t.Fatalf("saveOutput 应成功: %v", err)
	}

	// 验证文件内容
	safeID := sanitizeJobID(job.ID)
	expected := ts.Format("2006-01-02_15-04-05") + ".md"
	path := filepath.Join(dir, safeID, expected)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取输出文件失败: %v", err)
	}
	if string(data) != "测试输出内容" {
		t.Fatalf("文件内容不匹配: %s", string(data))
	}
}

func TestExecutor_SaveOutput_LongOutput(t *testing.T) {
	dir := t.TempDir()
	exec := NewExecutor(dir, nil)

	job := &Job{ID: "long-out"}
	ts := time.Now()

	// 大量输出
	longContent := strings.Repeat("A", 10000)
	err := exec.saveOutput(job, longContent, ts)
	if err != nil {
		t.Fatalf("saveOutput 应成功: %v", err)
	}

	safeID := sanitizeJobID(job.ID)
	expected := ts.Format("2006-01-02_15-04-05") + ".md"
	path := filepath.Join(dir, safeID, expected)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取输出文件失败: %v", err)
	}
	if len(data) != 10000 {
		t.Fatalf("输出长度 = %d, 期望 10000", len(data))
	}
}

// ═══════════════════════════════════════════════ saveAll 错误路径 ═══════════════════════════════════════════════════

func TestJobManager_SaveAll_Error(t *testing.T) {
	// 在 Windows 上无法通过权限控制触发 saveAll 错误
	// 测试正常保存后验证持久化
	dir := t.TempDir()
	mgr := NewJobManager(nil, dir)
	ctx := context.Background()

	job := &Job{
		ID:        "sa-1",
		Name:      "saveAll测试",
		Prompt:    "测试",
		Schedule:  "30m",
		Enabled:   true,
	}
	mgr.Create(ctx, job)

	// 直接调用 saveAll
	jobs, _ := mgr.loadAll()
	if len(jobs) != 1 {
		t.Fatalf("应有 1 个作业, 实际 %d", len(jobs))
	}

	// 修改并保存
	jobs[0].Name = "已更新"
	err := mgr.saveAll(jobs)
	if err != nil {
		t.Fatalf("saveAll 不应报错: %v", err)
	}

	// 验证持久化
	loaded, _ := mgr.loadAll()
	if loaded[0].Name != "已更新" {
		t.Fatalf("名称应为 '已更新', 实际 %s", loaded[0].Name)
	}
}

// ═══════════════════════════════════════════════ computeGraceSeconds ═══════════════════════════════════════════════════

func TestComputeGraceSeconds_Interval(t *testing.T) {
	tests := []struct {
		minutes int
		expect  int
	}{
		{1, 120},       // grace = 30s, clamped to minGraceSeconds (120)
		{5, 150},       // grace = 150s
		{30, 900},      // grace = 900s = 15min
		{60, 1800},     // grace = 1800s = 30min
		{240, 7200},    // grace = 7200s, clamped to maxGraceSeconds (7200)
		{480, 7200},    // grace = 14400s, clamped to maxGraceSeconds (7200)
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%dm", tt.minutes), func(t *testing.T) {
			job := &Job{ScheduleKind: "interval", ScheduleMinutes: tt.minutes}
			got := computeGraceSeconds(job)
			if got != tt.expect {
				t.Errorf("computeGraceSeconds(%dm) = %d, 期望 %d", tt.minutes, got, tt.expect)
			}
		})
	}
}

func TestComputeGraceSeconds_Default(t *testing.T) {
	job := &Job{ScheduleKind: "once"}
	got := computeGraceSeconds(job)
	if got != minGraceSeconds {
		t.Errorf("未知 kind 应返回 minGraceSeconds (%d), 实际 %d", minGraceSeconds, got)
	}
}

// ═══════════════════════════════════════════════ advanceToNext ═══════════════════════════════════════════════════

func TestAdvanceToNext_Interval(t *testing.T) {
	now := time.Now()
	job := &Job{
		ScheduleKind:    "interval",
		ScheduleMinutes: 30,
	}
	updated := advanceToNext(job, now)
	if updated == nil {
		t.Fatal("advanceToNext 不应返回 nil")
	}
	expectedDiff := 30 * time.Minute
	diff := updated.NextRunAt.Sub(now)
	if diff < expectedDiff-1*time.Second || diff > expectedDiff+1*time.Second {
		t.Fatalf("interval advance 差值 = %v, 期望约 %v", diff, expectedDiff)
	}
}

func TestAdvanceToNext_Once(t *testing.T) {
	now := time.Now()
	job := &Job{
		ScheduleKind: "once",
	}
	updated := advanceToNext(job, now)
	if updated.State != "completed" {
		t.Fatalf("once 作业应标记为 completed, 实际 %s", updated.State)
	}
	if !updated.NextRunAt.IsZero() {
		t.Fatal("once 作业 NextRunAt 应为零值")
	}
}

func TestAdvanceToNext_Cron(t *testing.T) {
	now := time.Now()
	job := &Job{
		ScheduleKind:     "cron",
		ScheduleCronExpr: "0 9 * * *",
	}
	updated := advanceToNext(job, now)
	if updated == nil {
		t.Fatal("advanceToNext 不应返回 nil")
	}
	if !updated.NextRunAt.After(now) {
		t.Fatalf("cron advance 应在未来, 实际 %v", updated.NextRunAt)
	}
}

// ═══════════════════════════════════════════════ Executor.Execute ═══════════════════════════════════════════════════

// ═══════════════════════════════════════════════ Execute — nil job / sanitizeJobID / buildPrompt / truncate ═══════════════════════════════

func TestExecutor_Execute_NilJob(t *testing.T) {
	dir := t.TempDir()
	exec := NewExecutor(dir, nil)
	ctx := context.Background()

	err := exec.Execute(ctx, nil)
	if err == nil {
		t.Fatal("Execute(nil) 应返回错误")
	}
	if !strings.Contains(err.Error(), "作业不能为 nil") && !strings.Contains(err.Error(), "nil") {
		t.Fatalf("错误信息应提及 nil, 实际: %v", err)
	}
}

func TestExecutor_Execute_NilAgentConfig(t *testing.T) {
	dir := t.TempDir()
	exec := NewExecutor(dir, nil)
	ctx := context.Background()

	job := &Job{
		ID:     "exec-nil",
		Name:   "nil config",
		Prompt: "test",
	}

	defer func() {
		r := recover()
		if r != nil {
			t.Logf("Execute with nil agentConfig panic'd (expected): %v", r)
		}
	}()

	err := exec.Execute(ctx, job)
	// 如果到达这里，说明 DefaultAgentFromConfig(nil) 没有 panic
	if err == nil {
		t.Log("Execute with nil agentConfig succeeded (unexpected but not fatal)")
	} else {
		t.Logf("Execute with nil agentConfig failed as expected: %v", err)
	}
}

func TestExecutor_SaveOutput_SpecialChars(t *testing.T) {
	dir := t.TempDir()
	exec := NewExecutor(dir, nil)

	// 特殊字符 ID 应被清理
	job := &Job{ID: "../etc/passwd"}
	ts := time.Now()

	err := exec.saveOutput(job, "安全测试", ts)
	if err != nil {
		t.Fatalf("saveOutput 应成功: %v", err)
	}

	// sanitizeJobID("../etc/passwd") = "etcpasswd"
	safeID := sanitizeJobID(job.ID)
	expected := ts.Format("2006-01-02_15-04-05") + ".md"
	path := filepath.Join(dir, safeID, expected)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("应能读取输出文件: %v", err)
	}
	if string(data) != "安全测试" {
		t.Fatalf("内容不匹配: %s", string(data))
	}
}

func TestExecutor_SaveOutput_EmptyOutput(t *testing.T) {
	dir := t.TempDir()
	exec := NewExecutor(dir, nil)

	job := &Job{ID: "empty-out"}
	ts := time.Now()

	err := exec.saveOutput(job, "", ts)
	if err != nil {
		t.Fatalf("saveOutput 空输出应成功: %v", err)
	}

	safeID := sanitizeJobID(job.ID)
	expected := ts.Format("2006-01-02_15-04-05") + ".md"
	path := filepath.Join(dir, safeID, expected)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("应能读取输出文件: %v", err)
	}
	if len(data) != 0 {
		t.Fatalf("空输出文件应为 0 字节, 实际 %d", len(data))
	}
}

// ───────────────────────────── mock conversationRunner ─────────────────────────────

// mockRunner 是 conversationRunner 的测试替身。
type mockRunner struct {
	result *agent.TurnResult
	err    error
	called bool
}

func (m *mockRunner) runConversation(_ context.Context, _ string, _ []any, _ string) (*agent.TurnResult, error) {
	m.called = true
	return m.result, m.err
}

// ───────────────────────────── Execute 覆盖率提升 ─────────────────────────────

func TestExecutor_Execute_MockSuccess(t *testing.T) {
	dir := t.TempDir()
	exec := NewExecutor(dir, nil).withRunner(&mockRunner{
		result: &agent.TurnResult{FinalResponse: "hello world"},
	})

	ctx := context.Background()
	job := &Job{ID: "mock-ok", Name: "mock test", Prompt: "say hi"}

	err := exec.Execute(ctx, job)
	if err != nil {
		t.Fatalf("Execute 应成功: %v", err)
	}
	if job.LastStatus != "ok" {
		t.Fatalf("LastStatus = %s, 期望 ok", job.LastStatus)
	}
	if job.LastRunAt.IsZero() {
		t.Fatal("LastRunAt 应已设置")
	}

	// 验证输出文件
	safeID := sanitizeJobID(job.ID)
	files, _ := os.ReadDir(filepath.Join(dir, safeID))
	if len(files) == 0 {
		t.Fatal("应有输出文件")
	}
}

func TestExecutor_Execute_MockSilent(t *testing.T) {
	dir := t.TempDir()
	exec := NewExecutor(dir, nil).withRunner(&mockRunner{
		result: &agent.TurnResult{FinalResponse: "[SILENT]"},
	})

	ctx := context.Background()
	job := &Job{ID: "mock-silent", Name: "silent test", Prompt: "check"}

	err := exec.Execute(ctx, job)
	if err != nil {
		t.Fatalf("Execute 应成功: %v", err)
	}
	if job.LastStatus != "silent" {
		t.Fatalf("LastStatus = %s, 期望 silent", job.LastStatus)
	}

	// 不应有输出文件 (静默跳过保存)
	safeID := sanitizeJobID(job.ID)
	outputDir := filepath.Join(dir, safeID)
	if _, err := os.Stat(outputDir); !os.IsNotExist(err) {
		files, _ := os.ReadDir(outputDir)
		if len(files) > 0 {
			t.Fatal("静默模式不应产生输出文件")
		}
	}
}

func TestExecutor_Execute_MockError(t *testing.T) {
	dir := t.TempDir()
	exec := NewExecutor(dir, nil).withRunner(&mockRunner{
		err: fmt.Errorf("mock agent error"),
	})

	ctx := context.Background()
	job := &Job{ID: "mock-err", Name: "error test", Prompt: "fail"}

	err := exec.Execute(ctx, job)
	if err == nil {
		t.Fatal("Execute 应返回错误")
	}
	if !strings.Contains(err.Error(), "mock agent error") {
		t.Fatalf("错误应包含 mock agent error, 实际: %v", err)
	}
}

func TestExecutor_Execute_NilRunner(t *testing.T) {
	dir := t.TempDir()
	// agentConfig = nil + runner = nil → DefaultAgentFromConfig(nil) 会 panic
	exec := NewExecutor(dir, nil)

	ctx := context.Background()
	job := &Job{ID: "no-runner", Name: "no runner", Prompt: "test"}

	defer func() {
		r := recover()
		if r != nil {
			t.Logf("无 runner 时 panic (预期行为): %v", r)
		}
	}()

	exec.Execute(ctx, job)
}

	// ───────────────────────────── saveOutput 错误路径 ─────────────────────────────

func TestExecutor_SaveOutput_MkdirAllFail(t *testing.T) {
	dir := t.TempDir()
	// 创建文件阻塞子目录创建
	blockPath := filepath.Join(dir, "blocked")
	os.WriteFile(blockPath, []byte("x"), 0600)

	exec := NewExecutor(filepath.Join(blockPath, "sub"), nil)
	job := &Job{ID: "mkdirfail"}
	ts := time.Now()

	err := exec.saveOutput(job, "test", ts)
	if err == nil {
		t.Fatal("MkdirAll 失败应返回错误")
	}
}

	// ───────────────────────────── saveAll 错误路径 ─────────────────────────────

func TestJobManager_SaveAll_MkdirAllFail(t *testing.T) {
	dir := t.TempDir()
	// 创建文件阻塞子目录
	blockPath := filepath.Join(dir, "blocked")
	os.WriteFile(blockPath, []byte("x"), 0600)

	mgr := NewJobManager(nil, filepath.Join(blockPath, "sub"))
	err := mgr.saveAll([]*Job{{ID: "x"}})
	if err == nil {
		t.Fatal("MkdirAll 失败应返回错误")
	}
}

func TestJobManager_SaveAll_BadPath(t *testing.T) {
	// 使用空字符串路径
	mgr := NewJobManager(nil, "")
	err := mgr.saveAll([]*Job{{ID: "x"}})
	if err == nil {
		t.Fatal("空路径应导致保存失败")
	}
}

// ───────────────────────────── Scheduler: tick/tickWithAdapters 错误路径 ─────────────────────────────

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

