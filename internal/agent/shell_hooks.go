// Package agent 提供 Shell Hook 系统。
// 允许用户通过 shell 脚本拦截和控制工具调用行为。
// 使用 JSON stdin/stdout wire protocol 与 hook 脚本通信。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ───────────────────────────── 数据结构 ─────────────────────────────

// ShellHookSpec 定义一个 Shell Hook 的规格。
type ShellHookSpec struct {
	Event      string         `yaml:"event"`      // 事件类型: pre_tool_call / post_tool_call
	Command    string         `yaml:"command"`     // hook 脚本路径
	Matcher    string         `yaml:"matcher"`     // 工具名匹配正则 (空 = 匹配所有)
	TimeoutSec int            `yaml:"timeout"`     // 超时秒数 (默认 60, 最大 300)
	compiled   *regexp.Regexp // 编译后的正则
}

// HookEvent 表示发送给 hook 脚本的事件。
type HookEvent struct {
	EventName  string         `json:"event_name"`
	ToolName   string         `json:"tool_name"`
	ToolInput  map[string]any `json:"tool_input"`
	SessionID  string         `json:"session_id"`
	CWD        string         `json:"cwd"`
}

// HookResponse 表示 hook 脚本的响应。
type HookResponse struct {
	Decision string `json:"decision"` // allow / block / modify
	Reason   string `json:"reason"`   // 阻止原因 (block 时)
	Message  string `json:"message"`  // 替换消息 (modify 时)
}

// ShellHookManager 管理所有 Shell Hook。
type ShellHookManager struct {
	hooks     []ShellHookSpec
	allowlist map[string]bool // 已允许的 hook 命令
	mu        sync.RWMutex
	hookDir   string
	acceptAll bool // 自动接受所有 hook
}

// NewShellHookManager 创建 Shell Hook 管理器。
func NewShellHookManager(hookDir string, acceptAll bool) *ShellHookManager {
	m := &ShellHookManager{
		allowlist: make(map[string]bool),
		hookDir:   hookDir,
		acceptAll: acceptAll,
	}
	m.loadAllowlist()
	return m
}

// ───────────────────────────── 注册与执行 ─────────────────────────────

// RegisterHooks 注册多个 hook 规格。
func (m *ShellHookManager) RegisterHooks(specs []ShellHookSpec) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, spec := range specs {
		if spec.Command == "" {
			continue
		}

		// 编译匹配正则
		if spec.Matcher != "" {
			re, err := regexp.Compile(spec.Matcher)
			if err != nil {
				return fmt.Errorf("编译 hook matcher 失败: %w", err)
			}
			spec.compiled = re
		}

		// 默认超时
		if spec.TimeoutSec <= 0 {
			spec.TimeoutSec = 60
		}
		if spec.TimeoutSec > 300 {
			spec.TimeoutSec = 300
		}

		m.hooks = append(m.hooks, spec)
		slog.Info("Shell Hook 已注册", "event", spec.Event, "command", spec.Command, "matcher", spec.Matcher)
	}

	return nil
}

// LoadFromDir 从目录加载 hook 配置。
func (m *ShellHookManager) LoadFromDir(dir string) error {
	if dir == "" {
		return nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".yaml") {
			// 简单解析: 每个 YAML 文件定义一个 hook
			path := filepath.Join(dir, entry.Name())
			spec, err := parseHookFile(path)
			if err != nil {
				slog.Warn("解析 hook 文件失败", "path", path, "err", err)
				continue
			}
			m.RegisterHooks([]ShellHookSpec{spec})
		}
	}

	return nil
}

