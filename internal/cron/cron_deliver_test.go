package cron

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nexus-agent/internal/gateway/platforms"
)

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

