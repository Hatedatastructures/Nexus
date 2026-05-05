// Package testutil 提供测试用的模拟对象和辅助工具。
// 本文件提供通用的测试辅助函数。
package testutil

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"
)

// ───────────────────────────── JSON 断言 ─────────────────────────────

// AssertJSONEqual 断言两个 JSON 字符串在语义上相等。
// 忽略键顺序和空白差异。
func AssertJSONEqual(t *testing.T, expected, actual string) {
	t.Helper()

	var expectedObj, actualObj any

	if err := json.Unmarshal([]byte(expected), &expectedObj); err != nil {
		t.Fatalf("解析 expected JSON 失败: %v\n原始字符串: %s", err, expected)
	}

	if err := json.Unmarshal([]byte(actual), &actualObj); err != nil {
		t.Fatalf("解析 actual JSON 失败: %v\n原始字符串: %s", err, actual)
	}

	if !reflect.DeepEqual(expectedObj, actualObj) {
		// 格式化输出以便调试
		expectedPretty, _ := json.MarshalIndent(expectedObj, "", "  ")
		actualPretty, _ := json.MarshalIndent(actualObj, "", "  ")
		t.Errorf("JSON 不相等:\n期望:\n%s\n实际:\n%s", expectedPretty, actualPretty)
	}
}

// ───────────────────────────── 条件等待 ─────────────────────────────

// WaitForCondition 等待条件函数返回 true 或超时。
// 每 10ms 检查一次条件。
// 超时时调用 t.Fatal。
func WaitForCondition(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		if condition() {
			return
		}

		<-ticker.C

		if time.Now().After(deadline) {
			t.Fatalf("等待条件超时 (%v)", timeout)
		}
	}
}

// WaitForConditionWithMessage 等待条件函数返回 true 或超时。
// 超时时使用提供的消息调用 t.Fatal。
func WaitForConditionWithMessage(t *testing.T, timeout time.Duration, condition func() bool, msg string) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		if condition() {
			return
		}

		<-ticker.C

		if time.Now().After(deadline) {
			t.Fatalf("等待条件超时 (%v): %s", timeout, msg)
		}
	}
}

// ───────────────────────────── 随机 ID ─────────────────────────────

// RandomID 生成一个带前缀的随机 ID。
// 格式: "{prefix}-{12位随机hex}"
// 用于测试中生成唯一标识符。
func RandomID(prefix string) string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		// 如果 crypto/rand 失败，使用时间戳作为后备
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(b))
}

// ───────────────────────────── 断言辅助 ─────────────────────────────

// AssertEqual 断言两个值相等。
func AssertEqual(t *testing.T, expected, actual any) {
	t.Helper()
	if !reflect.DeepEqual(expected, actual) {
		t.Errorf("期望 %v (类型 %T), 实际 %v (类型 %T)", expected, expected, actual, actual)
	}
}

// AssertNotEqual 断言两个值不相等。
func AssertNotEqual(t *testing.T, expected, actual any) {
	t.Helper()
	if reflect.DeepEqual(expected, actual) {
		t.Errorf("期望值不等于 %v, 但实际相等", expected)
	}
}

// AssertNil 断言值为 nil。
func AssertNil(t *testing.T, val any) {
	t.Helper()
	if val != nil && !reflect.ValueOf(val).IsNil() {
		t.Errorf("期望 nil, 实际 %v", val)
	}
}

// AssertNotNil 断言值不为 nil。
func AssertNotNil(t *testing.T, val any) {
	t.Helper()
	if val == nil || reflect.ValueOf(val).IsNil() {
		t.Error("期望非 nil, 实际为 nil")
	}
}

// AssertTrue 断言条件为 true。
func AssertTrue(t *testing.T, condition bool, msg string) {
	t.Helper()
	if !condition {
		t.Errorf("断言失败: %s", msg)
	}
}

// AssertFalse 断言条件为 false。
func AssertFalse(t *testing.T, condition bool, msg string) {
	t.Helper()
	if condition {
		t.Errorf("断言失败: %s", msg)
	}
}

// AssertContains 断言字符串包含子串。
func AssertContains(t *testing.T, s, substr string) {
	t.Helper()
	if !containsStr(s, substr) {
		t.Errorf("期望字符串包含 %q, 实际: %q", substr, s)
	}
}

// AssertNoError 断言错误为 nil。
func AssertNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("期望无错误, 实际: %v", err)
	}
}

// AssertError 断言错误不为 nil。
func AssertError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("期望有错误, 实际为 nil")
	}
}

// containsStr 检查字符串是否包含子串。
func containsStr(s, substr string) bool {
	return len(substr) == 0 || len(s) >= len(substr) && searchStr(s, substr)
}

// searchStr 在字符串中搜索子串。
func searchStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
