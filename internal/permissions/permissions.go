// Package permissions 提供五级权限管理系统。
// 统一管理所有工具调用的权限控制，替代分散在各工具中的审批逻辑。
// 权限级别从自动放行到人工升级，覆盖完整的安全需求谱系。
package permissions

import "fmt"

// ───────────────────────────── 权限级别 ─────────────────────────────

// Level 表示权限级别，数值越小越宽松。
type Level int

const (
	// LevelAutoAllow 自动放行: 只读操作、安全查询等低风险工具。
	// 不需要用户确认，直接执行。
	LevelAutoAllow Level = 0

	// LevelAutoDeny 自动拒绝: 硬封锁级别，始终拒绝执行。
	// 用于绝对禁止的操作 (如 rm -rf /、格式化磁盘)。
	LevelAutoDeny Level = 1

	// LevelAskOnce 询问一次: 首次调用时询问用户，会话内记住决策。
	// 适用于用户信任但首次需要确认的工具。
	LevelAskOnce Level = 2

	// LevelAskAlways 每次询问: 每次调用都需要用户确认。
	// 适用于高风险写操作 (如文件删除、数据库修改)。
	LevelAskAlways Level = 3

	// LevelEscalate 升级到人工操作员: 在网关模式下转发给人工审核。
	// 适用于需要人工判断的关键操作 (如生产环境部署)。
	LevelEscalate Level = 4
)

// levelNames 定义权限级别的可读名称映射。
var levelNames = map[Level]string{
	LevelAutoAllow: "auto_allow",
	LevelAutoDeny:  "auto_deny",
	LevelAskOnce:   "ask_once",
	LevelAskAlways: "ask_always",
	LevelEscalate:  "escalate",
}

// String 返回权限级别的可读名称。
func (l Level) String() string {
	if name, ok := levelNames[l]; ok {
		return name
	}
	return fmt.Sprintf("unknown(%d)", int(l))
}

// ParseLevel 从字符串解析权限级别。
// 支持的值: auto_allow, auto_deny, ask_once, ask_always, escalate。
// 也支持简写: allow, deny, once, always, escalate。
func ParseLevel(s string) (Level, error) {
	switch s {
	case "auto_allow", "allow", "0":
		return LevelAutoAllow, nil
	case "auto_deny", "deny", "1":
		return LevelAutoDeny, nil
	case "ask_once", "once", "2":
		return LevelAskOnce, nil
	case "ask_always", "always", "3":
		return LevelAskAlways, nil
	case "escalate", "4":
		return LevelEscalate, nil
	default:
		return LevelAutoDeny, fmt.Errorf("未知权限级别: %q", s)
	}
}

// IsAllowed 返回此级别是否表示允许执行。
// LevelAutoAllow 和 LevelAskOnce (已记忆) 属于允许。
func (l Level) IsAllowed() bool {
	return l == LevelAutoAllow
}

// IsDenied 返回此级别是否表示拒绝执行。
func (l Level) IsDenied() bool {
	return l == LevelAutoDeny
}

// NeedsUserConfirm 返回此级别是否需要用户确认。
func (l Level) NeedsUserConfirm() bool {
	return l == LevelAskOnce || l == LevelAskAlways
}

// NeedsEscalation 返回此级别是否需要升级到人工操作员。
func (l Level) NeedsEscalation() bool {
	return l == LevelEscalate
}

// Strictness 返回权限严格程度 (数值越大越严格)。
// 用于策略冲突时的默认合并: 取更严格的级别。
func (l Level) Strictness() int {
	switch l {
	case LevelAutoAllow:
		return 0
	case LevelAutoDeny:
		return 4
	case LevelAskOnce:
		return 1
	case LevelAskAlways:
		return 2
	case LevelEscalate:
		return 3
	default:
		return 5
	}
}

// ───────────────────────────── 权限决策 ─────────────────────────────

// Decision 表示一次权限检查的完整决策结果。
type Decision struct {
	Level   Level  // 最终权限级别
	Reason  string // 决策原因描述
	Matched string // 匹配到的规则描述 (空 = 默认策略)
	RuleIdx int    // 匹配到的规则索引 (-1 = 默认策略)
}

// IsAllowed 快捷判断: 决策是否允许执行。
func (d *Decision) IsAllowed() bool {
	return d.Level.IsAllowed()
}

// IsDenied 快捷判断: 决策是否拒绝执行。
func (d *Decision) IsDenied() bool {
	return d.Level.IsDenied()
}

// String 返回决策的可读描述。
func (d *Decision) String() string {
	return fmt.Sprintf("[%s] %s (规则: %s)", d.Level, d.Reason, d.Matched)
}
