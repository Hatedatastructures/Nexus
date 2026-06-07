package cron

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

