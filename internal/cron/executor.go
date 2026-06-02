// Package cron 提供 cron 作业执行器，负责在独立 Agent 实例中运行作业。
package cron

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nexus-agent/internal/agent"
	"nexus-agent/internal/config"
)

// ───────────────────────────── 作业执行器 ─────────────────────────────

// conversationRunner 抽象 agent 对话执行，方便测试时 mock。
type conversationRunner interface {
	runConversation(ctx context.Context, userMessage string, history []any, systemMessage string) (*agent.TurnResult, error)
}

// Executor 负责在独立的 AIAgent 实例中执行 cron 作业。
//
// 每个作业执行时:
//   - 创建受限工具集的独立 agent 实例
//   - 输出保存到 ~/.nexus/cron/output/{job_id}/{timestamp}.md
//   - 不活跃超时: 600 秒
type Executor struct {
	outputDir    string          // 输出目录 (~/.nexus/cron/output)
	inactivityTO time.Duration   // 不活跃超时
	agentConfig  *config.AgentConfig // 代理配置
	runner       conversationRunner  // 可选: 注入的对话执行器 (测试用)
}

// withRunner 注入自定义对话执行器 (测试用)，返回自身以支持链式调用。
func (e *Executor) withRunner(r conversationRunner) *Executor {
	e.runner = r
	return e
}

// NewExecutor 创建作业执行器。
func NewExecutor(outputDir string, agentConfig *config.AgentConfig) *Executor {
	return &Executor{
		outputDir:   outputDir,
		inactivityTO: 600 * time.Second,
		agentConfig: agentConfig,
	}
}

// agentRunner 包装真实 AIAgent 以实现 conversationRunner 接口。
type agentRunner struct {
	agent *agent.AIAgent
}

func (r *agentRunner) runConversation(ctx context.Context, userMessage string, history []any, systemMessage string) (*agent.TurnResult, error) {
	return r.agent.RunConversation(ctx, userMessage, nil, systemMessage)
}

// Execute 执行单个 cron 作业。
//
// 这会创建一个新的 AIAgent 实例，向模型发送作业的提示词，
// 然后将最终响应保存到输出目录。
//
// 返回最终的响应文本和可能的错误。
func (e *Executor) Execute(ctx context.Context, job *Job) error {
	if job == nil {
		return fmt.Errorf("作业不能为 nil")
	}

	slog.Info("Cron: starting job execution",
		"id", job.ID,
		"name", job.Name,
	)

	// 确保输出目录存在 (对 job.ID 进行清理防止路径遍历)
	safeID := sanitizeJobID(job.ID)
	jobOutputDir := filepath.Join(e.outputDir, safeID)
	if err := os.MkdirAll(jobOutputDir, 0700); err != nil {
		return fmt.Errorf("创建输出目录失败: %w", err)
	}

	// 构建执行上下文，设置不活跃超时
	execCtx, cancel := context.WithTimeout(ctx, e.inactivityTO)
	defer cancel()

	// 为作业构建提示词
	prompt := e.buildPrompt(job)

	// 记录开始时间
	startTime := time.Now()
	slog.Info("Cron: job prompt", "id", job.ID, "prompt_preview", truncate(prompt, 100))

	// 创建对话执行器
	var runner conversationRunner
	if e.runner != nil {
		runner = e.runner
	} else {
		sessionAgent := agent.DefaultAgentFromConfig(e.agentConfig)
		runner = &agentRunner{agent: sessionAgent}
	}
	result, err := runner.runConversation(execCtx, prompt, nil, "")
	if err != nil {
		return fmt.Errorf("agent 对话失败: %w", err)
	}

	// 检查是否为静默输出
	if result.FinalResponse == "[SILENT]" {
		slog.Info("Cron: job silent output, skipping delivery", "id", job.ID)
		job.LastRunAt = startTime
		job.LastStatus = "silent"
		return nil
	}

	// 保存输出
	if err := e.saveOutput(job, result.FinalResponse, startTime); err != nil {
		return fmt.Errorf("保存输出失败: %w", err)
	}

	// 更新作业状态
	job.LastRunAt = startTime
	job.LastStatus = "ok"

	slog.Info("Cron: job execution completed",
		"id", job.ID,
		"name", job.Name,
		"duration_ms", time.Since(startTime).Milliseconds(),
	)

	return nil
}

// buildPrompt 为作业构建最终提示词 (添加 cron 执行指令前缀)。
func (e *Executor) buildPrompt(job *Job) string {
	cronHint := "[重要提示: 你正在作为预定 cron 作业运行。\n" +
		"投递: 你的最终回复将被自动发送给用户 — 不要使用 send_message 或尝试自行投递。\n" +
		"只需将你的报告/输出作为最终回复，系统会处理其余部分。\n" +
		"静默: 如果确实没有新的内容需要报告，请只回复 \"[SILENT]\" (不带其他内容) 来抑制投递。\n" +
		"永远不要将 [SILENT] 与内容混合 — 要么正常报告你的发现，要么只回复 [SILENT]。]\n\n"

	return cronHint + job.Prompt
}

// saveOutput 将作业输出保存到磁盘。
func (e *Executor) saveOutput(job *Job, output string, timestamp time.Time) error {
	safeID := sanitizeJobID(job.ID)
	jobOutputDir := filepath.Join(e.outputDir, safeID)
	if err := os.MkdirAll(jobOutputDir, 0700); err != nil {
		return err
	}

	filename := timestamp.Format("2006-01-02_15-04-05") + ".md"
	outputFile := filepath.Join(jobOutputDir, filename)

	// 原子写入
	tmpFile, err := os.CreateTemp(jobOutputDir, ".output_*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.WriteString(output); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return err
	}
	tmpFile.Close()

	if err := os.Rename(tmpPath, outputFile); err != nil {
		os.Remove(tmpPath)
		return err
	}

	os.Chmod(outputFile, 0600)
	return nil
}

// sanitizeJobID 清理作业 ID，防止路径遍历攻击。
// 只保留字母、数字、连字符和下划线。
func sanitizeJobID(id string) string {
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}

// truncate 如果字符串长度超过 limit，则截断并附加 "..."
func truncate(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "..."
}
