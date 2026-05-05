// Package testutil 提供测试用的模拟对象和辅助工具。
// 本文件实现 sandbox.Environment 的假实现，支持预设命令输出和可配置工作目录。
package testutil

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"nexus-agent/internal/sandbox"
)

// ───────────────────────────── FakeEnvironment ─────────────────────────────

// FakeEnvironment 是 sandbox.Environment 的假实现。
// 通过预设命令输出映射来控制测试行为。
type FakeEnvironment struct {
	mu sync.Mutex

	// ── 配置字段 ──

	// cwd 当前工作目录。
	cwd string

	// CommandOutputs 预设的命令输出映射。
	// 键为命令前缀 (支持前缀匹配)，值为要返回的结果。
	CommandOutputs []CommandOutput

	// DefaultResult 当命令不匹配任何预设时的默认结果。
	DefaultResult *sandbox.ExecuteResult

	// ExecuteError 统一的执行错误 (如果设置，所有命令都返回此错误)。
	ExecuteError error

	// BackgroundHandle 预设的后台进程句柄。
	BackgroundHandle sandbox.ProcessHandle

	// BackgroundError 预设的后台执行错误。
	BackgroundError error

	// CleanupError 预设的清理错误。
	CleanupError error

	// ── 记录字段 (用于断言) ──

	// ExecutedCommands 记录所有执行过的命令。
	ExecutedCommands []RecordedCommand

	// CleanupCalled 标记 Cleanup 是否被调用。
	CleanupCalled bool
}

// CommandOutput 定义命令匹配规则和对应的输出。
type CommandOutput struct {
	// Prefix 命令前缀 (支持前缀匹配)。
	Prefix string

	// Result 当命令匹配时返回的结果。
	Result *sandbox.ExecuteResult

	// Error 当命令匹配时返回的错误。
	Error error
}

// RecordedCommand 记录一条已执行的命令。
type RecordedCommand struct {
	Command string
	Opts    *sandbox.ExecuteOptions
}

// ───────────────────────────── Environment 接口实现 ─────────────────────────────

// Execute 在假环境中执行命令。
func (f *FakeEnvironment) Execute(ctx context.Context, command string, opts *sandbox.ExecuteOptions) (*sandbox.ExecuteResult, error) {
	f.mu.Lock()
	f.ExecutedCommands = append(f.ExecutedCommands, RecordedCommand{
		Command: command,
		Opts:    opts,
	})
	f.mu.Unlock()

	if f.ExecuteError != nil {
		return nil, f.ExecuteError
	}

	// 尝试匹配预设命令输出
	for _, co := range f.CommandOutputs {
		if strings.HasPrefix(strings.TrimSpace(command), co.Prefix) {
			if co.Error != nil {
				return nil, co.Error
			}
			return co.Result, nil
		}
	}

	// 返回默认结果
	if f.DefaultResult != nil {
		return f.DefaultResult, nil
	}

	return &sandbox.ExecuteResult{
		Stdout:   "",
		Stderr:   "",
		ExitCode: 0,
		Duration: 0,
	}, nil
}

// ExecuteBackground 在后台执行命令。
func (f *FakeEnvironment) ExecuteBackground(ctx context.Context, command string, opts *sandbox.ExecuteOptions) (sandbox.ProcessHandle, error) {
	f.mu.Lock()
	f.ExecutedCommands = append(f.ExecutedCommands, RecordedCommand{
		Command: command,
		Opts:    opts,
	})
	f.mu.Unlock()

	if f.BackgroundError != nil {
		return nil, f.BackgroundError
	}

	if f.BackgroundHandle != nil {
		return f.BackgroundHandle, nil
	}

	return &FakeProcessHandle{}, nil
}

// CWD 返回当前工作目录。
func (f *FakeEnvironment) CWD() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cwd == "" {
		return "/tmp"
	}
	return f.cwd
}

// UpdateCWD 更新当前工作目录。
func (f *FakeEnvironment) UpdateCWD(cwd string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cwd = cwd
}

// Cleanup 清理环境资源。
func (f *FakeEnvironment) Cleanup() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.CleanupCalled = true
	return f.CleanupError
}

// ───────────────────────────── 辅助方法 ─────────────────────────────

