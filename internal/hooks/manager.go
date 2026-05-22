package hooks

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// ───────────────────────────── ShellHook 实现 ─────────────────────────────

// ShellHook 是 Hook 接口的 shell 脚本实现。
type ShellHook struct {
	name       string         // hook 名称 (用于日志和 allowlist)
	event      string         // 监听的事件类型
	command    string         // shell 脚本路径
	matcher    *regexp.Regexp // 工具名匹配正则 (nil = 匹配所有)
	timeoutSec int            // 超时秒数
}

// NewShellHook 创建 ShellHook。
func NewShellHook(spec HookSpec) (*ShellHook, error) {
	// 验证事件类型
	if err := ValidateEvent(spec.Event); err != nil {
		return nil, err
	}

	if spec.Command == "" {
		return nil, fmt.Errorf("hook command 不能为空")
	}

	// 编译匹配正则
	matcher, err := CompileMatcher(spec.Matcher)
	if err != nil {
		return nil, err
	}

	// 默认超时
	timeout := spec.TimeoutSec
	if timeout <= 0 {
		timeout = 60
	}
	if timeout > 300 {
		timeout = 300
	}

	return &ShellHook{
		name:       spec.Command, // 默认用 command 作为名称
		event:      spec.Event,
		command:    spec.Command,
		matcher:    matcher,
		timeoutSec: timeout,
	}, nil
}

// Name 返回 hook 名称。
func (h *ShellHook) Name() string { return h.name }

// Event 返回监听的事件类型。
func (h *ShellHook) Event() string { return h.event }

// Command 返回 shell 脚本路径。
func (h *ShellHook) Command() string { return h.command }

// TimeoutSec 返回超时秒数。
func (h *ShellHook) TimeoutSec() int { return h.timeoutSec }

// Match 判断是否匹配给定工具名。
func (h *ShellHook) Match(toolName string) bool {
	if h.matcher == nil {
		return true
	}
	return h.matcher.MatchString(toolName)
}

// Execute 执行 hook 脚本。
// 委托给 ShellExecutor 执行实际的 shell 命令。
func (h *ShellHook) Execute(ctx context.Context, event *HookEvent) (*HookResponse, error) {
	executor := NewShellExecutor()
	return executor.Execute(ctx, h, event)
}

// ───────────────────────────── HookManager 实现 ─────────────────────────────

// HookManager 实现 Manager 接口。
// 管理所有注册的 hook，支持链式执行和 allowlist。
type HookManager struct {
	hooks     []Hook
	allowlist *Allowlist
	mu        sync.RWMutex
}

// NewHookManager 创建 HookManager。
//   - hookDir: allowlist 持久化目录
//   - acceptAll: 自动接受所有 hook
func NewHookManager(hookDir string, acceptAll bool) *HookManager {
	return &HookManager{
		hooks:     make([]Hook, 0),
		allowlist: NewAllowlist(hookDir, acceptAll),
	}
}

