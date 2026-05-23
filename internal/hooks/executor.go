package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// ───────────────────────────── Shell 执行引擎 ─────────────────────────────

// ShellExecutor 负责执行 shell hook 脚本。
// 使用 JSON stdin/stdout wire protocol 与脚本通信。
type ShellExecutor struct{}

// NewShellExecutor 创建 Shell 执行引擎。
func NewShellExecutor() *ShellExecutor {
	return &ShellExecutor{}
}

// Execute 执行单个 shell hook 脚本。
// 将 HookEvent 序列化为 JSON 写入 stdin，从 stdout 解析 HookResponse。
//
// Wire Protocol:
//   - stdin:  HookEvent JSON
//   - stdout: HookResponse JSON
func (e *ShellExecutor) Execute(ctx context.Context, hook *ShellHook, event *HookEvent) (*HookResponse, error) {
	timeout := time.Duration(hook.TimeoutSec()) * time.Second
	hookCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(hookCtx, hook.Command())

	// 将事件序列化为 JSON 写入 stdin
	eventJSON, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("序列化 hook 事件失败: %w", err)
	}
	cmd.Stdin = strings.NewReader(string(eventJSON))

	// 捕获 stdout
	stdout, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("hook 脚本执行失败: %w", err)
	}

	// 解析响应
	resp, err := parseResponse(stdout)
	if err != nil {
		slog.Warn("failed to parse hook response, defaulting to allow", "command", hook.Command(), "err", err)
		return &HookResponse{Decision: "allow"}, nil
	}

	// 补充默认 reason
	if resp.Decision == "block" && resp.Reason == "" {
		resp.Reason = "被 shell hook 阻止"
	}

	return resp, nil
}

// parseResponse 解析 hook 脚本的 stdout 输出。
// 兼容两种格式: 完整 HookResponse 和精简 {"decision": "..."} 格式。
func parseResponse(data []byte) (*HookResponse, error) {
	// 优先尝试完整格式
	var resp HookResponse
	if err := json.Unmarshal(data, &resp); err == nil && resp.Decision != "" {
		return &resp, nil
	}

	// 回退到精简格式 (Claude Code 兼容)
	var minimal struct {
		Decision string `json:"decision"`
	}
	if err := json.Unmarshal(data, &minimal); err != nil {
		return nil, fmt.Errorf("无法解析 hook 响应: %w", err)
	}
	if minimal.Decision == "" {
		return nil, fmt.Errorf("hook 响应缺少 decision 字段")
	}

	return &HookResponse{Decision: minimal.Decision}, nil
}
