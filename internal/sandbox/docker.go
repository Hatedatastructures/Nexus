// Package sandbox 提供 Docker 容器执行环境。
// 通过 "docker exec" 在容器中执行命令，
// 提供与宿主机隔离的执行环境。
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

// ───────────────────────────── Docker 环境 ─────────────────────────────

// DockerEnvironment 是 Docker 容器内的执行环境。
// 通过 docker exec 在指定容器中执行命令。
type DockerEnvironment struct {
	mu             sync.Mutex
	cwd            string        // 容器内当前工作目录
	containerID    string        // Docker 容器 ID 或名称
	defaultTimeout time.Duration // 默认命令超时
	// 安全加固参数
	securityOpts []string // Docker 安全选项
	pidsLimit    int      // 容器内进程数限制
	tmpfsSizeMB  int      // /tmp 挂载大小 (MB)
}

// dockerDefaultSecurityArgs 返回默认的安全加固参数。
// 参照 Python 原版的 _SECURITY_ARGS:
//   - 移除所有默认 capability，仅保留必要的
//   - 禁止新权限提升
//   - 限制 PID 数量
//   - /tmp 使用 tmpfs 防止持久化
var dockerDefaultSecurityArgs = []string{
	"--cap-drop", "ALL",
	"--cap-add", "DAC_OVERRIDE",
	"--cap-add", "CHOWN",
	"--cap-add", "SETUID",
	"--cap-add", "SETGID",
	"--security-opt", "no-new-privileges",
}

// DockerSecurityOptions 定义 Docker 容器的安全加固选项。
type DockerSecurityOptions struct {
	CapDrop     []string // 要移除的 capabilities
	CapAdd      []string // 要添加的 capabilities
	SecurityOpt []string // 安全选项
	PIDsLimit   int      // 进程数限制
	NoNewPrivs  bool     // 禁止新权限提升
	TmpfsSizeMB int      // /tmp tmpfs 大小 (MB)
	NetworkNone bool     // 禁用网络 (--network=none)
}

// DefaultDockerSecurity 返回默认的安全加固配置。
func DefaultDockerSecurity() *DockerSecurityOptions {
	return &DockerSecurityOptions{
		CapDrop:     []string{"ALL"},
		CapAdd:      []string{"DAC_OVERRIDE", "CHOWN", "SETUID", "SETGID"},
		SecurityOpt: []string{"no-new-privileges"},
		PIDsLimit:   256,
		NoNewPrivs:  true,
		TmpfsSizeMB: 512,
		NetworkNone: true,
	}
}

// NewDockerEnvironment 创建 Docker 执行环境。
// containerID 是目标容器的 ID 或名称。
// cwd 是容器内的初始工作目录。
// sec 是安全加固选项 (nil 时使用默认值)。
func NewDockerEnvironment(containerID, cwd string, sec *DockerSecurityOptions) *DockerEnvironment {
	if cwd == "" {
		cwd = "/workspace"
	}

	e := &DockerEnvironment{
		cwd:            cwd,
		containerID:    containerID,
		defaultTimeout: 120 * time.Second,
	}

	if sec == nil {
		sec = DefaultDockerSecurity()
	}
	e.pidsLimit = sec.PIDsLimit
	e.tmpfsSizeMB = sec.TmpfsSizeMB

	// 构建安全选项列表
	e.securityOpts = make([]string, 0)
	if len(sec.CapDrop) > 0 {
		e.securityOpts = append(e.securityOpts, "--cap-drop")
		e.securityOpts = append(e.securityOpts, sec.CapDrop...)
	}
	if len(sec.CapAdd) > 0 {
		e.securityOpts = append(e.securityOpts, "--cap-add")
		e.securityOpts = append(e.securityOpts, sec.CapAdd...)
	}
	if len(sec.SecurityOpt) > 0 {
		e.securityOpts = append(e.securityOpts, "--security-opt")
		e.securityOpts = append(e.securityOpts, sec.SecurityOpt...)
	}
	if sec.PIDsLimit > 0 {
		e.securityOpts = append(e.securityOpts, "--pids-limit", fmt.Sprintf("%d", sec.PIDsLimit))
	}
	if sec.TmpfsSizeMB > 0 {
		e.securityOpts = append(e.securityOpts, "--tmpfs", fmt.Sprintf("/tmp:rw,nosuid,size=%dm", sec.TmpfsSizeMB))
	}
	if sec.NetworkNone {
		e.securityOpts = append(e.securityOpts, "--network", "none")
	}

	return e
}

