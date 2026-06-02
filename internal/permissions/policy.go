package permissions

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// regexCache 缓存已编译的正则表达式，避免每次调用都重新编译。
var (
	regexCache   = make(map[string]*regexp.Regexp)
	regexCacheMu sync.RWMutex
)

// getCompiledRegex 返回缓存编译后的正则表达式。
func getCompiledRegex(pat string) (*regexp.Regexp, error) {
	regexCacheMu.RLock()
	re, ok := regexCache[pat]
	regexCacheMu.RUnlock()
	if ok {
		return re, nil
	}

	regexCacheMu.Lock()
	defer regexCacheMu.Unlock()
	// 双重检查
	if re, ok = regexCache[pat]; ok {
		return re, nil
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		return nil, err
	}
	regexCache[pat] = re
	return re, nil
}

// ───────────────────────────── 策略规则 ─────────────────────────────

// Rule 表示一条权限规则。
// 包含工具名 glob 模式、参数匹配模式和对应的权限级别。
type Rule struct {
	// ToolPattern 工具名 glob 模式，支持 * 和 ? 通配符。
	// 例如: "terminal", "file_*", "browser_*", "*"
	ToolPattern string `yaml:"tool"`

	// ArgPatterns 参数匹配模式列表，格式为 "key=value" 或 "key~=regex"。
	// 空列表表示不检查参数，只匹配工具名。
	// 所有参数模式必须同时满足 (AND 逻辑)。
	ArgPatterns []string `yaml:"args,omitempty"`

	// Level 此规则对应的权限级别。
	Level Level `yaml:"level"`

	// Reason 规则说明，用于审计日志和用户提示。
	Reason string `yaml:"reason,omitempty"`
}

// Match 检查工具调用是否匹配此规则。
// toolName 是工具名称，args 是工具参数。
// 返回是否匹配。
func (r *Rule) Match(toolName string, args map[string]any) bool {
	// 匹配工具名 (glob 模式)
	matched, err := filepath.Match(r.ToolPattern, toolName)
	if err != nil {
		// 无效 glob 模式: 不匹配，让默认策略 (AskAlways) 兜底
		slog.Warn("permission rule glob pattern invalid, skipping", "pattern", r.ToolPattern, "err", err)
		return false
	}
	if !matched {
		return false
	}

	// 如果没有参数模式，仅工具名匹配即为命中
	if len(r.ArgPatterns) == 0 {
		return true
	}

	// 检查所有参数模式 (AND 逻辑)
	for _, ap := range r.ArgPatterns {
		if !matchArgPattern(ap, args) {
			return false
		}
	}

	return true
}

// ───────────────────────────── 参数匹配 ─────────────────────────────

// matchArgPattern 检查单个参数模式是否匹配。
// 支持的格式:
//
//	"key=value"    — 精确匹配
//	"key~=regex"   — 正则匹配
//	"key!=value"   — 不等于
//	"key"          — 参数存在性检查
func matchArgPattern(pattern string, args map[string]any) bool {
	// 解析模式: key~=regex (正则匹配)
	if idx := strings.Index(pattern, "~="); idx > 0 {
		key := strings.TrimSpace(pattern[:idx])
		pat := strings.TrimSpace(pattern[idx+2:])
		val, ok := args[key]
		if !ok {
			return false
		}
		s := fmt.Sprintf("%v", val)
		re, err := getCompiledRegex(pat)
		if err != nil {
			return false
		}
		return re.MatchString(s)
	}

	// 解析模式: key!=value (不等于)
	if idx := strings.Index(pattern, "!="); idx > 0 {
		key := strings.TrimSpace(pattern[:idx])
		val := strings.TrimSpace(pattern[idx+2:])
		actual, ok := args[key]
		if !ok {
			return true // 参数不存在，视为"不等于"
		}
		return fmt.Sprintf("%v", actual) != val
	}

	// 解析模式: key=value (精确匹配)
	if idx := strings.Index(pattern, "="); idx > 0 {
		key := strings.TrimSpace(pattern[:idx])
		val := strings.TrimSpace(pattern[idx+1:])
		actual, ok := args[key]
		if !ok {
			return false
		}
		return fmt.Sprintf("%v", actual) == val
	}

	// 仅 key: 参数存在性检查
	key := strings.TrimSpace(pattern)
	_, exists := args[key]
	return exists
}

// ───────────────────────────── 策略 ─────────────────────────────

// Policy 是权限策略引擎。
// 按规则顺序匹配，首个命中生效。
type Policy struct {
	// Name 策略名称，用于日志和审计。
	Name string `yaml:"name"`

	// Description 策略描述。
	Description string `yaml:"description,omitempty"`

	// Rules 按优先级排序的规则列表 (索引越小优先级越高)。
	Rules []Rule `yaml:"rules"`

	// Default 当没有规则命中时的默认权限级别。
	Default Level `yaml:"default"`

	// DefaultReason 默认级别的原因描述。
	DefaultReason string `yaml:"default_reason,omitempty"`
}