// SetCommandOutput 添加一条命令输出预设。
func (f *FakeEnvironment) SetCommandOutput(prefix string, result *sandbox.ExecuteResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.CommandOutputs = append(f.CommandOutputs, CommandOutput{
		Prefix: prefix,
		Result: result,
	})
}

// SetCommandError 添加一条命令错误预设。
func (f *FakeEnvironment) SetCommandError(prefix string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.CommandOutputs = append(f.CommandOutputs, CommandOutput{
		Prefix: prefix,
		Error:  err,
	})
}

// Reset 清空所有记录和预设。
func (f *FakeEnvironment) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ExecutedCommands = nil
	f.CleanupCalled = false
}

// ───────────────────────────── FakeProcessHandle ─────────────────────────────

// FakeProcessHandle 是 sandbox.ProcessHandle 的假实现。
type FakeProcessHandle struct {
	mu       sync.Mutex
	exitCode *int
	killErr  error
	waitErr  error
	stdout   string
	stderr   string
}

// Poll 检查进程是否已结束。
func (h *FakeProcessHandle) Poll() (*int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.exitCode, nil
}

// Kill 强制终止进程。
func (h *FakeProcessHandle) Kill() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	code := 137
	h.exitCode = &code
	return h.killErr
}

// Wait 等待进程结束。
func (h *FakeProcessHandle) Wait(ctx context.Context) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.waitErr != nil {
		return -1, h.waitErr
	}
	if h.exitCode != nil {
		return *h.exitCode, nil
	}
	return 0, nil
}

// Stdout 返回标准输出。
func (h *FakeProcessHandle) Stdout() io.Reader {
	h.mu.Lock()
	defer h.mu.Unlock()
	return strings.NewReader(h.stdout)
}

// Stderr 返回标准错误。
func (h *FakeProcessHandle) Stderr() io.Reader {
	h.mu.Lock()
	defer h.mu.Unlock()
	return strings.NewReader(h.stderr)
}

// ───────────────────────────── 便捷构造函数 ─────────────────────────────

// NewFakeEnvironment 创建一个预配置的 FakeEnvironment。
func NewFakeEnvironment(cwd string) *FakeEnvironment {
	if cwd == "" {
		cwd = "/tmp/test"
	}
	return &FakeEnvironment{
		cwd: cwd,
		DefaultResult: &sandbox.ExecuteResult{
			Stdout:   "",
			ExitCode: 0,
			Duration: time.Millisecond,
		},
	}
}

// NewFakeEnvironmentWithOutputs 创建一个带预设命令输出的 FakeEnvironment。
func NewFakeEnvironmentWithOutputs(cwd string, outputs map[string]string) *FakeEnvironment {
	env := NewFakeEnvironment(cwd)
	for cmd, output := range outputs {
		env.SetCommandOutput(cmd, &sandbox.ExecuteResult{
			Stdout:   output,
			ExitCode: 0,
			Duration: time.Millisecond,
		})
	}
	return env
}

// NewFakeProcessHandle 创建一个假的进程句柄。
func NewFakeProcessHandle(stdout, stderr string, exitCode int) *FakeProcessHandle {
	return &FakeProcessHandle{
		exitCode: &exitCode,
		stdout:   stdout,
		stderr:   stderr,
	}
}

// NewRunningProcessHandle 创建一个仍在运行的假进程句柄 (exitCode 为 nil)。
func NewRunningProcessHandle(stdout, stderr string) *FakeProcessHandle {
	return &FakeProcessHandle{
		stdout: stdout,
		stderr: stderr,
	}
}

// ExecutedCommandsText 返回所有已执行命令的文本列表。
func (f *FakeEnvironment) ExecutedCommandsText() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	cmds := make([]string, len(f.ExecutedCommands))
	for i, c := range f.ExecutedCommands {
		cmds[i] = c.Command
	}
	return cmds
}

// LastCommand 返回最后一条执行的命令。
func (f *FakeEnvironment) LastCommand() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.ExecutedCommands) == 0 {
		return "", fmt.Errorf("没有已执行的命令")
	}
	return f.ExecutedCommands[len(f.ExecutedCommands)-1].Command, nil
}
