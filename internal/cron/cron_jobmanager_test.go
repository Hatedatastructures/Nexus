package cron

import (
	"context"
	"testing"
	"time"
)

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

