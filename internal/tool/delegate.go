// Package tool 提供子代理委派工具。
// 创建独立的子代理实例来执行复杂任务，
// 子代理拥有隔离的上下文和受限的工具集。
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// ───────────────────────────── 子代理委派工具 ─────────────────────────────

// 委托子代理不允许使用的工具名称。
// 防止递归委派和跨会话副作用。
var delegateBlockedTools = map[string]bool{
	"delegate_task": true, // 禁止递归委派
	"memory":        true, // 禁止写入共享记忆
}

// DelegateTaskTool 实现子代理委派功能。
type DelegateTaskTool struct{}

// SubAgentRunner 是注入的子代理执行函数。
// 由 CLI/网关在启动时通过 SetSubAgentRunner 注入，避免 tool → agent 循环导入。
// 使用 atomic.Value 保证并发安全的读写。
var subAgentRunner atomic.Value // stores func(ctx context.Context, systemPrompt, task string) (string, error)

// SetSubAgentRunner 注入子代理执行函数。
// 应在应用启动时调用，传入实际的 AIAgent 创建和执行逻辑。
func SetSubAgentRunner(fn func(ctx context.Context, systemPrompt, task string) (string, error)) {
	subAgentRunner.Store(fn)
}

// GetSubAgentRunner 返回当前子代理执行函数。
func GetSubAgentRunner() func(ctx context.Context, systemPrompt, task string) (string, error) {
	if v := subAgentRunner.Load(); v != nil {
		return v.(func(ctx context.Context, systemPrompt, task string) (string, error))
	}
	return nil
}

// Name 返回工具名称。
func (t *DelegateTaskTool) Name() string { return "delegate_task" }

// Description 返回工具描述。
func (t *DelegateTaskTool) Description() string {
	return "将复杂任务委派给子代理执行。子代理拥有隔离的上下文和受限工具集。支持单个任务和批量任务模式。"
}

// Toolset 返回工具所属工具集。
func (t *DelegateTaskTool) Toolset() string { return "delegate" }

// Emoji 返回工具图标。
func (t *DelegateTaskTool) Emoji() string { return "🤖" }

// IsAvailable 始终可用 (不需要外部依赖)。
func (t *DelegateTaskTool) IsAvailable() bool { return true }

// MaxResultChars 返回结果最大字符数。
func (t *DelegateTaskTool) MaxResultChars() int { return 50000 }

// Schema 返回工具的 JSON Schema。
func (t *DelegateTaskTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "delegate_task",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task": map[string]any{
					"type":        "string",
					"description": "委派任务的描述。单个字符串为单任务，JSON 数组字符串为批量任务。",
				},
				"context": map[string]any{
					"type":        "string",
					"description": "提供给子代理的附加上下文信息",
				},
				"role": map[string]any{
					"type":        "string",
					"description": "子代理角色: leaf (叶子节点，执行具体任务) 或 orchestrator (编排者，可进一步委派)",
					"enum":        []string{"leaf", "orchestrator"},
				},
			},
			"required": []string{"task"},
		},
	}
}

// Execute 执行子代理委派。
// 创建隔离的子代理 → 执行任务 → 收集结果 → 返回摘要。
func (t *DelegateTaskTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	task, ok := args["task"].(string)
	if !ok || task == "" {
		return ToolError("参数 task 是必填项且必须为字符串"), nil
	}

	contextInfo, _ := args["context"].(string)
	role, _ := args["role"].(string)
	if role == "" {
		role = "leaf"
	}

	// 创建带超时的 context
	timeout := 600 * time.Second
	subCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	slog.Info("starting sub-agent delegation",
		"task", truncateForLog(task, 200),
		"role", role,
		"timeout", timeout.String(),
	)

	// 检查批量任务 (尝试 JSON 数组解析，而非脆弱的首字符判断)
	if len(task) > 0 && task[0] == '[' {
		var probe []json.RawMessage
		if err := json.Unmarshal([]byte(task), &probe); err == nil {
			return t.executeBatchTasks(subCtx, task, contextInfo, role)
		}
		// 不是合法 JSON 数组，按单任务处理
	}

	// 单任务执行
	result, err := t.executeSingleTask(subCtx, task, contextInfo, role)
	if err != nil {
		slog.Error("sub-agent execution failed", "task", truncateForLog(task, 100), "err", err)
		return ToolError(fmt.Sprintf("子代理执行失败: %v", err)), nil
	}

	return result, nil
}

// executeSingleTask 执行单个委派任务。
// 通过注入的 subAgentRunner 执行，避免 tool → agent 循环导入。
func (t *DelegateTaskTool) executeSingleTask(ctx context.Context, task, contextInfo, role string) (string, error) {
	// 构建子代理的系统提示词
	systemPrompt := buildSubAgentPrompt(task, contextInfo, role)

	runner := GetSubAgentRunner()
	if runner == nil {
		return ToolError("子代理委派不可用: 子代理执行器未注入。请在 CLI 或网关启动时调用 tool.SetSubAgentRunner()"), nil
	}

	slog.Debug("calling sub-agent executor", "task", truncateForLog(task, 100))

	// 执行子代理
	result, err := runner(ctx, systemPrompt, task)
	if err != nil {
		return ToolError(fmt.Sprintf("子代理执行失败: %v", err)), nil
	}

	return ToolResult(map[string]any{
		"output": result,
		"task":   task,
		"role":   role,
		"status": "completed",
	}), nil
}

