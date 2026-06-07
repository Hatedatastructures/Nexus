package testutil

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestAssertJSONEqual(t *testing.T) {
	t.Parallel()

	t.Run("equal objects different order", func(t *testing.T) {
		t.Parallel()
		// Should not fail - key order doesn't matter
		AssertJSONEqual(t, `{"a":1,"b":2}`, `{"b":2,"a":1}`)
	})

	t.Run("equal nested", func(t *testing.T) {
		t.Parallel()
		AssertJSONEqual(t, `{"x":{"y":1}}`, `{"x":{"y":1}}`)
	})

	t.Run("equal arrays", func(t *testing.T) {
		t.Parallel()
		AssertJSONEqual(t, `[1,2,3]`, `[1,2,3]`)
	})
}

func TestWaitForCondition(t *testing.T) {
	t.Run("condition met immediately", func(t *testing.T) {
		WaitForCondition(t, 1*time.Second, func() bool { return true })
	})

	t.Run("condition met after delay", func(t *testing.T) {
		counter := 0
		WaitForCondition(t, 2*time.Second, func() bool {
			counter++
			return counter >= 3
		})
	})
}

func TestWaitForConditionWithMessage(t *testing.T) {
	t.Run("condition met", func(t *testing.T) {
		WaitForConditionWithMessage(t, 1*time.Second, func() bool { return true }, "test condition")
	})
}

func TestRandomID(t *testing.T) {
	t.Parallel()

	t.Run("has prefix", func(t *testing.T) {
		t.Parallel()
		id := RandomID("test")
		if !strings.HasPrefix(id, "test-") {
			t.Errorf("RandomID('test') = %q, should start with 'test-'", id)
		}
	})

	t.Run("unique", func(t *testing.T) {
		t.Parallel()
		id1 := RandomID("a")
		id2 := RandomID("a")
		if id1 == id2 {
			t.Errorf("RandomID should generate unique IDs, got %q twice", id1)
		}
	})

	t.Run("empty prefix", func(t *testing.T) {
		t.Parallel()
		id := RandomID("")
		if !strings.Contains(id, "-") {
			t.Errorf("RandomID('') = %q, expected dash separator", id)
		}
	})

	t.Run("hex suffix length", func(t *testing.T) {
		t.Parallel()
		id := RandomID("pfx")
		// Should be "pfx-" + 12 hex chars
		suffix := id[len("pfx-"):]
		if len(suffix) != 12 {
			t.Errorf("RandomID hex suffix = %d chars, want 12", len(suffix))
		}
	})
}

func TestAssertEqual(t *testing.T) {
	t.Parallel()
	AssertEqual(t, 42, 42)
	AssertEqual(t, "hello", "hello")
	AssertEqual(t, []int{1, 2, 3}, []int{1, 2, 3})
	AssertEqual(t, nil, nil)
}

func TestAssertNotEqual(t *testing.T) {
	t.Parallel()
	AssertNotEqual(t, 42, 43)
	AssertNotEqual(t, "a", "b")
}

func TestAssertNil(t *testing.T) {
	t.Parallel()
	AssertNil(t, nil)
	var p *int = nil
	AssertNil(t, p)
	var ch chan int
	AssertNil(t, ch)
	var sl []int
	AssertNil(t, sl)
}

func TestAssertNotNil(t *testing.T) {
	t.Parallel()
	s := "hello"
	AssertNotNil(t, &s)
	slice := make([]int, 0)
	AssertNotNil(t, slice)
}

func TestAssertTrue(t *testing.T) {
	t.Parallel()
	AssertTrue(t, true, "should be true")
}

func TestAssertFalse(t *testing.T) {
	t.Parallel()
	AssertFalse(t, false, "should be false")
}

func TestAssertContains(t *testing.T) {
	t.Parallel()

	AssertContains(t, "hello world", "world")
	AssertContains(t, "hello", "")
	AssertContains(t, "abc", "abc")
}

func TestAssertNoError(t *testing.T) {
	t.Parallel()
	AssertNoError(t, nil)
}

func TestAssertError(t *testing.T) {
	t.Parallel()
	AssertError(t, fmt.Errorf("test error"))
}

func TestContainsStr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		s, substr string
		want      bool
	}{
		{"hello world", "world", true},
		{"hello", "hello", true},
		{"hello", "xyz", false},
		{"hello", "", true},
		{"", "hello", false},
		{"abc", "abcd", false},
		{"aaa", "aa", true},
	}

	for _, tt := range tests {
		got := containsStr(tt.s, tt.substr)
		if got != tt.want {
			t.Errorf("containsStr(%q, %q) = %v, want %v", tt.s, tt.substr, got, tt.want)
		}
	}
}

func TestSearchStr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		s, substr string
		want      bool
	}{
		{"hello world", "world", true},
		{"hello", "hel", true},
		{"hello", "xyz", false},
		{"aaa", "aa", true},
		{"abc", "abcd", false},
	}

	for _, tt := range tests {
		got := searchStr(tt.s, tt.substr)
		if got != tt.want {
			t.Errorf("searchStr(%q, %q) = %v, want %v", tt.s, tt.substr, got, tt.want)
		}
	}
}
