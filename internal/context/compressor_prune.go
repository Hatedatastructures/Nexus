// Package context 提供上下文压缩中的工具输出修剪功能。
// 通过将旧的工具调用结果替换为简短摘要，在不调用 LLM 的情况下
// 大幅减少上下文体积。
package context

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"strings"

	"nexus-agent/internal/llm"
)

// pruneOldToolResults 将旧工具消息替换为简短摘要。
//
// 修剪策略:
//  1. 去重: 大型工具结果 (>500 字符) 相同 hash 只保留最新版本，旧版替换为提示
//  2. 摘要化: 将旧工具消息替换为类似 "[terminal] ran `npm test` -> exit 0, 47 lines" 的摘要
//  3. 参数截断: 将旧助手消息中的大型工具调用参数截断到 500 字符
//
// protectTailCount 指定尾部保护消息数，这些消息不受修剪影响。
// protectTailTokens 指定尾部保护的 token 预算 (可选，优先于 protectTailCount)。
//
// 返回修剪后的消息列表和修剪计数。
func (c *Compressor) pruneOldToolResults(messages []llm.Message, protectTailCount int, protectTailTokens int) ([]llm.Message, int) {
	if len(messages) == 0 {
		return messages, 0
	}

	// 创建消息深拷贝副本
	result := make([]llm.Message, len(messages))
	copy(result, messages)
	for i := range result {
		if len(result[i].ToolCalls) > 0 {
			result[i].ToolCalls = make([]llm.ToolCall, len(messages[i].ToolCalls))
			copy(result[i].ToolCalls, messages[i].ToolCalls)
		}
	}
	pruned := 0

	// 构建 call_id → (tool_name, arguments) 索引
	callIDToTool := make(map[string][2]string) // call_id → (name, args)
	for _, msg := range result {
		if msg.Role == llm.RoleAssistant {
			for _, tc := range msg.ToolCalls {
				callIDToTool[tc.ID] = [2]string{tc.Name, tc.Arguments}
			}
		}
	}

	// 确定修剪边界: 保护最近的 N 条消息
	pruneBoundary := c.calcPruneBoundary(result, protectTailCount, protectTailTokens)

	// Pass 1: 去重大型工具结果 (>500 字符)
	// 相同 hash 只保留最新版本 (索引最大的)，旧版本替换为 hash 提示
	pruneHashSeen := make(map[string]int) // hash → index (记录首次出现位置)
	for i := len(result) - 1; i >= 0; i-- {
		msg := &result[i]
		if msg.Role != llm.RoleTool {
			continue
		}
		content := msg.Content
		if len(content) < 500 {
			continue
		}
		h := fmt.Sprintf("%x", md5.Sum([]byte(content)))[:12]
		if existingIdx, exists := pruneHashSeen[h]; exists {
			// 相同 hash 已存在更新版本 (索引更大)，当前为旧版 → 替换为 hash 提示
			result[i].Content = fmt.Sprintf("[重复工具输出 — 与最近调用 #%d 结果相同]", existingIdx)
			pruned++
		} else {
			pruneHashSeen[h] = i
		}
	}

	// Pass 2: 将边界之前的旧工具结果替换为摘要
	for i := 0; i < pruneBoundary && i < len(result); i++ {
		msg := &result[i]
		if msg.Role != llm.RoleTool {
			continue
		}
		content := msg.Content
		if content == "" {
			continue
		}
		// 跳过已去重的结果
		if strings.HasPrefix(content, "[重复工具输出") {
			continue
		}
		// 只修剪超过 200 字符的结果
		if len(content) <= 200 {
			continue
		}
		// 获取对应的工具名称和参数
		callID := msg.ToolCallID
		toolName := "unknown"
		toolArgs := ""
		if info, ok := callIDToTool[callID]; ok {
			toolName = info[0]
			toolArgs = info[1]
		}
		summary := summarizeToolResult(toolName, toolArgs, content)
		result[i].Content = summary
		pruned++
	}

	// Pass 3: 截断边界之前的助手消息中的大型工具调用参数 (截断到 500 字符)
	for i := 0; i < pruneBoundary && i < len(result); i++ {
		msg := &result[i]
		if msg.Role != llm.RoleAssistant || len(msg.ToolCalls) == 0 {
			continue
		}
		modified := false
		newTCs := make([]llm.ToolCall, len(msg.ToolCalls))
		copy(newTCs, msg.ToolCalls)
		for j := range newTCs {
			if len(newTCs[j].Arguments) > 500 {
				truncated := truncateToolCallArgsJSON(newTCs[j].Arguments, 500)
				if truncated != newTCs[j].Arguments {
					newTCs[j].Arguments = truncated
					modified = true
				}
			}
		}
		if modified {
			result[i].ToolCalls = newTCs
		}
	}

	return result, pruned
}

// calcPruneBoundary 计算修剪边界 (不修剪尾部的消息)。
// protectTailTokens 优先于 protectTailCount。
func (c *Compressor) calcPruneBoundary(messages []llm.Message, protectTailCount int, protectTailTokens int) int {
	const charsPerToken = 4

	if protectTailTokens > 0 && protectTailTokens < 1000000 {
		// Token 预算模式: 从尾部倒推，累计到预算即止
		accumulated := 0
		minProtect := protectTailCount
		if minProtect > len(messages) {
			minProtect = len(messages)
		}
		boundary := len(messages)

		for i := len(messages) - 1; i >= 0; i-- {
			msgTokens := len(messages[i].Content)/charsPerToken + 10
			// 加上工具调用参数的 token 数
			for _, tc := range messages[i].ToolCalls {
				msgTokens += len(tc.Arguments) / charsPerToken
			}
			if accumulated+msgTokens > protectTailTokens && (len(messages)-i) >= minProtect {
				boundary = i
				break
			}
			accumulated += msgTokens
			boundary = i
		}
		return boundary
	}

	// 简单计数模式
	if protectTailCount >= len(messages) {
		return 0
	}
	return len(messages) - protectTailCount
}

// summarizeToolResult 生成工具调用的简短摘要。
//
// 格式示例:
//
//	[terminal] ran `npm test` -> exit 0, 47 lines output
//	[read_file] read config.py from line 1 (1,200 chars)
func summarizeToolResult(toolName, toolArgs, content string) string {
	contentLen := len(content)
	lineCount := strings.Count(content, "\n") + 1
	if len(strings.TrimSpace(content)) == 0 {
		lineCount = 0
	}

	// 尝试解析参数
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
		// 通用后备: 使用前两个参数
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
// 使用 json.Unmarshal 代替正则以避免每次调用的编译开销。
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
// 保持 JSON 有效性，避免下游提供者因损坏的 JSON 而返回 400。
func truncateToolCallArgsJSON(args string, maxLen int) string {
	var parsed any
	if err := json.Unmarshal([]byte(args), &parsed); err != nil {
		// 不是有效 JSON — 原样返回，只做简单的字节截断
		if len(args) > maxLen {
			return args[:maxLen] + "...[truncated]"
		}
		return args
	}

	// 根据目标长度计算单个字符串的最大长度
	truncated := truncateJSONValues(parsed, max(50, maxLen/4))
	result, err := json.Marshal(truncated)
	if err != nil {
		// 序列化失败 — 原样截断
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

