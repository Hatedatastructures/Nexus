package platforms

import (
	"encoding/json"
	"testing"
)

// ---------------------------------------------------------------------------
// getString
// ---------------------------------------------------------------------------

func TestGetString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		m        map[string]any
		key      string
		def      []string
		expected string
	}{
		{"simple value", map[string]any{"k": "v"}, "k", nil, "v"},
		{"missing key no def", map[string]any{"a": "1"}, "k", nil, ""},
		{"missing key with def", map[string]any{"a": "1"}, "k", []string{"fallback"}, "fallback"},
		{"nil map no def", nil, "k", nil, ""},
		{"nil map with def", nil, "k", []string{"fb"}, "fb"},
		{"non-string value no def", map[string]any{"k": 123}, "k", nil, ""},
		{"non-string value with def", map[string]any{"k": 123}, "k", []string{"fb"}, "fb"},
		{"empty string value", map[string]any{"k": ""}, "k", nil, ""},
		{"unicode value", map[string]any{"k": "你好世界"}, "k", nil, "你好世界"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := getString(tc.m, tc.key, tc.def...)
			if got != tc.expected {
				t.Errorf("getString() = %q, want %q", got, tc.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// getInt
// ---------------------------------------------------------------------------

func TestGetInt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		m        map[string]any
		key      string
		def      []int
		expected int
	}{
		{"float64 value", map[string]any{"k": float64(42)}, "k", nil, 42},
		{"int value", map[string]any{"k": 7}, "k", nil, 7},
		{"int64 value", map[string]any{"k": int64(99)}, "k", nil, 99},
		{"json.Number value", map[string]any{"k": json.Number("100")}, "k", nil, 100},
		{"missing key no def", map[string]any{}, "k", nil, 0},
		{"missing key with def", map[string]any{}, "k", []int{5}, 5},
		{"nil map no def", nil, "k", nil, 0},
		{"nil map with def", nil, "k", []int{10}, 10},
		{"non-numeric value no def", map[string]any{"k": "abc"}, "k", nil, 0},
		{"non-numeric value with def", map[string]any{"k": "abc"}, "k", []int{3}, 3},
		{"zero value", map[string]any{"k": float64(0)}, "k", nil, 0},
		{"negative value", map[string]any{"k": float64(-5)}, "k", nil, -5},
		{"invalid json.Number", map[string]any{"k": json.Number("notanumber")}, "k", nil, 0},
		{"invalid json.Number with def", map[string]any{"k": json.Number("notanumber")}, "k", []int{7}, 7},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := getInt(tc.m, tc.key, tc.def...)
			if got != tc.expected {
				t.Errorf("getInt() = %d, want %d", got, tc.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// getMap
// ---------------------------------------------------------------------------

func TestGetMap(t *testing.T) {
	t.Parallel()

	t.Run("valid sub-map", func(t *testing.T) {
		t.Parallel()
		m := map[string]any{"sub": map[string]any{"a": 1}}
		got := getMap(m, "sub")
		if got == nil {
			t.Fatal("expected non-nil map")
		}
		if v, ok := got["a"].(int); !ok || v != 1 {
			t.Errorf("sub[a] = %v, want 1", got["a"])
		}
	})

	t.Run("missing key", func(t *testing.T) {
		t.Parallel()
		got := getMap(map[string]any{}, "missing")
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("non-map value", func(t *testing.T) {
		t.Parallel()
		m := map[string]any{"sub": "string-value"}
		got := getMap(m, "sub")
		if got != nil {
			t.Errorf("expected nil for non-map value, got %v", got)
		}
	})

	t.Run("nil map", func(t *testing.T) {
		t.Parallel()
		got := getMap(nil, "key")
		if got != nil {
			t.Errorf("expected nil for nil map, got %v", got)
		}
	})
}

// ---------------------------------------------------------------------------
// getList
// ---------------------------------------------------------------------------

func TestGetList(t *testing.T) {
	t.Parallel()

	t.Run("string slice", func(t *testing.T) {
		t.Parallel()
		m := map[string]any{"items": []string{"a", "b"}}
		got := getList(m, "items")
		if len(got) != 2 || got[0] != "a" || got[1] != "b" {
			t.Errorf("getList() = %v, want [a b]", got)
		}
	})

	t.Run("any slice with strings", func(t *testing.T) {
		t.Parallel()
		m := map[string]any{"items": []any{"x", 42, "y"}}
		got := getList(m, "items")
		if len(got) != 2 || got[0] != "x" || got[1] != "y" {
			t.Errorf("getList() = %v, want [x y]", got)
		}
	})

	t.Run("missing key", func(t *testing.T) {
		t.Parallel()
		got := getList(map[string]any{}, "items")
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("non-slice value", func(t *testing.T) {
		t.Parallel()
		m := map[string]any{"items": "not-a-slice"}
		got := getList(m, "items")
		if got != nil {
			t.Errorf("expected nil for non-slice, got %v", got)
		}
	})

	t.Run("nil map", func(t *testing.T) {
		t.Parallel()
		got := getList(nil, "items")
		if got != nil {
			t.Errorf("expected nil for nil map, got %v", got)
		}
	})
}

// ---------------------------------------------------------------------------
// getListAny
// ---------------------------------------------------------------------------

func TestGetListAny(t *testing.T) {
	t.Parallel()

	t.Run("valid any slice", func(t *testing.T) {
		t.Parallel()
		m := map[string]any{"items": []any{1, "two", true}}
		got := getListAny(m, "items")
		if len(got) != 3 {
			t.Errorf("getListAny() length = %d, want 3", len(got))
		}
	})

	t.Run("missing key", func(t *testing.T) {
		t.Parallel()
		got := getListAny(map[string]any{}, "items")
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("non-slice value", func(t *testing.T) {
		t.Parallel()
		m := map[string]any{"items": 123}
		got := getListAny(m, "items")
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("nil map", func(t *testing.T) {
		t.Parallel()
		got := getListAny(nil, "items")
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})
}