// executeBatchTasks 执行批量委派任务。
// 解析 JSON 数组，使用信号量控制并发度 (最多 3 个 goroutine) 并行执行。
// 每个子任务享有独立超时 (总超时 / 任务数，最小 60 秒)。
func (t *DelegateTaskTool) executeBatchTasks(ctx context.Context, tasksJSON, contextInfo, role string) (string, error) {
	var tasks []map[string]string
	if err := json.Unmarshal([]byte(tasksJSON), &tasks); err != nil {
		// 尝试解析为字符串数组
		var strTasks []string
		if err2 := json.Unmarshal([]byte(tasksJSON), &strTasks); err2 != nil {
			return ToolError(fmt.Sprintf("批量任务 JSON 解析失败: %v", err)), nil
		}
		for _, s := range strTasks {
			tasks = append(tasks, map[string]string{"task": s})
		}
	}

	if len(tasks) == 0 {
		return ToolError("批量任务列表为空"), nil
	}

	const maxBatchTasks = 50
	if len(tasks) > maxBatchTasks {
		return ToolError(fmt.Sprintf("批量任务数量 %d 超过上限 %d", len(tasks), maxBatchTasks)), nil
	}

	slog.Info("batch subtasks started", "total", len(tasks))

	// 计算每个子任务的超时
	// 从父 context 获取剩余超时，平均分配给各任务，最小 60 秒
	perTaskTimeout := 60 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		avg := remaining / time.Duration(len(tasks))
		if avg > perTaskTimeout {
			perTaskTimeout = avg
		}
	}

	// 结果收集
	type taskResult struct {
		Index  int    `json:"index"`
		Task   string `json:"task"`
		Status string `json:"status"`
		Error  string `json:"error,omitempty"`
		Result string `json:"result,omitempty"`
	}

	results := make([]taskResult, len(tasks))
	var wg sync.WaitGroup

	// 信号量：控制最大并发数为 3
	sem := make(chan struct{}, 3)

	for i, task := range tasks {
		taskDesc := task["task"]
		if taskDesc == "" {
			results[i] = taskResult{
				Index:  i + 1,
				Task:   "",
				Status: "skipped",
				Error:  "任务描述为空",
			}
			continue
		}

		wg.Add(1)
		go func(idx int, desc string) {
			defer wg.Done()

			// 获取信号量
			sem <- struct{}{}
			defer func() { <-sem }()

			slog.Info("parallel subtask executing", "index", idx+1, "total", len(tasks), "task", truncateForLog(desc, 100))

			// 为子任务创建带独立超时的 context
			subCtx, cancel := context.WithTimeout(ctx, perTaskTimeout)
			defer cancel()

			result, err := t.executeSingleTask(subCtx, desc, contextInfo, role)

			status := "completed"
			errMsg := ""
			if err != nil {
				status = "failed"
				errMsg = err.Error()
				slog.Error("parallel subtask failed", "index", idx+1, "err", err)
			}

			results[idx] = taskResult{
				Index:  idx + 1,
				Task:   desc,
				Status: status,
				Error:  errMsg,
				Result: result,
			}
		}(i, taskDesc)
	}

	// 等待所有子任务完成
	wg.Wait()

	// 统计结果
	completed := 0
	failed := 0
	for _, r := range results {
		switch r.Status {
		case "completed":
			completed++
		case "failed":
			failed++
		}
	}

	slog.Info("batch subtasks completed", "total", len(tasks), "completed", completed, "failed", failed)

	resultJSON, _ := json.Marshal(map[string]any{
		"total":     len(tasks),
		"completed": completed,
		"failed":    failed,
		"results":   results,
	})

	return string(resultJSON), nil
}

// buildSubAgentPrompt 构建子代理的系统提示词。
// 包含角色定义、工具限制、输出规范和上下文信息。
func buildSubAgentPrompt(task, contextInfo, role string) string {
	var sb strings.Builder

	// 角色定义
	sb.WriteString("你是一个专业的 AI 子代理实例，被主代理委派来独立执行特定任务。\n")
	sb.WriteString("你拥有与主代理相同的模型能力，但运行在隔离的上下文中。\n\n")

	// 角色类型
	if role == "leaf" {
		sb.WriteString("## 角色\n")
		sb.WriteString("你是一个叶子节点子代理，负责直接执行具体任务。\n")
		sb.WriteString("你需要完成分配的任务并返回结果，不要尝试进一步委派。\n\n")
	} else {
		sb.WriteString("## 角色\n")
		sb.WriteString("你是一个编排者子代理，可以协调多个子步骤来完成复杂任务。\n\n")
	}

	// 工具限制
	sb.WriteString("## 工具使用限制\n")
	sb.WriteString("你被禁止使用以下工具:\n")
	for blockedTool := range delegateBlockedTools {
		fmt.Fprintf(&sb, "- **%s**: 子代理不能使用 (防止递归委派和跨会话副作用)\n", blockedTool)
	}
	sb.WriteString("请使用其他可用工具来完成任务。如果任务超出你的工具能力范围，请在结果中明确说明。\n\n")

	// 输出规范
	sb.WriteString("## 输出要求\n")
	sb.WriteString("- 回答应简洁、聚焦任务，不要添加多余的对话或客套语\n")
	sb.WriteString("- 如果任务需要多步骤，先简要说明步骤再执行\n")
	sb.WriteString("- 如果任务无法完成，清楚说明原因和限制\n")
	sb.WriteString("- 如果涉及代码或文件操作，确保操作的安全性\n\n")

	// 任务描述
	sb.WriteString("## 委派任务\n")
	sb.WriteString(task)
	sb.WriteString("\n")

	// 附加上下文
	if contextInfo != "" {
		sb.WriteString("\n## 附加上下文\n")
		sb.WriteString(contextInfo)
		sb.WriteString("\n")
	}

	return sb.String()
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

// truncateForLog 截断字符串用于日志输出 (UTF-8 安全)。
func truncateForLog(s string, maxLen int) string {
	if utf8.RuneCountInString(s) <= maxLen {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxLen]) + "..."
}

