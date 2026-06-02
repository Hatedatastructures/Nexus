package platforms

import (
	"encoding/json"
	"os"
	"strings"
)

// maxAPIResponseSize limits response body reads from external API calls to 10 MB.
const maxAPIResponseSize = 10 << 20 // 10 MB

// ───────────────────────────── 通用辅助函数 ─────────────────────────────

// getString 从 map 中安全获取字符串值。
func getString(m map[string]any, key string, def ...string) string {
	if m == nil {
		if len(def) > 0 {
			return def[0]
		}
		return ""
	}
	v, ok := m[key]
	if !ok {
		if len(def) > 0 {
			return def[0]
		}
		return ""
	}
	s, ok := v.(string)
	if !ok {
		if len(def) > 0 {
			return def[0]
		}
		return ""
	}
	return s
}

// getInt 从 map 中安全获取整数值。
func getInt(m map[string]any, key string, def ...int) int {
	if m == nil {
		if len(def) > 0 {
			return def[0]
		}
		return 0
	}
	v, ok := m[key]
	if !ok {
		if len(def) > 0 {
			return def[0]
		}
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			if len(def) > 0 {
				return def[0]
			}
			return 0
		}
		return int(i)
	default:
		if len(def) > 0 {
			return def[0]
		}
		return 0
	}
}

// getMap 从 map 中安全获取子 map。
func getMap(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	v, ok := m[key]
	if !ok {
		return nil
	}
	sub, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return sub
}

// getList 从 map 中安全获取字符串切片。
func getList(m map[string]any, key string) []string {
	if m == nil {
		return nil
	}
	v, ok := m[key]
	if !ok {
		return nil
	}
	arr, ok := v.([]string)
	if ok {
		return arr
	}
	arrAny, ok := v.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(arrAny))
	for _, item := range arrAny {
		s, ok := item.(string)
		if ok {
			result = append(result, s)
		}
	}
	return result
}

// getListAny 从 map 中安全获取 []any 切片。
func getListAny(m map[string]any, key string) []any {
	if m == nil {
		return nil
	}
	v, ok := m[key]
	if !ok {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	return arr
}

// getEnvList 从环境变量获取逗号分隔的字符串列表。
func getEnvList(key string) []string {
	val := os.Getenv(key)
	if val == "" {
		return nil
	}
	parts := strings.Split(val, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