// Evaluate 评估工具调用的权限。
// 按规则顺序匹配，首个命中生效。
// 无规则命中时返回默认级别。
func (p *Policy) Evaluate(toolName string, args map[string]any) Decision {
	for i, rule := range p.Rules {
		if rule.Match(toolName, args) {
			reason := rule.Reason
			if reason == "" {
				reason = fmt.Sprintf("匹配规则 #%d: %s", i, rule.ToolPattern)
			}
			slog.Debug("permission rule matched",
				"policy", p.Name,
				"rule_idx", i,
				"tool", toolName,
				"level", rule.Level,
				"reason", reason,
			)
			return Decision{
				Level:   rule.Level,
				Reason:  reason,
				Matched: fmt.Sprintf("规则 #%d (%s)", i, rule.ToolPattern),
				RuleIdx: i,
			}
		}
	}

	// 无规则命中，返回默认级别
	reason := p.DefaultReason
	if reason == "" {
		reason = "未匹配任何规则，使用默认策略"
	}
	return Decision{
		Level:   p.Default,
		Reason:  reason,
		Matched: "默认策略",
		RuleIdx: -1,
	}
}

// ───────────────────────────── 默认策略工厂 ─────────────────────────────

// DefaultPolicy 返回内置的默认策略。
// 规则设计:
//   - 只读工具 (web_search, memory, vision 等) = AutoAllow
//   - 文件读取工具 = AutoAllow
//   - 写入工具 (file_write, terminal 等) = AskOnce
//   - 危险工具 (file_delete 等) = AskAlways
//   - 浏览器工具 = AskOnce
//   - 其他未知工具 = AskAlways (安全兜底)
func DefaultPolicy() *Policy {
	return &Policy{
		Name:        "default",
		Description: "内置默认策略: 只读=放行, 写入=询问一次, 危险=每次询问",
		Rules: []Rule{
			// ── 硬封锁: 绝对禁止的操作 (每条规则独立，使用 OR 语义) ──
			{
				ToolPattern: "terminal",
				ArgPatterns: []string{"command~=rm\\s+(-[a-zA-Z]*f[a-zA-Z]*\\s+)?/"},
				Level:       LevelAutoDeny,
				Reason:      "禁止执行 rm -rf /",
			},
			{
				ToolPattern: "terminal",
				ArgPatterns: []string{"command~=mkfs"},
				Level:       LevelAutoDeny,
				Reason:      "禁止执行 mkfs",
			},
			{
				ToolPattern: "terminal",
				ArgPatterns: []string{"command~=dd\\s+.*of=/dev/"},
				Level:       LevelAutoDeny,
				Reason:      "禁止执行 dd 写设备",
			},
			// ── 自动放行: 只读/查询工具 ──
			{
				ToolPattern: "web_search",
				Level:       LevelAutoAllow,
				Reason:      "网页搜索为只读操作",
			},
			{
				ToolPattern: "memory_*",
				Level:       LevelAutoAllow,
				Reason:      "记忆读取为只读操作",
			},
			{
				ToolPattern: "vision",
				Level:       LevelAutoAllow,
				Reason:      "图像分析为只读操作",
			},
			{
				ToolPattern: "session_search",
				Level:       LevelAutoAllow,
				Reason:      "会话搜索为只读操作",
			},
			{
				ToolPattern: "todo",
				Level:       LevelAutoAllow,
				Reason:      "待办事项读取为只读操作",
			},
			// ── 询问一次: 中等风险写操作 ──
			{
				ToolPattern: "file_read",
				Level:       LevelAutoAllow,
				Reason:      "文件读取为只读操作",
			},
			{
				ToolPattern: "file_write",
				Level:       LevelAskOnce,
				Reason:      "文件写入需要确认",
			},
			{
				ToolPattern: "file_edit",
				Level:       LevelAskOnce,
				Reason:      "文件编辑需要确认",
			},
			{
				ToolPattern: "patch",
				Level:       LevelAskOnce,
				Reason:      "代码补丁需要确认",
			},
			{
				ToolPattern: "browser_*",
				Level:       LevelAskOnce,
				Reason:      "浏览器操作需要确认",
			},
			{
				ToolPattern: "web_*",
				Level:       LevelAskOnce,
				Reason:      "网络操作需要确认",
			},
			// ── 每次询问: 高风险操作 ──
			{
				ToolPattern: "terminal",
				Level:       LevelAskAlways,
				Reason:      "终端命令为高风险操作",
			},
			{
				ToolPattern: "file_delete",
				Level:       LevelAskAlways,
				Reason:      "文件删除为高风险操作",
			},
			{
				ToolPattern: "delegate",
				Level:       LevelAskAlways,
				Reason:      "任务委派为高风险操作",
			},
		},
		// 兜底: 未知工具默认每次询问
		Default:      LevelAskAlways,
		DefaultReason: "未知工具默认需要每次确认",
	}
}
