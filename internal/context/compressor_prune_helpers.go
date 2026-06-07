// Package context 提供修剪辅助函数：工具调用结果摘要、参数截断和 JSON 处理。
package context

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ───────────────────────────── 修剪辅助函数 ─────────────────────────────

// summarizeToolResult 生成工具调用的简短摘要。
func summarizeToolResult(toolName, toolArgs, content string) string {
	contentLen := len(content)
	lineCount := strings.Count(content, "\n") + 1
	if len(strings.TrimSpace(content)) == 0 {
		lineCount = 0
	}

	args := parseArgs(toolArgs)

	switch toolName {
	case "terminal":
		cmd := argStr(args, "command")
		if len(cmd) > 80 {
			cmd = cmd[:77] + "..."
		}
		exitCode := extractJSONInt(content, "exit_code")
		return fmt.Sprintf("[terminal] ran `%s` -> exit %d, %d lines output", cmd, exitCode, lineCount)
	case "read_file":
		path := argStr(args, "path")
		offset := argInt(args, "offset", 1)
		return fmt.Sprintf("[read_file] read %s from line %d (%d chars)", path, offset, contentLen)
	case "write_file":
		path := argStr(args, "path")
		return fmt.Sprintf("[write_file] wrote to %s (%d chars)", path, contentLen)
	case "search_files":
		pattern := argStr(args, "pattern")
		path := argStr(args, "path")
		target := argStr(args, "target")
		if target == "" || target == "?" {
			target = "content"
		}
		count := extractJSONInt(content, "total_count")
		return fmt.Sprintf("[search_files] %s search for '%s' in %s -> %d matches", target, pattern, path, count)
	case "patch":
		path := argStr(args, "path")
		mode := argStr(args, "mode")
		if mode == "" || mode == "?" {
			mode = "replace"
		}
		return fmt.Sprintf("[patch] %s in %s (%d chars result)", mode, path, contentLen)
	case "web_search":
		query := argStr(args, "query")
		return fmt.Sprintf("[web_search] query='%s' (%d chars result)", query, contentLen)
	case "web_extract":
		urls := args["urls"]
		urlDesc := "?"
		if urlList, ok := urls.([]any); ok && len(urlList) > 0 {
			urlDesc = fmt.Sprintf("%v", urlList[0])
		}
		return fmt.Sprintf("[web_extract] %s (%d chars)", urlDesc, contentLen)
	case "delegate_task":
		goal := argStr(args, "goal")
		if len(goal) > 60 {
			goal = goal[:57] + "..."
		}
		return fmt.Sprintf("[delegate_task] '%s' (%d chars result)", goal, contentLen)
	case "execute_code":
		code := argStr(args, "code")
		codePreview := strings.ReplaceAll(code[:min(60, len(code))], "\n", " ")
		if len(code) > 60 {
			codePreview += "..."
		}
		return fmt.Sprintf("[execute_code] `%s` (%d lines output)", codePreview, lineCount)
	case "todo":
		return "[todo] 更新了任务列表"
	case "memory":
		action := argStr(args, "action")
		target := argStr(args, "target")
		return fmt.Sprintf("[memory] %s on %s", action, target)
	default:
		parts := make([]string, 0)
		for k, v := range args {
			s := fmt.Sprintf("%v", v)
			if len(s) > 40 {
				s = s[:37] + "..."
			}
			parts = append(parts, fmt.Sprintf("%s=%s", k, s))
			if len(parts) >= 2 {
				break
			}
		}
		return fmt.Sprintf("[%s] %s (%d chars result)", toolName, strings.Join(parts, " "), contentLen)
	}
}

// parseArgs 尝试将 JSON 参数字符串解析为 map。
func parseArgs(argsJSON string) map[string]any {
	if argsJSON == "" {
		return make(map[string]any)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &parsed); err != nil {
		return make(map[string]any)
	}
	return parsed
}

// argStr 安全地从参数 map 中获取字符串值。
func argStr(args map[string]any, key string) string {
	v, ok := args[key]
	if !ok {
		return "?"
	}
	s, ok := v.(string)
	if !ok {
		return fmt.Sprintf("%v", v)
	}
	if s == "" {
		return "?"
	}
	return s
}

// argInt 安全地从参数 map 中获取整数值。
func argInt(args map[string]any, key string, defaultVal int) int {
	v, ok := args[key]
	if !ok {
		return defaultVal
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	default:
		return defaultVal
	}
}

// extractJSONInt 尝试从 JSON 字符串中提取整数字段值。
func extractJSONInt(content, key string) int {
	var obj map[string]any
	if json.Unmarshal([]byte(content), &obj) != nil {
		return -1
	}
	switch v := obj[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	default:
		return -1
	}
}

// truncateToolCallArgsJSON 截断工具调用参数 JSON 到指定长度。
func truncateToolCallArgsJSON(args string, maxLen int) string {
	var parsed any
	if err := json.Unmarshal([]byte(args), &parsed); err != nil {
		if len(args) > maxLen {
			return args[:maxLen] + "...[truncated]"
		}
		return args
	}

	truncated := truncateJSONValues(parsed, max(50, maxLen/4))
	result, err := json.Marshal(truncated)
	if err != nil {
		if len(args) > maxLen {
			return args[:maxLen] + "...[truncated]"
		}
		return args
	}
	return string(result)
}

// truncateJSONValues 递归截断 JSON 结构中的长字符串值。
func truncateJSONValues(v any, maxLen int) any {
	switch val := v.(type) {
	case string:
		if len(val) > maxLen {
			return val[:maxLen] + "...[truncated]"
		}
		return val
	case map[string]any:
		result := make(map[string]any, len(val))
		for k, vv := range val {
			result[k] = truncateJSONValues(vv, maxLen)
		}
		return result
	case []any:
		result := make([]any, len(val))
		for i, vv := range val {
			result[i] = truncateJSONValues(vv, maxLen)
		}
		return result
	default:
		return v
	}
}
