// Package sandbox 提供 SSH 远程执行环境。
// 通过 ssh 子进程在远程主机上执行命令，
// 适用于远程开发、集群管理等场景。
package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ───────────────────────────── SSH 环境 ─────────────────────────────

// SSHEnvironment 是远程 SSH 主机的执行环境。
// 通过 ssh 子进程在远程主机上执行命令。
type SSHEnvironment struct {
	mu             sync.Mutex
	cwd            string        // 远程主机当前工作目录
	host           string        // 远程主机地址 (user@host)
	sshArgs        []string      // 额外的 ssh 参数
	defaultTimeout time.Duration // 默认命令超时
}

// NewSSHEnvironment 创建 SSH 远程执行环境。
// host 格式: "user@host" 或 "host"。
// cwd 是远程主机的初始工作目录。
func NewSSHEnvironment(host, cwd string, extraArgs ...string) *SSHEnvironment {
	if cwd == "" {
		cwd = "~"
	}
	return &SSHEnvironment{
		cwd:            cwd,
		host:           host,
		sshArgs:        extraArgs,
		defaultTimeout: 120 * time.Second,
	}
}

// ───────────────────────────── 环境接口实现 ─────────────────────────────

// Execute 在远程主机上执行命令。
func (e *SSHEnvironment) Execute(ctx context.Context, command string, opts *ExecuteOptions) (*ExecuteResult, error) {
	if command == "" {
		return &ExecuteResult{ExitCode: 0, Stdout: ""}, nil
	}

	if opts == nil {
		opts = &ExecuteOptions{}
	}

	timeout := e.defaultTimeout
	if opts.Timeout > 0 {
		timeout = opts.Timeout
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 构建 ssh 命令
	sshArgs := append([]string{}, e.sshArgs...)
	sshArgs = append(sshArgs, e.host)

	// 如果指定了 CWD，先 cd 再执行
	fullCommand := shellQuote(command)
	if opts.CWD != "" {
		fullCommand = fmt.Sprintf("cd %s && %s", shellQuote(opts.CWD), shellQuote(command))
	} else if e.cwd != "" && e.cwd != "~" {
		fullCommand = fmt.Sprintf("cd %s && %s", shellQuote(e.cwd), shellQuote(command))
	}

	sshArgs = append(sshArgs, fullCommand)

	cmd := exec.CommandContext(execCtx, "ssh", sshArgs...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if opts.StdinData != "" {
		cmd.Stdin = strings.NewReader(opts.StdinData)
	}

	startTime := time.Now()
	runErr := cmd.Run()
	duration := time.Since(startTime)

	result := &ExecuteResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: 0,
		Duration: duration,
	}

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

	// SSH 环境无法直接跟踪远程 CWD，保持原值
	if opts.CWD != "" {
		result.CWD = opts.CWD
	} else {
		result.CWD = e.cwd
	}

	slog.Debug("SSH command execution completed",
		"host", e.host,
		"exitCode", result.ExitCode,
		"duration", duration.String(),
	)

	return result, nil
}

// ExecuteBackground 在远程主机后台执行命令。
func (e *SSHEnvironment) ExecuteBackground(ctx context.Context, command string, opts *ExecuteOptions) (ProcessHandle, error) {
	// SSH 后台执行: 使用 nohup 并在末尾加 &
	bgCommand := fmt.Sprintf("nohup %s > /dev/null 2>&1 &", shellQuote(command))

	sshArgs := append([]string{}, e.sshArgs...)
	sshArgs = append(sshArgs, e.host, bgCommand)

	cmd := exec.CommandContext(ctx, "ssh", sshArgs...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("SSH 后台进程启动失败: %w", err)
	}

	handle := &OSProcessHandle{
		cmd:    cmd,
		process: cmd.Process,
	}

	slog.Info("SSH background process started", "host", e.host)
	return handle, nil
}

// CWD 返回远程主机的当前工作目录。
func (e *SSHEnvironment) CWD() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cwd
}

// UpdateCWD 更新远程主机的工作目录。
func (e *SSHEnvironment) UpdateCWD(cwd string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cwd = cwd
}

// Cleanup 清理 SSH 连接资源。
func (e *SSHEnvironment) Cleanup() error {
	return nil
}

// shellQuote 对 shell 参数进行简单引号包裹。
// 用于防止路径中的空格和特殊字符问题。
func shellQuote(s string) string {
	// 使用单引号包裹，内部单引号用 '\'' 转义
	// 这是最安全的 shell 转义方式，处理所有特殊字符
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
