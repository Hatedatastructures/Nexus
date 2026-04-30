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
		{"quota exceeded", true},
		{"billing error", true},
		{"500 server error", true},
		{"401 unauthorized", false},
		{"network timeout", false},
	}
	for _, tt := range tests {
		err := fmt.Errorf("%s", tt.msg)
		if got := shouldFallback(err); got != tt.expected {
			t.Errorf("shouldFallback(%q) = %v, want %v", tt.msg, got, tt.expected)
		}
	}
}
