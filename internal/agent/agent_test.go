package agent

import (
	"fmt"
	"testing"
)

func TestIterationBudget(t *testing.T) {
	b := NewIterationBudget(3)
	if b.Consume() != true {
		t.Fatal("第一次消耗应返回 true")
	}
	if b.Consume() != true {
		t.Fatal("第二次消耗应返回 true")
	}
	if b.Consume() != true {
		t.Fatal("第三次消耗应返回 true")
	}
	if b.Consume() != false {
		t.Fatal("第四次消耗应返回 false")
	}
	if b.Remaining() != 0 {
		t.Fatalf("剩余应为 0, 实际 %d", b.Remaining())
	}
	if b.Consumed() != 3 {
		t.Fatalf("已消耗应为 3, 实际 %d", b.Consumed())
	}
}

func TestShouldFallback(t *testing.T) {
	tests := []struct {
		msg      string
		expected bool
	}{
		// 精确匹配分类器模式的计费错误 → ShouldFallback=true
		{"insufficient credits", true},
		{"payment required", true},
		// 速率限制模式 → ShouldFallback=true
		{"rate limit exceeded", true},
		// 认证错误 (401) → ShouldFallback=true
		{"401 unauthorized", true},
		// 上下文溢出 → ShouldFallback=false (应压缩，不切换)
		{"context length exceeded", false},
		// 格式错误 → ShouldFallback=false
		{"invalid request format", false},
		// 无法分类的通用消息 → ShouldFallback=false (ShouldFallback 默认 false)
		{"network timeout", false},
		{"quota exceeded", false},
		// 纯 500 不含具体模式 → 不触发 fallback (仅重试)
		{"500 server error", false},
	}
	for _, tt := range tests {
		err := fmt.Errorf("%s", tt.msg)
		if got := shouldFallback(err); got != tt.expected {
			t.Errorf("shouldFallback(%q) = %v, want %v", tt.msg, got, tt.expected)
		}
	}
}
