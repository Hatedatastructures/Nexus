package commands

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"nexus-agent/internal/agent"
	"nexus-agent/internal/config"
	"nexus-agent/internal/tool"
)

// ───────────────────────────── RL 训练 CLI ─────────────────────────────

// RLCommand 实现 nexus rl 子命令。
type RLCommand struct{}

func (c *RLCommand) Name() string     { return "rl" }
func (c *RLCommand) Synopsis() string { return "RL 训练交互模式" }

func (c *RLCommand) Run(args []string) {
	cfg, err := config.Load("")
	if err != nil {
		PrintError("加载配置失败: %v", err)
		return
	}

	// 检查 API key
	if err := checkRLRequirements(cfg); err != nil {
		PrintError("%s", err.Error())
		return
	}

	PrintTitle("RL 训练模式")
	fmt.Println("输入问题进行 RL 训练对话，输入 'exit' 退出。")
	fmt.Println()

	// 创建 agent
	a, err := createRLAgent(cfg)
	if err != nil {
		PrintError("创建 agent 失败: %v", err)
		return
	}

	// REPL 循环
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("rl> ")
		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if input == "exit" || input == "quit" {
			fmt.Println("退出 RL 训练模式。")
			break
		}

		ctx := context.Background()
		result, err := a.RunConversation(ctx, input, nil, rlSystemPrompt)
		if err != nil {
			PrintError("执行失败: %v", err)
			continue
		}

		fmt.Println()
		fmt.Println(result.FinalResponse)
		fmt.Printf("\n[Token: %d, 工具调用: %d, 耗时: %v]\n\n",
			result.TotalTokens, result.ToolCalls, result.Duration)
	}
}

func checkRLRequirements(cfg *config.Config) error {
	// 检查是否有可用的 provider
	if len(cfg.Providers) == 0 {
		return fmt.Errorf("未配置 LLM 提供者。请先运行 'nexus setup'")
	}
	return nil
}

func createRLAgent(cfg *config.Config) (*agent.AIAgent, error) {
	// 获取工具注册表
	registry := tool.NewRegistry()
	tool.RegisterAllTools(registry)

	opts := []agent.AgentOption{
		agent.WithMaxTokens(cfg.Agent.MaxTokens),
		agent.WithMaxIterations(cfg.Agent.MaxIterations),
		agent.WithToolRegistry(registry),
	}

	a := agent.NewAgent(opts...)
	return a, nil
}

// ───────────────────────────── 系统提示 ─────────────────────────────

const rlSystemPrompt = `你是一个 RL 训练助手。你的任务是帮助用户进行强化学习训练相关的工作。

你可以:
1. 编写和调试 RL 训练代码
2. 分析训练结果和指标
3. 优化超参数
4. 实现新的 RL 算法
5. 处理训练环境和数据

使用终端工具执行代码，使用文件工具编辑文件。
在完成任务后提供清晰的解释。`

// ───────────────────────────── 环境列表 ─────────────────────────────

// RLEnvironment 表示一个 RL 训练环境。
type RLEnvironment struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Framework   string `json:"framework"`
}

// ListEnvironments 列出可用的 RL 训练环境。
func ListEnvironments() []RLEnvironment {
	return []RLEnvironment{
		{Name: "cartpole", Description: "CartPole-v1 平衡任务", Framework: "gymnasium"},
		{Name: "mountaincar", Description: "MountainCar-v0 爬山任务", Framework: "gymnasium"},
		{Name: "atari", Description: "Atari 游戏环境", Framework: "gymnasium"},
		{Name: "mujoco", Description: "MuJoCo 物理仿真", Framework: "mujoco"},
		{Name: "custom", Description: "自定义训练环境", Framework: "custom"},
	}
}
