// Package batch 提供 SWE-bench 风格的单工具 Agent 运行器。
// 使用终端工具在沙箱中执行代码编辑和测试任务。
package batch

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"

	pkgerrors "nexus-agent/internal/errors"
	"os"
	"strings"
	"time"

	"nexus-agent/internal/config"
	"nexus-agent/internal/llm"
	"nexus-agent/internal/sandbox"
)

// ───────────────────────────── 常量 ─────────────────────────────

const (
	sweMaxIterations     = 30                            // 最大迭代次数
	sweFinalOutputMarker = "MINI_SWE_AGENT_FINAL_OUTPUT" // 完成标记
	sweDefaultTimeout    = 300 * time.Second
)

// ───────────────────────────── 数据结构 ─────────────────────────────

// SWETask 表示一个 SWE 任务。
type SWETask struct {
	ID         string `json:"id"`
	Problem    string `json:"problem"`
	RepoPath   string `json:"repo_path"`
	BaseCommit string `json:"base_commit"`
}

// SWERunner SWE-bench 风格的 Agent 运行器。
type SWERunner struct {
	provider llm.Provider
	env      sandbox.Environment
}

// NewSWERunner 创建 SWE 运行器。
func NewSWERunner(provider llm.Provider, env sandbox.Environment) *SWERunner {
	return &SWERunner{
		provider: provider,
		env:      env,
	}
}

// RunTask 执行单个 SWE 任务。
func (r *SWERunner) RunTask(ctx context.Context, task SWETask) (Trajectory, error) {
	startTime := time.Now()

	messages := []llm.Message{
		{
			Role:    llm.RoleSystem,
			Content: sweSystemPrompt,
		},
		{
			Role:    llm.RoleUser,
			Content: task.Problem,
		},
	}

	var allToolCalls int
	var lastContent string

	for i := 0; i < sweMaxIterations; i++ {
		select {
		case <-ctx.Done():
			return r.buildTrajectory(task, messages, allToolCalls, false, time.Since(startTime)), ctx.Err()
		default:
		}

		req := &llm.ChatRequest{
			Messages:  messages,
			Tools:     sweToolDefs,
			MaxTokens: 4096,
		}

		resp, err := r.provider.CreateChatCompletion(ctx, req)
		if err != nil {
			return r.buildTrajectory(task, messages, allToolCalls, false, time.Since(startTime)), err
		}

		messages = append(messages, llm.Message{
			Role:      llm.RoleAssistant,
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		if resp.Content != "" {
			lastContent = resp.Content
		}

		// 检查完成标记
		if strings.Contains(lastContent, sweFinalOutputMarker) {
			return r.buildTrajectory(task, messages, allToolCalls, true, time.Since(startTime)), nil
		}

		// 无工具调用 = 完成
		if len(resp.ToolCalls) == 0 {
			return r.buildTrajectory(task, messages, allToolCalls, true, time.Since(startTime)), nil
		}

		// 执行工具调用
		for _, tc := range resp.ToolCalls {
			allToolCalls++
			result := r.executeToolCall(ctx, tc)
			messages = append(messages, llm.Message{
				Role:       llm.RoleTool,
				Content:    result,
				ToolCallID: tc.ID,
			})
		}
	}

	return r.buildTrajectory(task, messages, allToolCalls, false, time.Since(startTime)), pkgerrors.New(pkgerrors.CronJob, fmt.Sprintf("达到最大迭代次数 %d", sweMaxIterations))
}

// RunBatch 批量执行 SWE 任务。
func (r *SWERunner) RunBatch(ctx context.Context, tasks []SWETask, outputDir string) (*BatchResult, error) {
	workerFn := func(ctx context.Context, prompt Prompt) (Trajectory, error) {
		task := SWETask{
			ID:      prompt.Text,
			Problem: prompt.Text,
		}
		return r.RunTask(ctx, task)
	}

	runner := NewBatchRunner(DefaultBatchConfig(), workerFn, outputDir)

	var prompts []Prompt
	for _, task := range tasks {
		prompts = append(prompts, Prompt{
			Text:     task.Problem,
			Metadata: map[string]string{"id": task.ID},
		})
	}

	return runner.Run(ctx, prompts)
}

// executeToolCall 执行单个工具调用。
func (r *SWERunner) executeToolCall(ctx context.Context, tc llm.ToolCall) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
		return fmt.Sprintf(`{"error": "参数解析失败: %s"}`, err.Error())
	}

	if r.env == nil {
		return `{"error": "沙箱环境未设置"}`
	}

	command, _ := args["command"].(string)
	if command == "" {
		return `{"error": "命令为空"}`
	}

	result, err := r.env.Execute(ctx, command, &sandbox.ExecuteOptions{
		Timeout: sweDefaultTimeout,
	})
	if err != nil {
		errJSON, _ := json.Marshal(err.Error())
		return fmt.Sprintf(`{"error": %s}`, string(errJSON))
	}

	output := result.Stdout + result.Stderr
	if len(output) > 50000 {
		output = output[:50000] + "\n...[输出已截断]"
	}

	return fmt.Sprintf(`{"output": %q, "exit_code": %d}`, output, result.ExitCode)
}

