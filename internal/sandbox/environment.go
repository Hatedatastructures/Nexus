// Package sandbox 提供终端命令执行的沙箱环境抽象。
// 支持多种后端: 本地子进程、Docker 容器、SSH 远程主机等。
// 所有环境通过统一的 Environment 接口访问。
package sandbox

import (
	"context"
	"io"
	"time"
)

// ───────────────────────────── 环境接口 ─────────────────────────────

// Environment 是终端命令执行的抽象接口。
// 每个会话使用一个环境实例，跟踪当前工作目录。
type Environment interface {
	// Execute 在当前环境中执行命令。
	// command 是要执行的 shell 命令。
	// opts 包含执行选项 (工作目录、超时、环境变量、标准输入数据)。
	// 返回包含标准输出、标准错误和退出码的结果。
	// 如果命令超时或上下文被取消，返回的 ExecuteResult.Interrupted 为 true。
	Execute(ctx context.Context, command string, opts *ExecuteOptions) (*ExecuteResult, error)

	// ExecuteBackground 在后台执行命令，返回可查询/终止的进程句柄。
	// 用于长时间运行的任务 (如 IDE、Web 服务器)。
	ExecuteBackground(ctx context.Context, command string, opts *ExecuteOptions) (ProcessHandle, error)

	// CWD 返回环境的当前工作目录。
	CWD() string

	// UpdateCWD 更新环境的当前工作目录。
	// 通常在命令完成后从 stdout 的标记行中提取新 CWD。
	UpdateCWD(cwd string)

	// Cleanup 清理环境占用的所有资源。
	// 包括终止子进程、关闭连接、删除临时文件等。
	// 在会话结束时调用。
	Cleanup() error
}

// ───────────────────────────── 执行选项 ─────────────────────────────

// ExecuteOptions 定义命令执行的配置选项
type ExecuteOptions struct {
	CWD       string            // 工作目录 (空 = 使用环境当前目录)
	Timeout   time.Duration     // 命令超时时间 (0 = 使用环境默认值)
	Env       map[string]string // 额外环境变量
	StdinData string            // 标准输入数据 (空 = 无输入)
	Login     bool              // 是否以登录 shell 模式运行
	Sudo      bool              // 是否需要 sudo
}

// ───────────────────────────── 执行结果 ─────────────────────────────

// ExecuteResult 包含命令执行的结果
type ExecuteResult struct {
	Stdout      string        // 标准输出内容
	Stderr      string        // 标准错误内容
	ExitCode    int           // 进程退出码
	Duration    time.Duration // 实际执行时长
	Interrupted bool          // 是否被中断 (超时或上下文取消)
	CWD         string        // 命令执行后的工作目录 (从标记行提取)
}

// ───────────────────────────── 进程句柄 ─────────────────────────────

// ProcessHandle 表示一个后台运行的进程。
// 调用者可以查询状态、获取输出、或终止进程。
type ProcessHandle interface {
	// Poll 检查进程是否已结束。返回退出码的指针 (nil = 仍在运行)。
	Poll() (*int, error)

	// Kill 强制终止进程 (发送 SIGKILL)。
	Kill() error

	// Wait 等待进程结束并返回退出码。
	// 如果上下文被取消，返回 context.Canceled 错误但不终止进程。
	Wait(ctx context.Context) (int, error)

	// Stdout 返回标准输出的只读管道。
	Stdout() io.Reader

	// Stderr 返回标准错误的只读管道。
	Stderr() io.Reader
}

// ───────────────────────────── 常用退出码 ─────────────────────────────

const (
	ExitCodeSuccess = 0   // 成功
	ExitCodeGeneral = 1   // 一般错误
	ExitCodeTimeout = 124 // 超时 (被 timeout 命令终止)
	ExitCodeSignal  = 128 // 被信号终止基数
	ExitCodeSIGTERM = 143 // 128 + 15 (SIGTERM)
	ExitCodeSIGKILL = 137 // 128 + 9  (SIGKILL)
)
