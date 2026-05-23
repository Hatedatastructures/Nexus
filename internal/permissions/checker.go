// checker.go 定义权限检查器。
// Checker 是权限系统的核心入口，整合策略引擎和会话记忆。
// 同时包装现有的 approval.Checker，为其添加五级语义。

package permissions

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"nexus-agent/internal/approval"
)

// ───────────────────────────── 权限检查器 ─────────────────────────────

// Checker 是权限检查器，整合策略引擎和会话级记忆。
// 提供统一的权限检查入口，所有工具调用通过此检查器判断权限。
type Checker struct {
	policy       *Policy            // 当前生效的策略
	approval     *approval.Checker  // 包装的原有审批检查器 (用于终端命令)
	sessionDecisions map[string]Level // 会话级决策缓存 (key = "toolName:argHash")
	mu               sync.RWMutex     // 并发保护
}

// NewChecker 创建权限检查器。
// policy 为权限策略，approvalChecker 为原有的终端审批检查器 (可选)。
// 如果 approvalChecker 为 nil，终端命令仅使用五级权限策略。
func NewChecker(policy *Policy, approvalChecker *approval.Checker) *Checker {
	if policy == nil {
		policy = DefaultPolicy()
	}
	return &Checker{
		policy:           policy,
		approval:         approvalChecker,
		sessionDecisions: make(map[string]Level),
	}
}

// ───────────────────────────── 核心检查方法 ─────────────────────────────

// Check 检查工具调用的权限。
// 返回权限决策结果，包含级别、原因和匹配到的规则。
// 这是权限系统的主要入口点。
func (c *Checker) Check(toolName string, args map[string]any) Decision {
	// 1. 策略引擎评估 (读锁保护 policy 读取)
	c.mu.RLock()
	policy := c.policy
	c.mu.RUnlock()
	decision := policy.Evaluate(toolName, args)

	// 2. 会话记忆检查 (ask_once 级别)
	if decision.Level == LevelAskOnce {
		if remembered := c.checkSessionMemory(toolName, args); remembered != nil {
			slog.Debug("permission matched session memory",
				"tool", toolName,
				"remembered_level", *remembered,
			)
			decision.Level = *remembered
			decision.Reason = "会话内已记忆的决策"
			decision.Matched = "会话记忆"
			decision.RuleIdx = -2
		}
	}

	// 3. 终端命令: 与原有审批引擎联动
	if toolName == "terminal" && c.approval != nil && !decision.IsDenied() {
		decision = c.mergeApprovalDecision(decision, args)
	}

	return decision
}

// CheckWithReason 检查权限并返回格式化的权限描述。
// 适用于需要向用户展示权限状态的场景。
func (c *Checker) CheckWithReason(ctx context.Context, toolName string, args map[string]any) (Level, string) {
	decision := c.Check(toolName, args)
	return decision.Level, decision.String()
}

// ───────────────────────────── 会话记忆 ─────────────────────────────

// RememberDecision 记住用户在当前会话中的权限决策。
// 仅对 LevelAskOnce 有意义: 记住后，后续相同工具调用自动使用该决策。
func (c *Checker) RememberDecision(toolName string, args map[string]any, level Level) {
	key := buildMemoryKey(toolName, args)
	c.mu.Lock()
	c.sessionDecisions[key] = level
	c.mu.Unlock()

	slog.Info("permission decision remembered",
		"tool", toolName,
		"level", level,
		"key", key,
	)
}

// ForgetDecision 清除指定工具调用的会话记忆。
func (c *Checker) ForgetDecision(toolName string, args map[string]any) {
	key := buildMemoryKey(toolName, args)
	c.mu.Lock()
	delete(c.sessionDecisions, key)
	c.mu.Unlock()
}

// ClearSession 清除所有会话记忆。
// 通常在会话结束时调用。
func (c *Checker) ClearSession() {
	c.mu.Lock()
	c.sessionDecisions = make(map[string]Level)
	c.mu.Unlock()
}

// checkSessionMemory 检查会话记忆中是否有已记录的决策。
func (c *Checker) checkSessionMemory(toolName string, args map[string]any) *Level {
	key := buildMemoryKey(toolName, args)
	c.mu.RLock()
	defer c.mu.RUnlock()

	if level, ok := c.sessionDecisions[key]; ok {
		return &level
	}
	return nil
}

// ───────────────────────────── 审批引擎联动 ─────────────────────────────

// mergeApprovalDecision 将五级权限决策与原有审批引擎的结果合并。
// 合并策略: 取更严格的决策。
// 仅终端命令会触发此逻辑。
func (c *Checker) mergeApprovalDecision(permDecision Decision, args map[string]any) Decision {
	command, _ := args["command"].(string)
	if command == "" {
		return permDecision
	}

	approvalResult, approvalReason := c.approval.Check(context.Background(), command)

	// 审批引擎拒绝 = 权限系统中的 auto_deny
	if approvalResult == approval.Denied {
		return Decision{
			Level:   LevelAutoDeny,
			Reason:  fmt.Sprintf("审批引擎拒绝: %s", approvalReason),
			Matched: "审批引擎",
			RuleIdx: -3,
		}
	}

	// 审批引擎待审批 = 权限系统中的 ask_always
	if approvalResult == approval.Pending {
		// 如果权限系统已经是 ask_always 或更高级别，保持权限系统的决策
		if permDecision.Level >= LevelAskAlways {
			return permDecision
		}
		// 否则升级为 ask_always
		return Decision{
			Level:   LevelAskAlways,
			Reason:  fmt.Sprintf("审批引擎要求审批: %s", approvalReason),
			Matched: "审批引擎",
			RuleIdx: -3,
		}
	}

	// 审批引擎通过，保持权限系统的决策
	return permDecision
}

// ───────────────────────────── 策略管理 ─────────────────────────────

// SetPolicy 运行时切换策略。
// 线程安全。
func (c *Checker) SetPolicy(policy *Policy) {
	c.mu.Lock()
	c.policy = policy
	c.mu.Unlock()
	slog.Info("permission policy switched", "name", policy.Name)
}

// Policy 返回当前生效的策略。
func (c *Checker) Policy() *Policy {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.policy
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

// buildMemoryKey 构建会话记忆的缓存键。
// 使用工具名和关键参数的哈希。
func buildMemoryKey(toolName string, args map[string]any) string {
	// 简化实现: 使用工具名 + 命令/路径参数作为键
	// 生产环境应使用更精确的参数指纹
	switch toolName {
	case "terminal":
		cmd, _ := args["command"].(string)
		return fmt.Sprintf("terminal:%s", cmd)
	case "file_write", "file_edit", "file_read", "file_delete":
		path, _ := args["path"].(string)
		return fmt.Sprintf("%s:%s", toolName, path)
	default:
		return fmt.Sprintf("%s:*", toolName)
	}
}