// ───────────────────────────── 环境接口实现 ─────────────────────────────

// Execute 在 Docker 容器中执行命令。
func (e *DockerEnvironment) Execute(ctx context.Context, command string, opts *ExecuteOptions) (*ExecuteResult, error) {
	if command == "" {
		return &ExecuteResult{ExitCode: 0, Stdout: ""}, nil
	}

	if opts == nil {
		opts = &ExecuteOptions{}
	}

	cwd := e.CWD()
	if opts.CWD != "" {
		cwd = opts.CWD
	}

	timeout := e.defaultTimeout
	if opts.Timeout > 0 {
		timeout = opts.Timeout
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 构建 docker exec 命令
	// 注意: 安全加固参数 (securityOpts) 仅在 docker run 阶段生效，
	// docker exec 不支持 --cap-drop/--pids-limit/--network 等标志。
	dockerArgs := []string{"exec", "--workdir", cwd}

	// 保持 stdin 打开以支持交互
	dockerArgs = append(dockerArgs, "-i")

	// 环境变量
	for k, v := range opts.Env {
		dockerArgs = append(dockerArgs, "-e", fmt.Sprintf("%s=%s", k, v))
	}

	dockerArgs = append(dockerArgs, e.containerID, "sh", "-c", command)

	cmd := exec.CommandContext(execCtx, "docker", dockerArgs...)

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

	// 更新 CWD (Docker 环境不直接跟踪，保持原值)
	result.CWD = cwd

	slog.Debug("Docker command execution completed",
		"container", e.containerID,
		"exitCode", result.ExitCode,
		"duration", duration.String(),
	)

	return result, nil
}

// ExecuteBackground 在 Docker 容器中后台执行命令。
func (e *DockerEnvironment) ExecuteBackground(ctx context.Context, command string, opts *ExecuteOptions) (ProcessHandle, error) {
	if opts == nil {
		opts = &ExecuteOptions{}
	}

	cwd := e.CWD()
	if opts.CWD != "" {
		cwd = opts.CWD
	}

	// docker exec -d 后台运行
	dockerArgs := []string{"exec", "-d", "--workdir", cwd}
	for k, v := range opts.Env {
		dockerArgs = append(dockerArgs, "-e", fmt.Sprintf("%s=%s", k, v))
	}
	dockerArgs = append(dockerArgs, e.containerID, "sh", "-c", command)

	cmd := exec.CommandContext(ctx, "docker", dockerArgs...)
	cmd.Dir = cwd

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("docker 后台进程启动失败: %w", err)
	}

	handle := &OSProcessHandle{
		cmd:       cmd,
		process:   cmd.Process,
		stdoutBuf: &stdout,
		stderrBuf: &stderr,
	}

	// 提取容器内的进程 ID
	containerPID := strings.TrimSpace(stdout.String())
	slog.Info("Docker background process started", "container", e.containerID, "containerPID", containerPID)
	_ = containerPID

	return handle, nil
}

// CWD 返回容器内的当前工作目录。
func (e *DockerEnvironment) CWD() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cwd
}

// UpdateCWD 更新容器内的当前工作目录。
func (e *DockerEnvironment) UpdateCWD(cwd string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cwd = cwd
}

// SecurityRunArgs 返回应传给 docker run/create 的安全参数。
// docker exec 不支持 --cap-drop/--pids-limit/--network 等标志，
// 必须在容器创建时应用。
func (e *DockerEnvironment) SecurityRunArgs() []string {
	return e.securityOpts
}

// Cleanup 清理 Docker 环境资源。
// 注意: 不停止容器，仅清理本地状态。
func (e *DockerEnvironment) Cleanup() error {
	return nil
}
