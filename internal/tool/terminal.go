// Package tool 提供终端命令执行工具。
// TerminalTool 通过沙箱环境执行 shell 命令，支持审批检查。
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"nexus-agent/internal/approval"
	"nexus-agent/internal/sandbox"
)

// ───────────────────────────── 全局终端配置 ─────────────────────────────

// 终端工具的全局环境引用，用于支持运行时注入沙箱环境和审批检查器。
// 在代理初始化时通过 SetTerminalConfig() 设置。
// 使用 atomic.Value 保证并发安全的读写。
var (
	globalTerminalEnv     atomic.Value // stores sandbox.Environment
	globalTerminalChecker atomic.Value // stores *approval.Checker
)

// SetTerminalConfig 设置终端工具的全局沙箱环境和审批检查器。
// 应在代理启动时调用，在所有 TerminalTool 的 Execute 调用之前。
func SetTerminalConfig(env sandbox.Environment, checker *approval.Checker) {
	globalTerminalEnv.Store(env)
	globalTerminalChecker.Store(checker)
}

// ───────────────────────────── 终端工具 ─────────────────────────────

// TerminalTool 实现终端命令执行工具。
// 通过沙箱环境 (本地/Docker/SSH) 执行 shell 命令。
// 命令执行前会通过审批检查器检测危险命令。
type TerminalTool struct {
	defaultTimeout time.Duration
}

// NewTerminalTool 创建终端工具实例。
// env 和 checker 通过 SetTerminalConfig() 全局设置。
func NewTerminalTool() *TerminalTool {
	return &TerminalTool{
		defaultTimeout: 120 * time.Second,
	}
}

// Name 返回工具名称。
func (t *TerminalTool) Name() string { return "terminal" }

// Description 返回工具描述。
func (t *TerminalTool) Description() string {
	return "在当前环境中执行 shell 命令。支持前台和后台执行，支持超时控制和工作目录指定。"
}

// Toolset 返回工具所属工具集。
func (t *TerminalTool) Toolset() string { return "terminal" }

// Emoji 返回工具图标。
func (t *TerminalTool) Emoji() string { return "💻" }

// IsAvailable 检查终端工具是否可用。
// 只要有可用的沙箱环境即为可用。
func (t *TerminalTool) IsAvailable() bool {
	return globalTerminalEnv.Load() != nil
}

// MaxResultChars 返回结果最大字符数。
func (t *TerminalTool) MaxResultChars() int { return 50000 }

// Schema 返回工具的 JSON Schema。
func (t *TerminalTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "terminal",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "要执行的 shell 命令",
				},
				"background": map[string]any{
					"type":        "boolean",
					"description": "是否在后台执行 (长时间运行的任务)",
				},
				"timeout": map[string]any{
					"type":        "integer",
					"description": "命令超时时间 (秒)，默认 120 秒",
				},
				"workdir": map[string]any{
					"type":        "string",
					"description": "工作目录路径，默认使用当前目录",
				},
			},
			"required": []string{"command"},
		},
	}
}

// sanitizeLog 清理日志字符串中的控制字符。
func sanitizeLog(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\r':
			b.WriteString("\\r")
		case '\n':
			b.WriteString("\\n")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}


// Execute 执行终端命令。
// 流程: 审批检查 → 构建执行选项 → sandbox.Execute() → 格式化结果。
func (t *TerminalTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	// 提取命令参数
	command, ok := args["command"].(string)
	if !ok || command == "" {
		return ToolError("参数 command 是必填项且必须为字符串"), nil
	}

	// 审批检查
	if v := globalTerminalChecker.Load(); v != nil {
		checker := v.(*approval.Checker)
		result, reason := checker.Check(ctx, command)
		switch result {
		case approval.Denied:
			slog.Warn("terminal command denied by approval engine", "command", sanitizeLog(command), "reason", reason)
			return ToolError(fmt.Sprintf("命令被拒绝: %s", reason)), nil
		case approval.Pending:
			slog.Warn("terminal command requires user approval", "command", sanitizeLog(command), "reason", reason)
			return ToolError(fmt.Sprintf("命令需要审批: %s。请用户在终端确认后重试。", reason)), nil
		}
	}

	envVal := globalTerminalEnv.Load()
	if envVal == nil {
		return ToolError("终端环境未配置，请先调用 SetTerminalConfig"), nil
	}
	env := envVal.(sandbox.Environment)

	// 构建执行选项
	opts := &sandbox.ExecuteOptions{}
	if workdir, ok := args["workdir"].(string); ok && workdir != "" {
		opts.CWD = workdir
	}
	if timeoutSec, ok := args["timeout"].(float64); ok && timeoutSec > 0 {
		opts.Timeout = time.Duration(timeoutSec) * time.Second
	} else {
		opts.Timeout = t.defaultTimeout
	}

	// 检查是否后台执行
	background, _ := args["background"].(bool)

	var result *sandbox.ExecuteResult
	var err error

	if background {
		// 后台执行
		handle, bgErr := env.ExecuteBackground(ctx, command, opts)
		if bgErr != nil {
			slog.Error("background command start failed", "command", sanitizeLog(command), "err", bgErr)
			return ToolError(fmt.Sprintf("后台命令启动失败: %v", bgErr)), nil
		}
		// 后台命令返回进程句柄信息
		resultJSON, _ := json.Marshal(map[string]any{
			"output":    fmt.Sprintf("后台进程已启动 (命令: %s). 注意: 后台模式下不会捕获 stdout/stderr，如需检查输出请使用前台模式。", command),
			"exit_code": 0,
			"cwd":       env.CWD(),
			"pid":       fmt.Sprintf("%v", handle),
		})
		return string(resultJSON), nil
	}

	// 前台执行
	result, err = env.Execute(ctx, command, opts)
	if err != nil {
		slog.Error("command execution error", "command", sanitizeLog(command), "err", err)
		return ToolError(fmt.Sprintf("命令执行出错: %v", err)), nil
	}

	// 构建结果 JSON
	output := result.Stdout
	if result.Stderr != "" {
		if output != "" {
			output += "\n"
		}
		output += "[stderr]\n" + result.Stderr
	}

	if result.Interrupted {
		if output != "" {
			output += "\n"
		}
		output += "[命令被中断 (超时或取消)]"
	}

	cwd := result.CWD
	if cwd == "" {
		cwd = env.CWD()
	}

	resultJSON, _ := json.Marshal(map[string]any{
		"output":    output,
		"exit_code": result.ExitCode,
		"cwd":       cwd,
		"duration":  result.Duration.String(),
	})

	return string(resultJSON), nil
}

// ───────────────────────────── init 注册 ─────────────────────────────

func init() {
	GetRegistry().Register(NewTerminalTool())
}