// buildTrajectory 构建轨迹记录。
func (r *SWERunner) buildTrajectory(task SWETask, messages []llm.Message, toolCalls int, completed bool, duration time.Duration) Trajectory {
	// 提取最终响应
	var finalResponse string
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == llm.RoleAssistant && messages[i].Content != "" {
			finalResponse = messages[i].Content
			break
		}
	}

	return Trajectory{
		Prompt:    task.Problem,
		Response:  finalResponse,
		ToolCalls: toolCalls,
		Completed: completed,
		Duration:  duration,
		Timestamp: time.Now(),
	}
}

// DefaultBatchConfig 返回默认批处理配置。
func DefaultBatchConfig() config.BatchConfig {
	return config.BatchConfig{
		MaxWorkers: 4,
	}
}

// TrajectoryTurn 表示轨迹中的一轮对话（与 agent.TrajectoryTurn 相同结构）。
type TrajectoryTurn struct {
	From    string `json:"from"`
	Value   string `json:"value"`
	ToolUse string `json:"tool_use,omitempty"`
}

// ───────────────────────────── SWE 工具定义 ─────────────────────────────

var sweToolDefs = []llm.ToolSchema{
	{
		Name:        "terminal",
		Description: "在沙箱中执行终端命令。用于代码编辑、文件操作、运行测试等。",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "要执行的终端命令",
				},
			},
			"required": []string{"command"},
		},
	},
}

// ───────────────────────────── 系统提示 ─────────────────────────────

const sweSystemPrompt = `你是一个软件工程 Agent，负责解决代码问题。

工作流程:
1. 阅读和理解问题描述
2. 浏览相关代码文件
3. 实现修复方案
4. 运行测试验证修复

当你完成所有修改并验证后，在最终回复中包含标记: MINI_SWE_AGENT_FINAL_OUTPUT

规则:
- 使用终端工具执行命令 (读文件、编辑代码、运行测试)
- 每次只做一个小改动，然后验证
- 如果测试失败，分析原因并修复
- 不要修改不相关的代码`

// ───────────────────────────── JSONL 输出 ─────────────────────────────

// WriteTrajectoriesJSONL 将轨迹写入 JSONL 文件。
func WriteTrajectoriesJSONL(path string, trajectories []Trajectory) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	w := bufio.NewWriter(f)
	defer func() { _ = w.Flush() }()

	for _, t := range trajectories {
		data, _ := json.Marshal(t)
		_, _ = w.Write(data)
		_ = w.WriteByte('\n')
	}
	return nil
}

// ReadTrajectoriesJSONL 从 JSONL 文件读取轨迹。
func ReadTrajectoriesJSONL(path string) ([]Trajectory, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var results []Trajectory
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var t Trajectory
		if json.Unmarshal(scanner.Bytes(), &t) == nil {
			results = append(results, t)
		}
	}
	if err := scanner.Err(); err != nil {
		return results, pkgerrors.Wrap(pkgerrors.FileIO, "读取 JSONL 文件出错", err)
	}
	return results, nil
}

// convertToTrajectory 将消息转换为 ShareGPT 格式（复用 agent 包的逻辑）。
func convertToTrajectory(messages []llm.Message, model string, completed bool) []TrajectoryTurn {
	var turns []TrajectoryTurn
	for _, msg := range messages {
		role := string(msg.Role)
		switch msg.Role {
		case llm.RoleSystem:
			role = "system"
		case llm.RoleUser:
			role = "human"
		case llm.RoleAssistant:
			role = "gpt"
		case llm.RoleTool:
			role = "tool"
		}

		if len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				turns = append(turns, TrajectoryTurn{
					From:    "gpt",
					Value:   fmt.Sprintf("<tool_call>%s\n%s", tc.Name, tc.Arguments),
					ToolUse: tc.Name,
				})
			}
		} else {
			turns = append(turns, TrajectoryTurn{
				From:  role,
				Value: msg.Content,
			})
		}
	}
	return turns
}
