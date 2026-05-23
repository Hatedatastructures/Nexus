// Package cron 提供跨平台作业结果投递功能。
// 将 cron 作业的输出发送到指定的消息平台。
package cron

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"nexus-agent/internal/gateway/platforms"
)

// ───────────────────────────── 投递常量 ─────────────────────────────

// silentMarker 静默标记 — 当 agent 以 [SILENT] 开始回复时，跳过投递
const silentMarker = "[SILENT]"

// ───────────────────────────── 投递结果 ─────────────────────────────

// DeliverResult 将作业执行结果投递到指定平台。
//
// 参数:
//   - ctx: 上下文
//   - job: 已执行的作业
//   - result: 执行结果 (文本内容)
//   - platforms: 可用的平台适配器映射 (Platform -> PlatformAdapter)
//     如果为 nil，则跳过投递 (仅本地保存)
//
// 投递逻辑:
//  1. 如果结果为 "ok" (仅状态字符串)，不投递内容
//  2. 检查是否有活跃的适配器用于此作业的投递目标
//  3. 如无适配器可用，跳过投递并记录日志
//  4. 通过适配器发送消息
func DeliverResult(ctx context.Context, job *Job, result string, platformAdapters map[platforms.Platform]platforms.PlatformAdapter) error {
	if job == nil {
		return fmt.Errorf("作业不能为 nil")
	}

	// 状态结果不投递
	if result == "ok" || result == "" {
		return nil
	}

	// 检查静默标记
	if strings.HasPrefix(strings.TrimSpace(strings.ToUpper(result)), silentMarker) {
		slog.Info("Cron: job returned silent marker, skipping delivery", "job_id", job.ID)
		return nil
	}

	// 如果没有平台适配器可用，仅记录日志
	if platformAdapters == nil || len(platformAdapters) == 0 {
		slog.Debug("Cron: no platform adapter available, skipping delivery", "job_id", job.ID)
		return nil
	}

	// 构建投递内容
	content := buildDeliveryContent(job, result)
	if content == "" {
		return nil
	}

	successCount := 0
	failCount := 0

	// 尝试向每个可用的平台投递
	for platform, adapter := range platformAdapters {
		if adapter == nil {
			continue
		}

		// 构建目标 chat_id (暂时使用 job.Name 作为 fallback)
		chatID := "cron-" + job.ID
		if job.Name != "" {
			chatID = job.Name
		}

		// 发送消息
		sendResult, sendErr := adapter.Send(ctx, chatID, content, nil)
		if sendErr != nil {
			slog.Warn("Cron: failed to deliver to platform",
				"job_id", job.ID,
				"platform", platform,
				"error", sendErr,
			)
			failCount++
			continue
		}

		if sendResult != nil && !sendResult.Success {
			slog.Warn("Cron: platform returned send failure",
				"job_id", job.ID,
				"platform", platform,
				"error", sendResult.Error,
			)
			failCount++
			continue
		}

		successCount++
		slog.Info("Cron: delivery succeeded",
			"job_id", job.ID,
			"platform", platform,
			"chat_id", chatID,
		)
	}

	if failCount > 0 && successCount == 0 {
		return fmt.Errorf("作业 '%s' 投递到 %d 个平台全部失败", job.ID, failCount)
	}

	return nil
}

// buildDeliveryContent 构建投递的消息内容。
// 包装作业信息 header 和 footer，帮助用户识别 cron 投递。
func buildDeliveryContent(job *Job, result string) string {
	if result == "" {
		return ""
	}

	taskName := job.Name
	if taskName == "" {
		taskName = job.ID
	}

	var buf strings.Builder

	// Header
	buf.WriteString(fmt.Sprintf("Cron 作业响应: %s\n", taskName))
	buf.WriteString(fmt.Sprintf("(作业 ID: %s)\n", job.ID))
	buf.WriteString("─────────────\n\n")

	// 正文 (限制长度)
	maxLen := 3900 // Telegram 消息限制为 4096，留一些余量
	if len(result) > maxLen {
		result = result[:maxLen] + "\n\n[... 输出已截断 ...]"
	}
	buf.WriteString(result)

	// Footer
	buf.WriteString("\n\n─────────────\n")
	buf.WriteString(fmt.Sprintf("执行时间: %s\n", time.Now().Format("2006-01-02 15:04:05")))
	buf.WriteString(fmt.Sprintf("如需停止或管理此作业，发送新消息给我 (例如 \"停止提醒 %s\")。", taskName))

	return buf.String()
}
