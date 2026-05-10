// Package agent 提供自动恢复配方引擎。
// recovery.go 实现基于配方的错误恢复策略，将错误分类结果映射到具体的恢复动作。
// 内置覆盖常见 LLM API 错误场景的默认配方，支持自定义扩展。
package agent

import (
	"fmt"
	"regexp"
	"strconv"
	"time"

	"nexus-agent/internal/llm"
)

// ───────────────────────────── 恢复动作 ─────────────────────────────

// RecoveryAction 描述错误发生后的恢复策略。
type RecoveryAction struct {
	Strategy string        // 恢复策略标识 (见 Strategy* 常量)
	WaitTime time.Duration // 等待时间 (仅 wait_and_retry 策略使用)
	Message  string        // 人类可读的恢复说明
}

// 恢复策略常量
const (
	// StrategyCompressAndRetry 压缩上下文后重试
	StrategyCompressAndRetry = "compress_and_retry"
	// StrategyWaitAndRetry 等待指定时间后重试 (用于速率限制)
	StrategyWaitAndRetry = "wait_and_retry"
	// StrategyRotateCredential 轮换凭证后重试
	StrategyRotateCredential = "rotate_credential"
	// StrategyFallbackModel 切换到备选模型
	StrategyFallbackModel = "fallback_model"
	// StrategyTruncateAndRetry 截断输入后重试
	StrategyTruncateAndRetry = "truncate_and_retry"
	// StrategyAbort 终止操作，不重试
	StrategyAbort = "abort"
)

// ───────────────────────────── 恢复配方 ─────────────────────────────

// RecoveryRecipe 定义一条恢复配方。
// 当条件函数返回 true 时，引擎选择该配方对应的恢复动作。
type RecoveryRecipe struct {
	Name      string                                               // 配方名称 (用于日志)
	Condition func(err error, classified *llm.ClassifiedError) bool // 匹配条件
	Build     func(err error, classified *llm.ClassifiedError) RecoveryAction // 构建恢复动作
	Priority  int                                                  // 优先级 (数字越小越优先)
}

// ───────────────────────────── 恢复引擎 ─────────────────────────────

// RecoveryEngine 是错误恢复引擎，维护一组有序配方。
// 遇到错误时按优先级依次匹配配方，返回第一个命中的恢复动作。
type RecoveryEngine struct {
	recipes []RecoveryRecipe
}

// NewRecoveryEngine 创建恢复引擎实例，预装所有内置默认配方。
// 配方按优先级升序排列，先匹配的先执行。
func NewRecoveryEngine() *RecoveryEngine {
	e := &RecoveryEngine{}
	e.registerDefaults()
	return e
}

// ClassifyAndRecover 对错误进行分类并返回最匹配的恢复动作。
// 如果没有配方命中，返回 abort 策略作为安全兜底。
func (e *RecoveryEngine) ClassifyAndRecover(err error) RecoveryAction {
	if err == nil {
		return RecoveryAction{
			Strategy: StrategyAbort,
			Message:  "无错误，无需恢复",
		}
	}

	classified := llm.ClassifyFromError(err)

	// 按优先级遍历配方，返回第一个命中的
	for _, recipe := range e.recipes {
		if recipe.Condition(err, classified) {
			action := recipe.Build(err, classified)
			return action
		}
	}

	// 兜底：未知错误默认终止
	return RecoveryAction{
		Strategy: StrategyAbort,
		Message:  fmt.Sprintf("未匹配任何恢复配方 (reason=%s)，终止操作", classified.Reason),
	}
}

// AddRecipe 添加自定义恢复配方。配方按优先级自动排序。
func (e *RecoveryEngine) AddRecipe(recipe RecoveryRecipe) {
	e.recipes = append(e.recipes, recipe)
	// 按优先级升序排列
	sortRecipes(e.recipes)
}

// ───────────────────────────── 内置配方注册 ─────────────────────────────