// ExecuteHook 执行匹配的 hook。
// 返回 HookResponse 和是否应该阻止工具调用。
func (m *ShellHookManager) ExecuteHook(ctx context.Context, toolName string, toolInput map[string]any, sessionID string) (*HookResponse, bool, error) {
	// 先用读锁收集需要执行的 hooks
	m.mu.RLock()
	var matched []ShellHookSpec
	for _, hook := range m.hooks {
		if hook.Event != "pre_tool_call" {
			continue
		}
		if hook.compiled != nil && !hook.compiled.MatchString(toolName) {
			continue
		}
		matched = append(matched, hook)
	}
	needAllowlistUpdate := false
	allowlistCmd := ""
	m.mu.RUnlock()

	for _, hook := range matched {
		// 检查 allowlist (不持锁)
		m.mu.RLock()
		allowed := m.acceptAll || m.allowlist[hook.Command]
		m.mu.RUnlock()

		if !allowed {
			if !m.promptAllow(hook.Command) {
				return &HookResponse{Decision: "block", Reason: "hook 未被允许"}, true, nil
			}
			needAllowlistUpdate = true
			allowlistCmd = hook.Command
		}

		// 执行 hook
		resp, err := m.runHook(ctx, hook, toolName, toolInput, sessionID)
		if err != nil {
			slog.Warn("Hook 执行失败", "command", hook.Command, "err", err)
			continue
		}

		if resp.Decision == "block" {
			return resp, true, nil
		}
	}

	// 在所有 hook 执行完毕后，用写锁更新 allowlist
	if needAllowlistUpdate {
		m.mu.Lock()
		m.allowlist[allowlistCmd] = true
		m.saveAllowlist()
		m.mu.Unlock()
	}

	return nil, false, nil
}

// runHook 运行单个 hook 脚本。
func (m *ShellHookManager) runHook(ctx context.Context, hook ShellHookSpec, toolName string, toolInput map[string]any, sessionID string) (*HookResponse, error) {
	timeout := time.Duration(hook.TimeoutSec) * time.Second
	hookCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(hookCtx, hook.Command)

	// 准备 stdin JSON
	event := HookEvent{
		EventName: "pre_tool_call",
		ToolName:  toolName,
		ToolInput: toolInput,
		SessionID: sessionID,
		CWD:       getCWD(),
	}
	eventJSON, _ := json.Marshal(event)
	cmd.Stdin = strings.NewReader(string(eventJSON))

	// 捕获 stdout
	stdout, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("hook 执行失败: %w", err)
	}

	// 解析响应
	var resp HookResponse
	if err := json.Unmarshal(stdout, &resp); err != nil {
		// 尝试解析 Claude Code 风格的响应
		var ccResp struct {
			Decision string `json:"decision"`
		}
		if json.Unmarshal(stdout, &ccResp) == nil {
			resp.Decision = ccResp.Decision
		} else {
			return nil, fmt.Errorf("解析 hook 响应失败: %w", err)
		}
	}

	// Claude Code 风格映射
	if resp.Decision == "block" && resp.Reason == "" {
		resp.Reason = "被 shell hook 阻止"
	}

	return &resp, nil
}

// promptAllow 提示用户是否允许 hook（TTY 模式）。
func (m *ShellHookManager) promptAllow(command string) bool {
	if m.acceptAll {
		return true
	}
	// 非交互模式默认拒绝
	return false
}

// ───────────────────────────── 持久化 ─────────────────────────────

const allowlistFile = "shell-hooks-allowlist.json"

func (m *ShellHookManager) saveAllowlist() {
	if m.hookDir == "" {
		return
	}

	path := filepath.Join(m.hookDir, allowlistFile)
	data, _ := json.MarshalIndent(m.allowlist, "", "  ")
	os.WriteFile(path, data, 0600)
}

func (m *ShellHookManager) loadAllowlist() {
	if m.hookDir == "" {
		return
	}

	path := filepath.Join(m.hookDir, allowlistFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	var list map[string]bool
	if json.Unmarshal(data, &list) == nil {
		m.allowlist = list
	}
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

func parseHookFile(path string) (ShellHookSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ShellHookSpec{}, err
	}

	var spec ShellHookSpec
	// 简单 YAML 解析: 手动提取字段
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		value = strings.Trim(value, "\"'")

		switch key {
		case "event":
			spec.Event = value
		case "command":
			spec.Command = value
		case "matcher":
			spec.Matcher = value
		case "timeout":
			fmt.Sscanf(value, "%d", &spec.TimeoutSec)
		}
	}

	return spec, nil
}

func getCWD() string {
	cwd, _ := os.Getwd()
	return cwd
}
