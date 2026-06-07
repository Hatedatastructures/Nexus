package cron

import (
	"fmt"
	"strings"
	"testing"
	"time"
)


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