// registerDefaults 注册所有内置恢复配方。
func (e *RecoveryEngine) registerDefaults() {
	e.recipes = []RecoveryRecipe{
		// 1. 上下文溢出 → 压缩后重试
		{
			Name: "context_overflow_compress",
			Condition: func(_ error, classified *llm.ClassifiedError) bool {
				return classified.Reason == llm.ReasonContextOverflow
			},
			Build: func(_ error, classified *llm.ClassifiedError) RecoveryAction {
				return RecoveryAction{
					Strategy: StrategyCompressAndRetry,
					Message:  fmt.Sprintf("上下文溢出，需压缩对话历史后重试 (status=%d)", classified.StatusCode),
				}
			},
			Priority: 10,
		},

		// 2. 请求体过大 → 截断后重试
		{
			Name: "payload_too_large_truncate",
			Condition: func(_ error, classified *llm.ClassifiedError) bool {
				return classified.Reason == llm.ReasonPayloadTooLarge
			},
			Build: func(_ error, classified *llm.ClassifiedError) RecoveryAction {
				return RecoveryAction{
					Strategy: StrategyTruncateAndRetry,
					Message:  fmt.Sprintf("请求体过大 (status=%d)，截断输入后重试", classified.StatusCode),
				}
			},
			Priority: 20,
		},

		// 3. 速率限制 → 等待后重试 (解析 Retry-After)
		{
			Name: "rate_limit_wait",
			Condition: func(_ error, classified *llm.ClassifiedError) bool {
				return classified.Reason == llm.ReasonRateLimit
			},
			Build: func(err error, classified *llm.ClassifiedError) RecoveryAction {
				waitTime := parseRetryAfter(classified.Message)
				if waitTime == 0 {
					waitTime = 30 * time.Second // 默认等待 30 秒
				}
				return RecoveryAction{
					Strategy: StrategyWaitAndRetry,
					WaitTime: waitTime,
					Message:  fmt.Sprintf("速率限制 (status=%d)，等待 %v 后重试", classified.StatusCode, waitTime),
				}
			},
			Priority: 30,
		},

		// 4. 认证耗尽 → 轮换凭证
		{
			Name: "auth_exhausted_rotate",
			Condition: func(_ error, classified *llm.ClassifiedError) bool {
				return classified.Reason == llm.ReasonAuth
			},
			Build: func(_ error, classified *llm.ClassifiedError) RecoveryAction {
				return RecoveryAction{
					Strategy: StrategyRotateCredential,
					Message:  fmt.Sprintf("认证失败 (status=%d)，需轮换 API Key", classified.StatusCode),
				}
			},
			Priority: 40,
		},

		// 5. 计费耗尽 → 轮换凭证 (可能有备用账号)
		{
			Name: "billing_exhausted_rotate",
			Condition: func(_ error, classified *llm.ClassifiedError) bool {
				return classified.Reason == llm.ReasonBilling
			},
			Build: func(_ error, classified *llm.ClassifiedError) RecoveryAction {
				return RecoveryAction{
					Strategy: StrategyRotateCredential,
					Message:  fmt.Sprintf("计费额度耗尽 (status=%d)，需切换账号或充值", classified.StatusCode),
				}
			},
			Priority: 45,
		},

		// 6. 模型不存在 → 切换备选模型
		{
			Name: "model_not_found_fallback",
			Condition: func(_ error, classified *llm.ClassifiedError) bool {
				return classified.Reason == llm.ReasonModelNotFound
			},
			Build: func(_ error, classified *llm.ClassifiedError) RecoveryAction {
				return RecoveryAction{
					Strategy: StrategyFallbackModel,
					Message:  fmt.Sprintf("模型不可用 (status=%d)，需切换到备选模型", classified.StatusCode),
				}
			},
			Priority: 50,
		},

		// 7. 服务器过载 → 等待后重试 (指数退避)
		{
			Name: "overloaded_wait",
			Condition: func(_ error, classified *llm.ClassifiedError) bool {
				return classified.Reason == llm.ReasonOverloaded
			},
			Build: func(_ error, classified *llm.ClassifiedError) RecoveryAction {
				return RecoveryAction{
					Strategy: StrategyWaitAndRetry,
					WaitTime: 10 * time.Second,
					Message:  fmt.Sprintf("服务过载 (status=%d)，等待 10s 后重试", classified.StatusCode),
				}
			},
			Priority: 60,
		},

		// 8. 服务器内部错误 → 等待后重试
		{
			Name: "server_error_wait",
			Condition: func(_ error, classified *llm.ClassifiedError) bool {
				return classified.Reason == llm.ReasonServerError
			},
			Build: func(_ error, classified *llm.ClassifiedError) RecoveryAction {
				return RecoveryAction{
					Strategy: StrategyWaitAndRetry,
					WaitTime: 5 * time.Second,
					Message:  fmt.Sprintf("服务器错误 (status=%d)，等待 5s 后重试", classified.StatusCode),
				}
			},
			Priority: 70,
		},

		// 9. 超时 → 等待后重试
		{
			Name: "timeout_wait",
			Condition: func(_ error, classified *llm.ClassifiedError) bool {
				return classified.Reason == llm.ReasonTimeout
			},
			Build: func(_ error, classified *llm.ClassifiedError) RecoveryAction {
				return RecoveryAction{
					Strategy: StrategyWaitAndRetry,
					WaitTime: 3 * time.Second,
					Message:  "请求超时，等待 3s 后重试",
				}
			},
			Priority: 80,
		},

		// 10. 格式错误 → 终止 (请求本身有问题，重试无意义)
		{
			Name: "format_error_abort",
			Condition: func(_ error, classified *llm.ClassifiedError) bool {
				return classified.Reason == llm.ReasonFormatError
			},
			Build: func(_ error, classified *llm.ClassifiedError) RecoveryAction {
				return RecoveryAction{
					Strategy: StrategyAbort,
					Message:  fmt.Sprintf("请求格式错误 (status=%d)，需修正请求内容", classified.StatusCode),
				}
			},
			Priority: 90,
		},
	}

	// 确保配方按优先级排序
	sortRecipes(e.recipes)
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

// retryAfterRegexp 匹配常见的 Retry-After 格式。
// 支持: "retry after 30", "retry-after: 60", "try again in 45 seconds" 等。
var retryAfterRegexp = regexp.MustCompile(`(?i)(?:retry[- ]?after|try again in)\D*(\d+)`)

// parseRetryAfter 从错误消息中提取 Retry-After 秒数。
// 如果无法解析，返回 0。
func parseRetryAfter(msg string) time.Duration {
	matches := retryAfterRegexp.FindStringSubmatch(msg)
	if len(matches) < 2 {
		return 0
	}
	seconds, err := strconv.Atoi(matches[1])
	if err != nil || seconds <= 0 {
		return 0
	}
	// 合理上限：最多等待 5 分钟
	if seconds > 300 {
		seconds = 300
	}
	return time.Duration(seconds) * time.Second
}

// sortRecipes 按 Priority 升序排列配方切片。
func sortRecipes(recipes []RecoveryRecipe) {
	// 简单插入排序，配方数量少 (通常 < 20)，无需更复杂的算法
	for i := 1; i < len(recipes); i++ {
		key := recipes[i]
		j := i - 1
		for j >= 0 && recipes[j].Priority > key.Priority {
			recipes[j+1] = recipes[j]
			j--
		}
		recipes[j+1] = key
	}
}
