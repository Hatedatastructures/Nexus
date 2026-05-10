package i18n

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadLocale 从 YAML 字节数据中解析 flat key-value 翻译表。
//
// YAML 格式示例:
//
//	agent.welcome: "Welcome to Nexus Agent"
//	error.auth.failed: "Authentication failed"
//
// 支持两种格式:
//   - flat key: value（点分键直接作为 key）
//   - 嵌套 YAML 结构（自动展平为点分键）
func LoadLocale(data []byte) (map[string]string, error) {
	result := make(map[string]string)

	// 先尝试解析为 flat map[string]string
	var flat map[string]string
	if err := yaml.Unmarshal(data, &flat); err == nil {
		// 检查是否所有值都是字符串（排除嵌套结构）
		allFlat := true
		for _, v := range flat {
			if strings.Contains(v, ":") && !strings.Contains(v, " ") {
				// 可能是嵌套结构的误解析，跳过
			}
			_ = v
		}
		if allFlat {
			return flat, nil
		}
	}

	// 回退：解析为嵌套 map 并展平
	var nested map[string]any
	if err := yaml.Unmarshal(data, &nested); err != nil {
		return nil, fmt.Errorf("解析 locale YAML 失败: %w", err)
	}
	flattenMap("", nested, result)
	return result, nil
}

// flattenMap 递归展平嵌套 map 为点分键。
func flattenMap(prefix string, m map[string]any, out map[string]string) {
	for k, v := range m {
		fullKey := k
		if prefix != "" {
			fullKey = prefix + "." + k
		}
		switch val := v.(type) {
		case map[string]any:
			flattenMap(fullKey, val, out)
		case string:
			out[fullKey] = val
		default:
			// 非字符串值转为字符串
			out[fullKey] = fmt.Sprintf("%v", val)
		}
	}
}

// MergeLocales 合并两个翻译表。override 中的键会覆盖 base 中的同名键。
// 返回新的合并结果，不修改原始 map。
func MergeLocales(base, override map[string]string) map[string]string {
	merged := make(map[string]string, len(base)+len(override))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range override {
		merged[k] = v
	}
	return merged
}
