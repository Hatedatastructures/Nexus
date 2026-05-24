// Package tool 提供定时任务管理工具（供 Agent 调用）。
// 注意：此工具不直接导入 internal/cron，而是通过回调函数与 cron 系统交互，
// 避免循环依赖。
package tool

import (
	"context"
	"fmt"
	"sync/atomic"
)

// ───────────────────────────── 回调函数注入 ─────────────────────────────

// CronJobManager 定义了定时任务管理器的接口。
type CronJobManager interface {
	CreateJob(ctx context.Context, name, schedule, prompt string) (string, error)
	ListJobs(ctx context.Context) ([]map[string]any, error)
	PauseJob(ctx context.Context, id string) error
	ResumeJob(ctx context.Context, id string) error
	DeleteJob(ctx context.Context, id string) error
}

var cronJobManager atomic.Value // stores CronJobManager

// SetCronJobManager 注入定时任务管理器。
func SetCronJobManager(m CronJobManager) {
	cronJobManager.Store(m)
}

// getCronJobManager 从 atomic.Value 中加载定时任务管理器。
func getCronJobManager() CronJobManager {
	if v := cronJobManager.Load(); v != nil {
		return v.(CronJobManager)
	}
	return nil
}

// ───────────────────────────── CronJobTool ─────────────────────────────

// CronJobTool 定时任务管理工具。
type CronJobTool struct{}

func (t *CronJobTool) Name() string        { return "cronjob" }
func (t *CronJobTool) Description() string  { return "管理定时任务。支持创建、列出、暂停、恢复、删除定时任务。" }
func (t *CronJobTool) Toolset() string      { return "cron" }
func (t *CronJobTool) IsAvailable() bool { return getCronJobManager() != nil }
func (t *CronJobTool) Emoji() string        { return "⏰" }
func (t *CronJobTool) MaxResultChars() int  { return 10000 }

func (t *CronJobTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "操作类型",
					"enum":        []string{"create", "list", "pause", "resume", "delete"},
				},
				"name": map[string]any{
					"type":        "string",
					"description": "任务名称 (create 操作时必填)",
				},
				"schedule": map[string]any{
					"type":        "string",
					"description": "调度配置 (create 操作时必填)。格式: cron 表达式 (如 '0 9 * * *') 或间隔 (如 '30m', '1h')",
				},
				"prompt": map[string]any{
					"type":        "string",
					"description": "执行的提示词 (create 操作时必填)",
				},
				"job_id": map[string]any{
					"type":        "string",
					"description": "任务 ID (pause/resume/delete 操作时必填)",
				},
			},
			"required": []string{"action"},
		},
	}
}

func (t *CronJobTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	mgr := getCronJobManager()
	if mgr == nil {
		return ToolError("定时任务管理器未初始化"), nil
	}

	action := getStringFromArgs(args, "action")
	if action == "" {
		return ToolError("action 参数是必填项"), nil
	}

	switch action {
	case "create":
		return t.createJob(ctx, args)
	case "list":
		return t.listJobs(ctx)
	case "pause":
		jobID := getStringFromArgs(args, "job_id")
		if jobID == "" {
			return ToolError("job_id 参数是必填项"), nil
		}
		return t.toggleJob(ctx, jobID, false)
	case "resume":
		jobID := getStringFromArgs(args, "job_id")
		if jobID == "" {
			return ToolError("job_id 参数是必填项"), nil
		}
		return t.toggleJob(ctx, jobID, true)
	case "delete":
		jobID := getStringFromArgs(args, "job_id")
		if jobID == "" {
			return ToolError("job_id 参数是必填项"), nil
		}
		return t.deleteJob(ctx, jobID)
	default:
		return ToolError(fmt.Sprintf("未知操作: %s", action)), nil
	}
}

func (t *CronJobTool) createJob(ctx context.Context, args map[string]any) (string, error) {
	name := getStringFromArgs(args, "name")
	schedule := getStringFromArgs(args, "schedule")
	prompt := getStringFromArgs(args, "prompt")

	if name == "" {
		return ToolError("name 参数是必填项"), nil
	}
	if schedule == "" {
		return ToolError("schedule 参数是必填项"), nil
	}
	if prompt == "" {
		return ToolError("prompt 参数是必填项"), nil
	}

	mgr := getCronJobManager()
	jobID, err := mgr.CreateJob(ctx, name, schedule, prompt)
	if err != nil {
		return ToolError(fmt.Sprintf("创建任务失败: %v", err)), nil
	}

	return ToolResult(map[string]any{
		"success": true,
		"message": fmt.Sprintf("任务已创建: %s", name),
		"job_id":  jobID,
	}), nil
}

func (t *CronJobTool) listJobs(ctx context.Context) (string, error) {
	mgr := getCronJobManager()
	jobs, err := mgr.ListJobs(ctx)
	if err != nil {
		return ToolError(fmt.Sprintf("列出任务失败: %v", err)), nil
	}

	if len(jobs) == 0 {
		return ToolResult(map[string]any{
			"success": true,
			"message": "无定时任务",
			"count":   0,
		}), nil
	}

	return ToolResult(map[string]any{
		"success": true,
		"count":   len(jobs),
		"jobs":    jobs,
	}), nil
}

func (t *CronJobTool) toggleJob(ctx context.Context, jobID string, enabled bool) (string, error) {
	mgr := getCronJobManager()
	var err error
	if enabled {
		err = mgr.ResumeJob(ctx, jobID)
	} else {
		err = mgr.PauseJob(ctx, jobID)
	}

	if err != nil {
		return ToolError(fmt.Sprintf("更新任务失败: %v", err)), nil
	}

	action := "暂停"
	if enabled {
		action = "恢复"
	}

	return ToolResult(map[string]any{
		"success": true,
		"message": fmt.Sprintf("任务已%s", action),
	}), nil
}

func (t *CronJobTool) deleteJob(ctx context.Context, jobID string) (string, error) {
	mgr := getCronJobManager()
	if err := mgr.DeleteJob(ctx, jobID); err != nil {
		return ToolError(fmt.Sprintf("删除任务失败: %v", err)), nil
	}

	return ToolResult(map[string]any{
		"success": true,
		"message": "任务已删除",
	}), nil
}

func init() {
	GetRegistry().Register(&CronJobTool{})
}
