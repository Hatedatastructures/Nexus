// Package sandbox 提供进程句柄实现。
// 包装 os.Process 实现 ProcessHandle 接口，
// 支持轮询、终止和等待功能。
package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"time"
)

// ───────────────────────────── OS 进程句柄 ─────────────────────────────

// OSProcessHandle 是 ProcessHandle 接口的操作系统进程实现。
// 包装 os/exec.Cmd 和 os.Process，提供后台进程的控制能力。
type OSProcessHandle struct {
	mu        sync.Mutex
	cmd       *exec.Cmd     // 命令对象
	process   *os.Process   // 操作系统进程
	stdoutBuf *bytes.Buffer // 标准输出缓冲区
	stderrBuf *bytes.Buffer // 标准错误缓冲区
	exitCode  *int          // 缓存退出码 (nil = 仍在运行)
	killed    bool          // 是否已主动终止
}

// ───────────────────────────── ProcessHandle 接口实现 ─────────────────────────────

// Poll 检查进程是否已结束。
// 返回退出码的指针 (nil 表示仍在运行)。
// 使用跨平台的 os.Process.Signal(os.Kill) 检查进程是否存在。
func (h *OSProcessHandle) Poll() (*int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.exitCode != nil {
		return h.exitCode, nil
	}

	if h.process == nil {
		return nil, fmt.Errorf("进程句柄无效")
	}

	// 通过等待进程状态检查进程是否已退出 (跨平台)
	// 不能使用 Signal(os.Kill)，因为它会实际发送 SIGKILL 杀死进程
	if h.cmd.ProcessState != nil {
		code := h.cmd.ProcessState.ExitCode()
		h.exitCode = &code
		return h.exitCode, nil
	}

	// 进程仍在运行
	return nil, nil
}

// Kill 终止进程。
// 先尝试优雅终止，超时后强制杀死。
func (h *OSProcessHandle) Kill() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.killed || h.process == nil {
		return nil
	}

	pid := h.process.Pid

	// 使用 os.Process.Kill() (跨平台: Unix=SIGKILL, Windows=TerminateProcess)
	if err := h.process.Kill(); err != nil {
		// 进程可能已经退出
		slog.Debug("process kill failed", "pid", pid, "err", err)
		return fmt.Errorf("终止进程失败: %w", err)
	}

	// 等待进程退出
	time.Sleep(100 * time.Millisecond)

	h.killed = true
	slog.Info("process killed", "pid", pid)
	return nil
}

// Wait 等待进程结束。
// 如果上下文被取消，返回 context.Canceled 错误但不终止进程。
func (h *OSProcessHandle) Wait(ctx context.Context) (int, error) {
	// 先快速轮询检查
	if code, err := h.Poll(); code != nil || err != nil {
		if code != nil {
			return *code, err
		}
		return -1, err
	}

	// 使用 channel 等待
	done := make(chan struct{})
	var exitCode int
	var waitErr error

	go func() {
		state, err := h.process.Wait()
		if err != nil {
			waitErr = err
		}
		if state != nil {
			exitCode = state.ExitCode()
		}
		close(done)
	}()

	select {
	case <-ctx.Done():
		return -1, ctx.Err()
	case <-done:
		h.mu.Lock()
		h.exitCode = &exitCode
		h.mu.Unlock()
		return exitCode, waitErr
	}
}

// Stdout 返回标准输出的读取器。
func (h *OSProcessHandle) Stdout() io.Reader {
	if h.stdoutBuf != nil {
		return bytes.NewReader(h.stdoutBuf.Bytes())
	}
	return bytes.NewReader(nil)
}

// Stderr 返回标准错误的读取器。
func (h *OSProcessHandle) Stderr() io.Reader {
	if h.stderrBuf != nil {
		return bytes.NewReader(h.stderrBuf.Bytes())
	}
	return bytes.NewReader(nil)
}
