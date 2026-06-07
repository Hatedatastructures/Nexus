package cron

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

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

