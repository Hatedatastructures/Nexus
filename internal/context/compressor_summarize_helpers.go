package context

import (
	"fmt"
	"regexp"
	"strings"

	"nexus-agent/internal/llm"
)

// FormatSummary 从消息列表中提取结构化信息并格式化为摘要文本。
//
// 扫描消息中的工具调用、助手决策和用户请求，生成符合默认摘要模板的结构化文本。
// 可用于在不调用 LLM 的情况下生成快速摘要，或作为 LLM 摘要的补充。
//
// 参数:
//   - messages: 需要提取消息列表
//
// 返回格式化的结构化摘要文本。
func FormatSummary(messages []llm.Message) string {
	var toolCalls []string    // 工具调用摘要
	var decisions []string    // 关键决策
	var pendingTasks []string // 待处理任务
	var contextInfo []string  // 当前上下文
	var keyFiles []string     // 关键文件
	var timeline []string     // 完整时间线

	// 消息统计计数
	userCount := 0
	assistantCount := 0
	toolCount := 0
	totalChars := 0

	seenUserRequests := make(map[string]bool)
	seenFiles := make(map[string]string) // 文件路径 -> 操作类型 (已读取/已修改/已创建)

	for _, msg := range messages {
		totalChars += len(msg.Content)
		for _, tc := range msg.ToolCalls {
			totalChars += len(tc.Arguments)
		}

		switch msg.Role {
		case llm.RoleUser:
			userCount++
			// 提取用户请求作为待处理任务
			if msg.Content != "" {
				content := strings.TrimSpace(msg.Content)
				// 去重: 跳过已见过的短消息 (如 "ok", "yes" 等)
				if len(content) > 10 && !seenUserRequests[content] {
					seenUserRequests[content] = true
					// 截断过长的请求
					if len(content) > 200 {
						content = content[:197] + "..."
					}
					pendingTasks = append(pendingTasks, "- "+content)
					timeline = append(timeline, fmt.Sprintf("- 用户请求: %s", truncateStr(content, 100)))
				}
			}

		case llm.RoleAssistant:
			assistantCount++
			// 提取工具调用信息
			for _, tc := range msg.ToolCalls {
				toolName := tc.Name
				args := redactSensitiveData(tc.Arguments)
				// 提取简短参数描述
				argDesc := extractArgSummary(args)
				entry := fmt.Sprintf("- [%s] %s", toolName, argDesc)
				toolCalls = append(toolCalls, entry)
				timeline = append(timeline, fmt.Sprintf("- 调用 [%s] %s", toolName, argDesc))

				// 从工具调用参数中提取文件路径
				path := extractFilePath(args)
				if path != "" {
					op := inferFileOperation(toolName)
					if existing, ok := seenFiles[path]; ok {
						// 已修改/已创建 优先级高于 已读取
						if op != "已读取" || existing == "" {
							seenFiles[path] = op
						}
					} else {
						seenFiles[path] = op
					}
				}
			}

			// 从助手消息中提取决策 (检测包含决策性关键词的句子)
			if msg.Content != "" {
				for _, line := range strings.Split(msg.Content, "\n") {
					line = strings.TrimSpace(line)
					if isDecisionStatement(line) {
						decisions = append(decisions, "- "+line)
					}
				}
				// 添加助手回复事件到时间线
				if len(msg.ToolCalls) == 0 && len(msg.Content) > 0 {
					timeline = append(timeline, fmt.Sprintf("- 助手回复: %s", truncateStr(msg.Content, 80)))
				}
			}

		case llm.RoleTool:
			toolCount++
			// 从工具结果中提取有价值的上下文信息
			if msg.Content != "" && len(msg.Content) < 500 {
				// 保留简短的工具结果作为上下文
				content := strings.TrimSpace(msg.Content)
				if len(content) > 200 {
					content = content[:197] + "..."
				}
				contextInfo = append(contextInfo, "- "+content)
				timeline = append(timeline, fmt.Sprintf("- 工具结果: %s", truncateStr(content, 80)))
			}
		}
	}

	// 构建关键文件列表
	for path, op := range seenFiles {
		if op == "" {
			op = "已读取"
		}
		keyFiles = append(keyFiles, fmt.Sprintf("- %s — %s", path, op))
	}

	// 估算总 token 数
	const charsPerToken = 4
	totalTokenEst := totalChars / charsPerToken

	// 组装结构化摘要
	var sb strings.Builder

	// 消息统计
	sb.WriteString("## 消息统计\n")
	fmt.Fprintf(&sb, "- 用户消息: %d 条\n", userCount)
	fmt.Fprintf(&sb, "- 助手消息: %d 条\n", assistantCount)
	fmt.Fprintf(&sb, "- 工具结果: %d 条\n", toolCount)
	fmt.Fprintf(&sb, "- 总 token 估算: ~%d\n", totalTokenEst)

	// 工具调用摘要
	sb.WriteString("\n## 工具调用摘要\n")
	if len(toolCalls) > 0 {
		// 限制输出数量，避免摘要过长
		limit := toolCalls
		if len(limit) > 20 {
			limit = limit[len(limit)-20:]
			fmt.Fprintf(&sb, "[共 %d 次调用，仅显示最近 20 次]\n", len(toolCalls))
		}
		sb.WriteString(strings.Join(limit, "\n"))
	} else {
		sb.WriteString("None.")
	}

	// 关键文件
	sb.WriteString("\n\n## 关键文件\n")
	if len(keyFiles) > 0 {
		sb.WriteString(strings.Join(keyFiles, "\n"))
	} else {
		sb.WriteString("None.")
	}

	// 关键决策
	sb.WriteString("\n\n## 关键决策\n")
	if len(decisions) > 0 {
		sb.WriteString(strings.Join(decisions, "\n"))
	} else {
		sb.WriteString("None.")
	}

	// 待处理任务
	sb.WriteString("\n\n## 待处理任务\n")
	if len(pendingTasks) > 0 {
		sb.WriteString(strings.Join(pendingTasks, "\n"))
	} else {
		sb.WriteString("None.")
	}

	// 完整时间线
	sb.WriteString("\n\n## 完整时间线\n")
	if len(timeline) > 0 {
		sb.WriteString(strings.Join(timeline, "\n"))
	} else {
		sb.WriteString("None.")
	}

	// 当前上下文
	sb.WriteString("\n\n## 当前上下文\n")
	if len(contextInfo) > 0 {
		sb.WriteString(strings.Join(contextInfo, "\n"))
	} else {
		sb.WriteString("None.")
	}

	return sb.String()
}

