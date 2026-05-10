// Package agent 工具调用护栏的单元测试。
// 覆盖精确重复检测、工具固着检测、重置行为和不同参数区分。
package agent

import (
	"testing"
)

// ───────────────────────────── 精确重复检测测试 ─────────────────────────────

// TestGuardrailsExactDuplicate 验证连续 3 次相同调用应被阻止。
func TestGuardrailsExactDuplicate(t *testing.T) {
	g := NewToolCallGuardrails()
	// 降低阈值便于测试
	g.WithMaxConsecutiveDuplicates(3)

	args := map[string]any{"path": "/tmp/file.txt"}

	// 前 2 次调用应被允许（Record 后连续计数为 1 和 2）
	for i := 0; i < 2; i++ {
		allowed, reason := g.Check("write_file", args)
		if !allowed {
			t.Fatalf("第 %d 次调用应被允许, 但被拒绝: %s", i+1, reason)
		}
		g.Record("write_file", args)
	}

	// 第 3 次调用 — 连续计数达到阈值，应被阻止
	allowed, reason := g.Check("write_file", args)
	if allowed {
		t.Error("第 3 次相同调用应被阻止, 但被允许了")
	}
	if reason == "" {
		t.Error("拒绝原因不应为空")
	}
}

// ───────────────────────────── 工具固着检测测试 ─────────────────────────────

// TestGuardrailsToolFixation 验证滑动窗口内同一工具调用 10 次应被阻止。
func TestGuardrailsToolFixation(t *testing.T) {
	g := NewToolCallGuardrails()
	g.WithMaxToolCallsInWindow(10)
	g.WithWindowSize(20)

	// 用不同参数调用同一工具 10 次 (不触发精确重复)
	for i := 0; i < 10; i++ {
		args := map[string]any{"query": "test_query_" + itoa(i)}
		allowed, reason := g.Check("web_search", args)
		if !allowed {
			t.Fatalf("第 %d 次调用应被允许, 但被拒绝: %s", i+1, reason)
		}
		g.Record("web_search", args)
	}

	// 第 11 次调用 — 窗口内同工具调用次数达到阈值，应被阻止
	args := map[string]any{"query": "another_query"}
	allowed, reason := g.Check("web_search", args)
	if allowed {
		t.Error("工具固着检测: 第 11 次调用应被阻止, 但被允许了")
	}
	if reason == "" {
		t.Error("拒绝原因不应为空")
	}
}

// ───────────────────────────── 重置行为测试 ─────────────────────────────

// TestGuardrailsReset 验证重置后所有计数器清零，调用可重新开始。
func TestGuardrailsReset(t *testing.T) {
	g := NewToolCallGuardrails()
	g.WithMaxConsecutiveDuplicates(3)

	args := map[string]any{"path": "/tmp/file.txt"}

	// 先触发精确重复阻止
	for i := 0; i < 3; i++ {
		g.Record("write_file", args)
	}
	allowed, _ := g.Check("write_file", args)
	if allowed {
		t.Fatal("重置前第 4 次调用应被阻止")
	}

	// 重置护栏
	g.Reset()

	// 重置后应重新允许
	allowed, reason := g.Check("write_file", args)
	if !allowed {
		t.Errorf("重置后调用应被允许, 但被拒绝: %s", reason)
	}
}

// ───────────────────────────── 不同参数区分测试 ─────────────────────────────

// TestGuardrailsDifferentArgs 验证相同工具但不同参数不应被计为重复。
func TestGuardrailsDifferentArgs(t *testing.T) {
	g := NewToolCallGuardrails()
	g.WithMaxConsecutiveDuplicates(3)

	// 用相同工具但不同参数调用
	for i := 0; i < 5; i++ {
		args := map[string]any{"path": "/tmp/file_" + itoa(i) + ".txt"}
		g.Record("write_file", args)
	}

	// 不同参数的调用不应触发精确重复检测
	args := map[string]any{"path": "/tmp/file_new.txt"}
	allowed, reason := g.Check("write_file", args)
	if !allowed {
		t.Errorf("不同参数的调用不应被阻止, 但被拒绝: %s", reason)
	}
}

// ───────────────────────────── 配置方法测试 ─────────────────────────────

// TestGuardrailsConfiguration 验证配置方法的边界值处理。
func TestGuardrailsConfiguration(t *testing.T) {
	g := NewToolCallGuardrails()

	// 零值或负值不应修改配置
	g.WithMaxConsecutiveDuplicates(0)
	g.WithMaxToolCallsInWindow(-1)
	g.WithWindowSize(0)

	// 验证默认值未被修改: 连续 3 次应被阻止
	args := map[string]any{"key": "value"}
	for i := 0; i < 3; i++ {
		g.Record("tool", args)
	}
	allowed, _ := g.Check("tool", args)
	if allowed {
		t.Error("零值配置后默认阈值应仍为 3")
	}
}

// ───────────────────────────── nil 参数处理测试 ─────────────────────────────

// TestGuardrailsNilArgs 验证 nil 参数的处理。
func TestGuardrailsNilArgs(t *testing.T) {
	g := NewToolCallGuardrails()

	// nil 参数应被序列化为 "{}"
	g.Record("tool", nil)

	allowed, _ := g.Check("tool", nil)
	if !allowed {
		t.Error("nil 参数的首次调用应被允许")
	}
}

// ───────────────────────────── 窗口大小维护测试 ─────────────────────────────

// TestGuardrailsWindowSize 验证滑动窗口大小限制。
func TestGuardrailsWindowSize(t *testing.T) {
	g := NewToolCallGuardrails()
	g.WithWindowSize(5)
	g.WithMaxToolCallsInWindow(3)

	// 调用不同工具填满窗口
	for i := 0; i < 5; i++ {
		args := map[string]any{"idx": itoa(i)}
		g.Record("other_tool", args)
	}

	// 在窗口中调用目标工具 3 次
	for i := 0; i < 3; i++ {
		args := map[string]any{"idx": itoa(i)}
		allowed, _ := g.Check("target_tool", args)
		if !allowed {
			t.Fatalf("第 %d 次调用应被允许", i+1)
		}
		g.Record("target_tool", args)
	}

	// 窗口已滑出旧记录，目标工具在窗口内出现 3 次
	// 再调用一次应被阻止
	allowed, _ := g.Check("target_tool", map[string]any{"new": "data"})
	if allowed {
		t.Error("窗口内第 4 次调用应被阻止")
	}
}
