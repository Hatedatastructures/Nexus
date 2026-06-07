// Nexus Agent CLI 入口点。
package main

import (
	"fmt"
	"os"
	"sort"

	"nexus-agent/cmd/nexus/commands"
	"nexus-agent/internal/llm"
)

func main() {
	commands.RegisterAllCommands()
	llm.RegisterAllTransports()

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	// 获取命令包的全局函数
	cmdName := os.Args[1]

	// 特殊处理 help 命令
	if cmdName == "help" || cmdName == "--help" || cmdName == "-h" {
		printUsage()
		return
	}

	// 从注册表获取命令
	cmd, ok := commands.GetCommand(cmdName)
	if !ok {
		fmt.Fprintf(os.Stderr, "未知命令: %s\n", cmdName)
		fmt.Println()
		printUsage()
		os.Exit(1)
	}

	// 执行命令，传递剩余参数
	cmd.Run(os.Args[2:])
}

func printUsage() {
	fmt.Println("用法: nexus <command> [args...]")
	fmt.Println()
	fmt.Println("可用命令:")

	// 获取所有命令并排序
	names := commands.ListCommands()
	sort.Strings(names)

	// 命令描述映射
	descriptions := map[string]string{
		"chat":     "启动交互式对话",
		"config":   "配置管理 (show/validate/edit/set/path)",
		"provider": "LLM 提供者管理 (list/test)",
		"tool":     "工具管理 (list/info)",
		"session":  "会话管理 (list/search/export/stats)",
		"memory":   "管理记忆",
		"skill":    "技能管理 (list/search/install)",
		"cron":     "定时任务管理 (list/create/pause/resume/remove/status)",
		"gateway":  "网关服务管理 (run/start/stop/restart/status)",
		"setup":    "交互式设置向导",
		"model":    "交互式选择默认模型",
		"status":   "显示组件状态总览",
		"doctor":   "系统诊断检查",
		"version":  "显示版本信息",
		"backup":   "备份配置和数据",
		"logs":     "查看日志",
		"export":   "导出数据 (memory/config)",
		"import":   "导入资源 (skills)",
	}

	// 计算最大命令名长度
	maxLen := 0
	for _, name := range names {
		if len(name) > maxLen {
			maxLen = len(name)
		}
	}

	// 打印命令列表
	for _, name := range names {
		desc := descriptions[name]
		if desc == "" {
			desc = "..."
		}
		fmt.Printf("  %-*s  %s\n", maxLen, name, desc)
	}

	fmt.Println()
	fmt.Println("使用 nexus <command> --help 查看命令详细帮助")
}
