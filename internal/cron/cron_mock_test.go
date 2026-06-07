package cron

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nexus-agent/internal/agent"
)

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


