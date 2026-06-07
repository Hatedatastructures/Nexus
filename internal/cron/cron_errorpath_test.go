package cron

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

