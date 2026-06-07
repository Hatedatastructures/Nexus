package commands

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"nexus-agent/internal/agent"
	"nexus-agent/internal/config"
	ctxbuilder "nexus-agent/internal/context"
	"nexus-agent/internal/llm"
	"nexus-agent/internal/memory"
	"nexus-agent/internal/skill"
	"nexus-agent/internal/tool"
	"nexus-agent/pkg/logutil"
)

var spinnerChars = []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}

var (
	reasoningLbl = lipgloss.NewStyle().Foreground(lipgloss.Color("#A78BFA")).Bold(true)
)

// ChatCommand 实现 nexus chat 命令。
type ChatCommand struct{}

func (c *ChatCommand) Name() string     { return "chat" }
func (c *ChatCommand) Synopsis() string { return "启动交互式对话" }

func (c *ChatCommand) Run(args []string) {
	cfg, err := config.Load("")
	if err != nil {
		PrintError("加载配置失败: %v", err)
	}

	logutil.InitLogger(cfg.Logging.Level, cfg.Logging.Format, "")

	registry := tool.NewRegistry()
	tool.RegisterAllTools(registry)

	// 构建记忆管理器 (如果配置了)
	var memMgr *memory.Manager
	if homeDir, err := os.UserHomeDir(); err == nil {
		builtinProvider := memory.NewBuiltinProvider(filepath.Join(homeDir, ".nexus"))
		memMgr = memory.NewManager(builtinProvider)
	}

	// 构建技能管理器 (如果配置了)
	var skillMgr *skill.Manager
	if homeDir, err := os.UserHomeDir(); err == nil {
		skillsDir := filepath.Join(homeDir, ".nexus", "skills")
		skillMgr = skill.NewManager(skillsDir, cfg.Skills.Disabled)
	}

	// 构建系统提示词构建器 (接入完整系统提示词管线)
	ctxBuilder := ctxbuilder.NewBuilder("", "cli", memMgr, skillMgr)

	sessionAgent := agent.NewAgent(
		agent.WithConfigProvider(cfg),
		agent.WithToolRegistry(registry),
		agent.WithContextBuilder(ctxBuilder),
		agent.WithMemoryManager(memMgr),
	)
	if sessionAgent.Provider() == nil {
		PrintError("LLM 提供者未初始化")
	}

	// 注入子代理执行器，使 delegate_task 工具可用
	tool.SetSubAgentRunner(func(ctx context.Context, systemPrompt, task string) (string, error) {
		subAgent := agent.NewAgent(
			agent.WithConfigProvider(cfg),
			agent.WithToolRegistry(registry),
			agent.WithMaxIterations(15),
		)
		if subAgent.Provider() == nil {
			return "", fmt.Errorf("子代理 LLM 提供者未初始化")
		}

		result, err := subAgent.RunConversation(ctx, task, nil, systemPrompt)
		if err != nil {
			return "", err
		}
		return result.FinalResponse, nil
	})

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	md, _ := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(0),
		glamour.WithPreservedNewLines(),
	)

	fmt.Println()
	fmt.Println(TitleStyle.Render("  ✦ Nexus Agent"))
	fmt.Println()
	fmt.Println(DimStyle.Render(fmt.Sprintf("    模型: %s", cfg.Agent.Model)))
	fmt.Println(DimStyle.Render("    /clear 清空  ·  /quit 退出  ·  /tr 切换思考  ·  滚动查看历史"))
	fmt.Println()
	fmt.Println(strings.Repeat("━", 70))
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024)

	var history []llm.Message
	roundNum := 0
	showReasoning := false

	for {
		fmt.Print(UserStyle.Render("▎ ") + " ")

		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		// /tr 切换推理过程
		if input == "/tr" || input == "/toggle-reasoning" {
			showReasoning = !showReasoning
			if showReasoning {
				fmt.Println(GreenBold.Render("  ✓ 推理过程已开启"))
			} else {
				fmt.Println(DimStyle.Render("  ✗ 推理过程已关闭"))
			}
			continue
		}

		switch strings.ToLower(input) {
		case "quit", "exit", "q":
			fmt.Println(DimStyle.Render("  再见!"))
			return
		case "/clear":
			history = nil
			roundNum = 0
			fmt.Println()
			fmt.Println(GreenBold.Render("  ✦ 对话已清空"))
			fmt.Println()
			continue
		}

		roundNum++
		fmt.Println()

		// Spinner — 等待首 token
		spinDone := make(chan struct{})
		go func() {
			idx := 0
			for {
				select {
				case <-spinDone:
					return
				default:
					fmt.Printf("\r  %c  思考中...", spinnerChars[idx])
					idx = (idx + 1) % len(spinnerChars)
					time.Sleep(80 * time.Millisecond)
				}
			}
		}()

		// 推理实时输出 + 流式累积（不打印）
		var mu sync.Mutex
		var reasonText strings.Builder
		var aiBuf strings.Builder

		sessionAgent.SetReasoningCallback(func(reasoning string) {
			mu.Lock()
			defer mu.Unlock()
			reasonText.WriteString(reasoning)
			if showReasoning {
				fmt.Print(reasoningLbl.Render("💭 ") + DimStyle.Render(reasoning))
			}
		})

		sessionAgent.SetStreamCallback(func(delta string) {
			mu.Lock()
			aiBuf.WriteString(delta)
			mu.Unlock()
		})

		// 执行
		result, err := sessionAgent.RunConversation(ctx, input, history, "")

		// 停止 spinner
		select {
		case <-spinDone:
		default:
			close(spinDone)
		}

		if err != nil {
			fmt.Print("\r" + strings.Repeat(" ", 50) + "\r")
			fmt.Println()
			fmt.Println(ErrorStyle.Render(fmt.Sprintf("  ✖ %v", err)))
			fmt.Println()
			continue
		}

		// Markdown 渲染一次输出
		finalAI := deduplicateResponse(result.FinalResponse)
		if finalAI != "" {
			rendered, err := md.Render(finalAI)
			if err != nil {
				fmt.Println(finalAI)
			} else {
				fmt.Print(rendered)
			}
		}

		// 统计（始终显示）
		tokenInfo := ""
		if result.TotalTokens > 0 {
			tokenInfo = fmt.Sprintf("Token: %d", result.TotalTokens)
		} else {
			// API 未返回 usage 时估算
			estimated := len(finalAI) / 4
			if estimated > 0 {
				tokenInfo = fmt.Sprintf("Token: ~%d (估算)", estimated)
			} else {
				tokenInfo = "Token: -"
			}
		}
		fmt.Println()
		fmt.Println(DimStyle.Render(fmt.Sprintf("  ╭─ Round %d · API: %d · 工具: %d · %s · 耗时: %v",
			roundNum, result.APICalls, result.ToolCalls, tokenInfo,
			result.Duration.Truncate(100*time.Millisecond))))
		if !result.Completed {
			fmt.Println(DimStyle.Render("  ╰─ 状态: 未完成"))
		}
		fmt.Println()

		// 使用 result.Messages 作为完整历史（包含 reasoning_content）
		// 跳过 system prompt（第一条消息）
		if len(result.Messages) > 0 {
			history = result.Messages[1:]
		}
	}
}

// deduplicateResponse 去除 LLM 回复中的完整自我重复。
func deduplicateResponse(resp string) string {
	if len(resp) < 200 {
		return resp
	}
	// 取前 15 个字符作为指纹
	prefixLen := 15
	fingerprint := resp[:prefixLen]
	// 从中间位置开始搜索相同指纹
	half := len(resp) / 2
	for i := half; i < len(resp)-prefixLen; i++ {
		if resp[i:i+prefixLen] == fingerprint {
			// 验证后续内容是否也匹配
			if resp[prefixLen:prefixLen*2] == resp[i+prefixLen:i+prefixLen*2] {
				return strings.TrimSpace(resp[:i])
			}
		}
	}
	return resp
}