// extractArgSummary 从 JSON 参数字符串中提取简短描述。
// 尝试解析 JSON 并提取关键字段 (path, command, query, goal 等)。
func extractArgSummary(argsJSON string) string {
	if argsJSON == "" {
		return ""
	}

	args := parseArgs(argsJSON)
	if len(args) == 0 {
		return ""
	}

	// 优先提取常见关键字段
	keyFields := []string{"path", "command", "query", "goal", "pattern", "action", "target"}
	for _, key := range keyFields {
		if v, ok := args[key]; ok {
			s := fmt.Sprintf("%v", v)
			if len(s) > 80 {
				s = s[:77] + "..."
			}
			return fmt.Sprintf("%s=%s", key, s)
		}
	}

	// 回退: 使用第一个参数
	for k, v := range args {
		s := fmt.Sprintf("%v", v)
		if len(s) > 60 {
			s = s[:57] + "..."
		}
		return fmt.Sprintf("%s=%s", k, s)
	}

	return ""
}

// isDecisionStatement 检测文本是否为决策性陈述。
// 通过关键词匹配判断，避免误判日常对话。
func isDecisionStatement(line string) bool {
	if len(line) < 10 {
		return false
	}

	// 决策性关键词
	keywords := []string{
		"决定", "选择", "采用", "使用", "方案", "策略",
		"decided", "chose", "using", "approach", "strategy",
		"改为", "替换为", "迁移到", "切换到",
	}

	lower := strings.ToLower(line)
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}

	return false
}

// extractFilePath 从 JSON 参数字符串中提取文件路径。
// 依次尝试 "path", "file_path", "file", "filename", "destination" 字段。
func extractFilePath(argsJSON string) string {
	args := parseArgs(argsJSON)
	pathKeys := []string{"path", "file_path", "file", "filename", "destination"}
	for _, key := range pathKeys {
		if v, ok := args[key]; ok {
			s, ok := v.(string)
			if ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// inferFileOperation 根据工具名称推断文件操作类型。
// 返回 "已读取"、"已修改" 或 "已创建"。
func inferFileOperation(toolName string) string {
	switch toolName {
	case "create_file", "write_file", "Write", "NotebookEdit":
		return "已创建"
	case "edit_file", "patch", "Edit", "replace":
		return "已修改"
	case "read_file", "Read", "Grep", "Glob", "head", "tail", "cat":
		return "已读取"
	default:
		return "已读取"
	}
}

// sensitivePatterns 匹配 API key、token、password、secret 等敏感值的正则模式。
var sensitivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(api[_-]?key|apikey)\s*[:=]\s*\S+`),
	regexp.MustCompile(`(?i)(token|access[_-]?token|auth[_-]?token|bearer)\s*[:=]\s*\S+`),
	regexp.MustCompile(`(?i)(password|passwd|pwd|secret)\s*[:=]\s*\S+`),
	regexp.MustCompile(`(?i)(credential|connection[_-]?string)\s*[:=]\s*\S+`),
	regexp.MustCompile(`sk-[a-zA-Z0-9]{20,}`),
	regexp.MustCompile(`eyJ[a-zA-Z0-9_-]{10,}\.[a-zA-Z0-9_-]{10,}`),
}

// redactSensitiveData 将输入中的敏感数据替换为 [REDACTED]。
func redactSensitiveData(input string) string {
	for _, re := range sensitivePatterns {
		input = re.ReplaceAllString(input, "[REDACTED]")
	}
	return input
}

// truncateStr 将字符串截断到指定长度，超出时追加 "..."。
func truncateStr(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	// 去除换行，保持单行
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
