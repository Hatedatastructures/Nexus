package cron

import (
	"context"
	"testing"
	"time"
)

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

