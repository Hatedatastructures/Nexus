// Package agent 提供工具调用安全护栏。
// ToolCallGuardrails 通过滑动窗口检测重复调用和工具固着行为，
// 防止 LLM 陷入无限循环或重复执行相同操作。
package agent

import (
	"bytes"
	"encoding/json"
	"sort"
	"sync"
	"time"
)

// ───────────────────────────── 数据类型 ─────────────────────────────

// ToolCallRecord 记录单次工具调用的元数据。
// 用于在滑动窗口中追踪调用历史。
type ToolCallRecord struct {
	ToolName  string    // 工具名称
	ArgsJSON  string    // 参数的 JSON 序列化字符串 (用于精确比较)
	Timestamp time.Time // 调用时间戳
}

// ───────────────────────────── 护栏配置 ─────────────────────────────

// ToolCallGuardrails 实施工具调用安全策略。
// 检测两种异常模式:
//   - 精确重复: 相同工具 + 相同参数连续调用 N 次
//   - 工具固着: 同一工具在滑动窗口内被调用 M 次
type ToolCallGuardrails struct {
	mu sync.Mutex

	// ── 调用历史 ──
	history []ToolCallRecord // 滑动窗口历史记录

	// ── 连续重复检测 ──
	consecutiveDuplicates int    // 当前连续重复计数
	lastToolName          string // 上一次调用的工具名称
	lastArgsJSON          string // 上一次调用的参数 JSON

	// ── 阈值配置 ──
	maxConsecutiveDuplicates int // 精确重复阈值 (默认 3)
	maxToolCallsInWindow     int // 滑动窗口内同工具最大调用次数 (默认 10)
	windowSize               int // 滑动窗口大小 (默认 20)
}

// NewToolCallGuardrails 创建工具调用护栏实例。
// 使用默认阈值: 精确重复 3 次, 窗口内同工具 10 次, 窗口大小 20。
func NewToolCallGuardrails() *ToolCallGuardrails {
	return &ToolCallGuardrails{
		history:                  make([]ToolCallRecord, 0, 20),
		maxConsecutiveDuplicates: 3,
		maxToolCallsInWindow:     10,
		windowSize:               20,
	}
}

// ───────────────────────────── 核心方法 ─────────────────────────────

// Check 检查新的工具调用是否被允许，同时更新内部状态。
// 返回 allowed=true 表示允许执行, allowed=false 表示应被拦截。
// reason 在拦截时说明原因。
//
// 注意: 此方法会修改护栏状态（连续重复计数、历史记录等），
// 以防止同一批次中多个相同调用绕过检查。
//
// 检测策略:
//  1. 精确重复: 相同 toolName + 相同 args 连续出现 N 次
//  2. 工具固着: 同一 toolName 在滑动窗口内出现 M 次
func (g *ToolCallGuardrails) Check(toolName string, args map[string]any) (allowed bool, reason string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	argsJSON := serializeArgs(args)

	// ── 检测 1: 精确重复 ──
	if toolName == g.lastToolName && argsJSON == g.lastArgsJSON {
		if g.consecutiveDuplicates+1 >= g.maxConsecutiveDuplicates {
			return false, "检测到精确重复调用: 工具 " + toolName +
				" 以相同参数连续调用了 " + itoa(g.maxConsecutiveDuplicates) + " 次，可能存在循环"
		}
	}

	// ── 检测 2: 工具固着 ──
	count := 0
	for _, record := range g.history {
		if record.ToolName == toolName {
			count++
		}
	}
	if count >= g.maxToolCallsInWindow {
		return false, "检测到工具固着: 工具 " + toolName +
			" 在最近 " + itoa(g.windowSize) + " 次调用中出现了 " + itoa(count) + " 次"
	}

	// 关键修复: Check 通过后立即更新状态，防止同一批次中
	// 多个相同调用都绕过护栏（原实现 Check 不修改状态，
	// 导致批量重复调用全部通过检查后才被 Record 记录）。
	if toolName == g.lastToolName && argsJSON == g.lastArgsJSON {
		g.consecutiveDuplicates++
	} else {
		g.consecutiveDuplicates = 1
	}
	g.lastToolName = toolName
	g.lastArgsJSON = argsJSON

	// 追加到历史记录，维护滑动窗口
	g.history = append(g.history, ToolCallRecord{
		ToolName:  toolName,
		ArgsJSON:  argsJSON,
		Timestamp: time.Now(),
	})
	if len(g.history) > g.windowSize {
		g.history = g.history[len(g.history)-g.windowSize:]
	}

	return true, ""
}

// Record 记录一次工具调用到历史窗口。
// 应在工具调用执行成功后调用，用于更新滑动窗口状态。
func (g *ToolCallGuardrails) Record(toolName string, args map[string]any) {
	g.mu.Lock()
	defer g.mu.Unlock()

	argsJSON := serializeArgs(args)

	// 更新连续重复计数
	if toolName == g.lastToolName && argsJSON == g.lastArgsJSON {
		g.consecutiveDuplicates++
	} else {
		g.consecutiveDuplicates = 1
	}

	g.lastToolName = toolName
	g.lastArgsJSON = argsJSON

	// 追加到历史记录
	record := ToolCallRecord{
		ToolName:  toolName,
		ArgsJSON:  argsJSON,
		Timestamp: time.Now(),
	}
	g.history = append(g.history, record)

	// 维护滑动窗口大小
	if len(g.history) > g.windowSize {
		g.history = g.history[len(g.history)-g.windowSize:]
	}
}

// Reset 重置所有护栏状态。
// 应在每轮新用户消息到达时调用，防止跨轮次的误报。
func (g *ToolCallGuardrails) Reset() {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.history = g.history[:0]
	g.consecutiveDuplicates = 0
	g.lastToolName = ""
	g.lastArgsJSON = ""
}

// ───────────────────────────── 配置方法 ─────────────────────────────

// WithMaxConsecutiveDuplicates 设置精确重复检测阈值。
func (g *ToolCallGuardrails) WithMaxConsecutiveDuplicates(n int) *ToolCallGuardrails {
	g.mu.Lock()
	defer g.mu.Unlock()
	if n > 0 {
		g.maxConsecutiveDuplicates = n
	}
	return g
}

// WithMaxToolCallsInWindow 设置滑动窗口内同工具最大调用次数。
func (g *ToolCallGuardrails) WithMaxToolCallsInWindow(n int) *ToolCallGuardrails {
	g.mu.Lock()
	defer g.mu.Unlock()
	if n > 0 {
		g.maxToolCallsInWindow = n
	}
	return g
}

// WithWindowSize 设置滑动窗口大小。
func (g *ToolCallGuardrails) WithWindowSize(n int) *ToolCallGuardrails {
	g.mu.Lock()
	defer g.mu.Unlock()
	if n > 0 {
		g.windowSize = n
		// 如果历史记录超出新窗口大小，截断
		if len(g.history) > n {
			g.history = g.history[len(g.history)-n:]
		}
	}
	return g
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

// serializeArgs 将参数 map 序列化为稳定的 JSON 字符串。
// 使用有序序列化确保相同参数总是产生相同的字符串。
func serializeArgs(args map[string]any) string {
	if args == nil {
		return "{}"
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, _ := json.Marshal(k)
		val, _ := json.Marshal(args[k])
		buf.Write(key)
		buf.WriteByte(':')
		buf.Write(val)
	}
	buf.WriteByte('}')
	return buf.String()
}

// itoa 简单的整数转字符串 (避免引入 strconv 包)。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	if n < 0 {
		return "-" + itoa(-n)
	}
	digits := make([]byte, 0, 10)
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
