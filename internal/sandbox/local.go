// Package sandbox 提供本地子进程执行环境。
// 使用 os/exec 执行 shell 命令，支持进程管理、CWD 跟踪和超时控制。
package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"nexus-agent/internal/approval"
)

// ───────────────────────────── 本地环境 ─────────────────────────────

// LocalEnvironment 是本地子进程沙箱环境的实现。
// 使用当前操作系统的 shell 执行命令。
type LocalEnvironment struct {
	mu             sync.Mutex
	cwd            string        // 当前工作目录
	shell          string        // shell 路径 (bash/sh/cmd)
	shellFlag      string        // shell 命令标志 (-c 或 /c)
	defaultTimeout time.Duration // 默认命令超时
}

// NewLocalEnvironment 创建本地执行环境。
// 自动检测系统 shell (Unix: bash/sh, Windows: cmd)。
func NewLocalEnvironment(cwd string) *LocalEnvironment {
	shell := "/bin/sh"
	shellFlag := "-c"

	// 检测可用的 shell
	if _, err := exec.LookPath("bash"); err == nil {
		shell = "bash"
	}

	// Windows 检测
	if _, err := exec.LookPath("cmd.exe"); err == nil {
		if _, err := exec.LookPath("bash"); err != nil {
			shell = "cmd"
			shellFlag = "/c"
		}
	}

	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	return &LocalEnvironment{
		cwd:            cwd,
		shell:          shell,
		shellFlag:      shellFlag,
		defaultTimeout: 120 * time.Second,
	}
}

// ───────────────────────────── 环境接口实现 ─────────────────────────────

// Execute 在当前环境中执行命令。
func (e *LocalEnvironment) Execute(ctx context.Context, command string, opts *ExecuteOptions) (*ExecuteResult, error) {
	if command == "" {
		return &ExecuteResult{ExitCode: 0, Stdout: ""}, nil
	}

	if opts == nil {
		opts = &ExecuteOptions{}
	}

	// 确定工作目录
	cwd := e.CWD()
	if opts.CWD != "" {
		cwd = opts.CWD
	}

	// 确定超时
	timeout := e.defaultTimeout
	if opts.Timeout > 0 {
		timeout = opts.Timeout
	}

	// 创建带超时的 context
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 在命令末尾添加 CWD 标记输出
	fullCommand := e.wrapCommand(command)

	// 构建命令
	cmd := exec.CommandContext(execCtx, e.shell, e.shellFlag, fullCommand)
	cmd.Dir = cwd

	// 设置进程属性 (平台相关)
	setSysProcAttr(cmd)

	// 环境变量
	cmd.Env = os.Environ()
	for k, v := range opts.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// 登录 shell 模式
	if opts.Login {
		if e.shell == "bash" {
			cmd.Args = []string{e.shell, "-l", e.shellFlag, fullCommand}
		}
	}

	// Sudo 模式 — 需要用户审批
	if opts.Sudo {
		checker := approval.NewChecker("smart", nil, nil)
		result, reason := checker.Check(ctx, command)
		if result != approval.Approved {
			return nil, fmt.Errorf("sudo 权限提升被拒绝: %s", reason)
		}
		cmd.Args = append([]string{"sudo"}, cmd.Args...)
		sudoPath, lookErr := exec.LookPath("sudo")
		if lookErr != nil {
			return nil, fmt.Errorf("sudo 不可用: %w", lookErr)
		}
		cmd.Path = sudoPath
	}

	// 标准输入
	if opts.StdinData != "" {
		cmd.Stdin = strings.NewReader(opts.StdinData)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// 执行
	startTime := time.Now()
	runErr := cmd.Run()
	duration := time.Since(startTime)

	// 构建结果
	result := &ExecuteResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: 0,
		Duration: duration,
	}

	// 提取 CWD 标记
	result.Stdout, result.CWD = e.extractCWDMarker(result.Stdout)

	// 处理错误
	if runErr != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			result.Interrupted = true
			result.ExitCode = ExitCodeTimeout
		} else if exitErr, ok := runErr.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = ExitCodeGeneral
		}
	}

	slog.Debug("command execution completed",
		"cwd", cwd,
		"exitCode", result.ExitCode,
		"duration", duration.String(),
		"stdoutLen", len(result.Stdout),
	)

	return result, nil
}

// ExecuteBackground 在后台执行命令。
func (e *LocalEnvironment) ExecuteBackground(ctx context.Context, command string, opts *ExecuteOptions) (ProcessHandle, error) {
	if opts == nil {
		opts = &ExecuteOptions{}
	}

	cwd := e.CWD()
	if opts.CWD != "" {
		cwd = opts.CWD
	}

	cmd := exec.CommandContext(ctx, e.shell, e.shellFlag, command)
	cmd.Dir = cwd
	setSysProcAttr(cmd)
	cmd.Env = os.Environ()

	handle := &OSProcessHandle{
		cmd:       cmd,
		stdoutBuf: &bytes.Buffer{},
		stderrBuf: &bytes.Buffer{},
	}
	cmd.Stdout = handle.stdoutBuf
	cmd.Stderr = handle.stderrBuf

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("后台进程启动失败: %w", err)
	}
	handle.process = cmd.Process

	slog.Info("background process started", "pid", cmd.Process.Pid, "command", truncateShellCmd(command, 100))
	return handle, nil
}

// CWD 返回当前工作目录。
func (e *LocalEnvironment) CWD() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cwd
}

// UpdateCWD 更新当前工作目录。
func (e *LocalEnvironment) UpdateCWD(cwd string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cwd = cwd
}

// Cleanup 清理环境资源 (本地环境无特殊清理需求)。
func (e *LocalEnvironment) Cleanup() error {
	return nil
}

// ───────────────────────────── CWD 标记 ─────────────────────────────

// cwdMarker 是 CWD 跟踪标记的前缀字符串。
const cwdMarker = "__NEXUS_CWD__:"

// wrapCommand 在命令末尾添加 CWD 提取命令。
func (e *LocalEnvironment) wrapCommand(command string) string {
	// 在命令后添加 CWD 输出
	cwdCmd := fmt.Sprintf("echo %s$(pwd)", cwdMarker)
	return command + "\n" + cwdCmd
}

// extractCWDMarker 从输出中提取 CWD 标记行。
func (e *LocalEnvironment) extractCWDMarker(output string) (string, string) {
	lines := strings.Split(output, "\n")
	var cleanLines []string
	var extractedCWD string

	for _, line := range lines {
		if strings.HasPrefix(line, cwdMarker) {
			cwd := strings.TrimPrefix(line, cwdMarker)
			cwd = strings.TrimSpace(cwd)
			if cwd != "" {
				extractedCWD = cwd
				// 更新环境 CWD
				e.UpdateCWD(cwd)
			}
			continue
		}
		cleanLines = append(cleanLines, line)
	}

	return strings.Join(cleanLines, "\n"), extractedCWD
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

// truncateShellCmd 截断命令字符串用于日志。
// 按 rune 截断以避免在多字节 UTF-8 字符中间切断。
func truncateShellCmd(cmd string, maxLen int) string {
	runes := []rune(cmd)
	if len(runes) <= maxLen {
		return cmd
	}
	return string(runes[:maxLen]) + "..."
}
