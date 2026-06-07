package commands

import (
	"context"
	"fmt"
	"strings"
	"time"

	"nexus-agent/internal/config"
	"nexus-agent/internal/cron"
)

// CronCommand 实现 nexus cron 命令。
type CronCommand struct{}

func (c *CronCommand) Name() string { return "cron" }
func (c *CronCommand) Synopsis() string {
	return "定时任务管理 (list/create/edit/pause/resume/run/remove/status)"
}

func (c *CronCommand) Run(args []string) {
	if len(args) == 0 {
		c.listJobs()
		return
	}

	switch args[0] {
	case "list", "ls":
		c.listJobs()
	case "create", "new":
		c.createJob(args[1:])
	case "pause":
		if len(args) < 2 {
			PrintError("用法: nexus cron pause <job_id>")
		}
		c.toggleJob(args[1], false)
	case "resume":
		if len(args) < 2 {
			PrintError("用法: nexus cron resume <job_id>")
		}
		c.toggleJob(args[1], true)
	case "remove", "rm", "delete":
		if len(args) < 2 {
			PrintError("用法: nexus cron remove <job_id>")
		}
		c.removeJob(args[1])
	case "status":
		c.statusCron()
	default:
		PrintError("未知子命令: %s", args[0])
	}
}

func (c *CronCommand) getManager() (*cron.JobManager, error) {
	nexusHome := GetNexusHome()
	manager := cron.NewJobManager(nil, nexusHome+"/cron")
	return manager, nil
}

func (c *CronCommand) listJobs() {
	manager, err := c.getManager()
	if err != nil {
		PrintError("%v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	jobs, err := manager.List(ctx)
	if err != nil {
		PrintError("列出任务失败: %v", err)
	}

	PrintTitle("定时任务列表")

	if len(jobs) == 0 {
		fmt.Println(DimStyle.Render("  无定时任务"))
		fmt.Println()
		fmt.Println(DimStyle.Render("  提示: 使用 nexus cron create 创建任务"))
		return
	}

	for i, job := range jobs {
		status := GreenBold.Render("●")
		if !job.Enabled {
			status = DimStyle.Render("○")
		}

		fmt.Printf("  %2d. %s %s\n", i+1, status, job.Name)
		fmt.Printf("      ID:       %s\n", job.ID)
		fmt.Printf("      调度:     %s\n", job.Schedule)
		fmt.Printf("      提示:     %s\n", truncate(job.Prompt, 60))
		if !job.LastRunAt.IsZero() {
			fmt.Printf("      上次运行: %s\n", job.LastRunAt.Format("2006-01-02 15:04:05"))
		}
		if !job.NextRunAt.IsZero() {
			fmt.Printf("      下次运行: %s\n", job.NextRunAt.Format("2006-01-02 15:04:05"))
		}
		fmt.Println()
	}

	fmt.Printf("  共 %d 个任务\n", len(jobs))
	fmt.Println()
}

func (c *CronCommand) createJob(args []string) {
	if len(args) < 2 {
		fmt.Println("用法: nexus cron create <name> <schedule> [prompt]")
		fmt.Println()
		fmt.Println("示例:")
		fmt.Println("  nexus cron create \"每日报告\" \"0 9 * * *\" \"生成今日工作报告\"")
		fmt.Println("  nexus cron create \"清理日志\" \"0 0 * * 0\" \"清理 7 天前的日志\"")
		return
	}

	name := args[0]
	schedule := args[1]
	prompt := ""
	if len(args) > 2 {
		prompt = strings.Join(args[2:], " ")
	}

	manager, err := c.getManager()
	if err != nil {
		PrintError("%v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	job := &cron.Job{
		Name:     name,
		Schedule: schedule,
		Prompt:   prompt,
	}

	if err := manager.Create(ctx, job); err != nil {
		PrintError("创建任务失败: %v", err)
	}

	PrintSuccess(fmt.Sprintf("任务已创建: %s (ID: %s)", job.Name, job.ID))
}

func (c *CronCommand) toggleJob(id string, enabled bool) {
	manager, err := c.getManager()
	if err != nil {
		PrintError("%v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	job, err := manager.Get(ctx, id)
	if err != nil {
		PrintError("获取任务失败: %v", err)
	}

	job.Enabled = enabled
	if enabled {
		job.State = "scheduled"
	} else {
		job.State = "paused"
	}

	if err := manager.Update(ctx, job); err != nil {
		PrintError("更新任务失败: %v", err)
	}

	if enabled {
		PrintSuccess("任务已恢复")
	} else {
		PrintSuccess("任务已暂停")
	}
}

func (c *CronCommand) removeJob(id string) {
	manager, err := c.getManager()
	if err != nil {
		PrintError("%v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := manager.Delete(ctx, id); err != nil {
		PrintError("删除任务失败: %v", err)
	}

	PrintSuccess("任务已删除")
}

func (c *CronCommand) statusCron() {
	cfg, err := config.Load("")
	if err != nil {
		PrintError("加载配置失败: %v", err)
	}

	PrintTitle("Cron 调度器状态")

	if cfg.Cron.Enabled {
		fmt.Printf("  %s\n", GreenBold.Render("● 已启用"))
		fmt.Printf("  最大并行任务: %d\n", cfg.Cron.MaxParallelJobs)
		fmt.Printf("  检测间隔: %d 秒\n", cfg.Cron.TickIntervalSecs)
	} else {
		fmt.Printf("  %s\n", DimStyle.Render("○ 未启用"))
		fmt.Println()
		fmt.Println(DimStyle.Render("  提示: 在 config.yaml 中设置 cron.enabled: true"))
	}
	fmt.Println()
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