// Register 注册一个 hook。
// hook 按注册顺序排列，执行时按注册顺序依次执行。
func (m *HookManager) Register(hook Hook) error {
	if hook == nil {
		return fmt.Errorf("不能注册 nil hook")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.hooks = append(m.hooks, hook)
	slog.Info("Hook 已注册", "name", hook.Name(), "event", hook.Event())
	return nil
}

// ExecutePreHooks 执行所有匹配的 pre_tool_call hook。
//
// 链式语义:
//   - 按注册顺序依次执行
//   - 首个返回 block 的 hook 终止链并返回 (resp, true, nil)
//   - 所有 hook 返回 allow 或 modify 则返回 (nil, false, nil)
//   - hook 执行失败时记录警告并跳过，不终止链
func (m *HookManager) ExecutePreHooks(ctx context.Context, toolName string, input map[string]any) (*HookResponse, bool, error) {
	return m.executeChain(ctx, EventPreToolCall, toolName, input, "")
}

// ExecutePostHooks 执行所有匹配的 post_tool_call hook。
// post hook 链不会被中断，所有匹配的 hook 都会执行。
func (m *HookManager) ExecutePostHooks(ctx context.Context, toolName string, input map[string]any, output string) error {
	_, _, err := m.executeChain(ctx, EventPostToolCall, toolName, input, output)
	return err
}

// executeChain 执行指定事件类型的 hook 链。
//
// 参数:
//   - eventType: 事件类型 (EventPreToolCall / EventPostToolCall)
//   - toolName: 工具名
//   - input: 工具输入
//   - output: 工具输出 (仅 post_tool_call 时有意义)
//
// 返回:
//   - *HookResponse: 最终响应 (pre_tool_call 时可能为 block)
//   - bool: 是否阻止工具调用 (仅 pre_tool_call 有意义)
//   - error: 执行错误
func (m *HookManager) executeChain(ctx context.Context, eventType string, toolName string, input map[string]any, output string) (*HookResponse, bool, error) {
	// 收集匹配的 hooks (读锁)
	m.mu.RLock()
	var matched []Hook
	for _, hook := range m.hooks {
		if hook.Event() != eventType {
			continue
		}
		if !hook.Match(toolName) {
			continue
		}
		matched = append(matched, hook)
	}
	m.mu.RUnlock()

	if len(matched) == 0 {
		return nil, false, nil
	}

	// 获取 cwd
	cwd, _ := os.Getwd()

	// 构建事件
	event := &HookEvent{
		EventName:  eventType,
		ToolName:   toolName,
		ToolInput:  input,
		ToolOutput: output,
		CWD:        cwd,
	}

	// 按注册顺序执行 hook 链
	for _, hook := range matched {
		// 检查 allowlist
		if !m.allowlist.IsAllowed(hook.Name()) {
			// 首次使用: 提示用户 (当前非交互模式默认拒绝)
			if !m.promptAllow(hook.Name()) {
				return &HookResponse{
					Decision: "block",
					Reason:   "hook 未被允许: " + hook.Name(),
				}, eventType == EventPreToolCall, nil
			}
			m.allowlist.Add(hook.Name())
		}

		// 执行 hook
		resp, err := hook.Execute(ctx, event)
		if err != nil {
			slog.Warn("Hook 执行失败，跳过", "name", hook.Name(), "event", eventType, "err", err)
			continue
		}

		// pre_tool_call: block 终止链
		if eventType == EventPreToolCall && resp.IsBlock() {
			slog.Info("Hook 阻止工具调用", "name", hook.Name(), "tool", toolName, "reason", resp.Reason)
			return resp, true, nil
		}

		// post_tool_call 或 allow/modify: 继续执行下一个 hook
		slog.Debug("Hook 执行完成", "name", hook.Name(), "event", eventType, "decision", resp.Decision)
	}

	return nil, false, nil
}

// promptAllow 提示用户是否允许 hook。
// 如果 stdin 是终端 (非管道)，交互式询问用户。
// 否则默认拒绝。
func (m *HookManager) promptAllow(command string) bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}

	// 检查 stdin 是否为终端 (非管道/重定向)
	if fi.Mode()&os.ModeCharDevice == 0 {
		slog.Warn("Hook 需要确认但 stdin 非终端，默认拒绝", "command", command)
		return false
	}

	fmt.Fprintf(os.Stderr, "\n⚠ Hook 确认: 是否允许执行 %q？[y/N] ", command)

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return false
	}

	answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
	return answer == "y" || answer == "yes"
}

// ───────────────────────────── 便捷方法 ─────────────────────────────

// RegisterFromSpecs 从 HookSpec 列表批量注册 hook。
func (m *HookManager) RegisterFromSpecs(specs []HookSpec) error {
	for _, spec := range specs {
		if spec.Command == "" {
			continue
		}
		hook, err := NewShellHook(spec)
		if err != nil {
			return fmt.Errorf("创建 hook 失败 (command=%s): %w", spec.Command, err)
		}
		if err := m.Register(hook); err != nil {
			return err
		}
	}
	return nil
}

// LoadFromDir 从目录加载 hook 配置文件。
// 扫描目录下所有 .yaml 文件，解析为 HookSpec 并注册。
func (m *HookManager) LoadFromDir(dir string) error {
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
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		spec, err := parseHookFile(path)
		if err != nil {
			slog.Warn("解析 hook 文件失败", "path", path, "err", err)
			continue
		}

		hook, err := NewShellHook(spec)
		if err != nil {
			slog.Warn("创建 hook 失败", "path", path, "err", err)
			continue
		}

		if err := m.Register(hook); err != nil {
			slog.Warn("注册 hook 失败", "path", path, "err", err)
		}
	}

	return nil
}

// AllowlistRef 返回底层 Allowlist 引用，供外部直接操作。
func (m *HookManager) AllowlistRef() *Allowlist {
	return m.allowlist
}

// ───────────────────────────── YAML 解析 ─────────────────────────────

// parseHookFile 简单解析 YAML 格式的 hook 配置文件。
// 手动提取 event / command / matcher / timeout 字段。
func parseHookFile(path string) (HookSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return HookSpec{}, err
	}

	var spec HookSpec
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
