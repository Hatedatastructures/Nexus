// Nexus Agent CLI 入口点。
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"nexus-agent/internal/agent"
	"nexus-agent/internal/config"
	"nexus-agent/internal/llm"
	"nexus-agent/internal/state"
	"nexus-agent/internal/tool"
	"nexus-agent/pkg/logutil"
)

var (
	titleStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#6EE7B7")).Bold(true)
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))
	userStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#FBBF24")).Bold(true)
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#F87171")).Bold(true)
	greenBold    = lipgloss.NewStyle().Foreground(lipgloss.Color("#34D399")).Bold(true)
	reasoningLbl = lipgloss.NewStyle().Foreground(lipgloss.Color("#A78BFA")).Bold(true)
)

var spinnerChars = []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}

// ───────────────────────────── 去重 ─────────────────────────────

// deduplicateResponse 去除 LLM 回复中的完整自我重复。
// 某些 OpenAI 兼容 API 会把回复内容返回两遍，此函数检测并截断。
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

// ───────────────────────────── 主入口 ─────────────────────────────

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}
	switch os.Args[1] {
	case "chat":
		runChat()
	case "skill":
		runSkill()
	case "memory":
		runMemory()
	case "cron":
		runCron()
	case "config":
		runConfig()
	case "provider":
		runProvider()
	case "tool":
		runTool()
	case "session":
		runSession()
	case "export":
		runExport()
	case "import":
		runImport()
	default:
		fmt.Fprintf(os.Stderr, "未知命令: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func runChat() {
	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", errorStyle.Render(fmt.Sprintf("加载配置失败: %v", err)))
		os.Exit(1)
	}

	logutil.InitLogger(cfg.Logging.Level, cfg.Logging.Format, "")
	tool.DiscoverBuiltin()

	sessionAgent := agent.NewAgent(
		agent.WithConfigProvider(cfg),
		agent.WithToolRegistry(tool.GetRegistry()),
	)
	if sessionAgent.Provider() == nil {
		fmt.Fprintf(os.Stderr, "%s\n", errorStyle.Render("错误: LLM 提供者未初始化"))
		os.Exit(1)
	}

	// 注入子代理执行器，使 delegate_task 工具可用
	tool.SetSubAgentRunner(func(ctx context.Context, systemPrompt, task string) (string, error) {
		subAgent := agent.NewAgent(
			agent.WithConfigProvider(cfg),
			agent.WithToolRegistry(tool.GetRegistry()),
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
	fmt.Println(titleStyle.Render("  ✦ Nexus Agent"))
	fmt.Println()
	fmt.Println(dimStyle.Render(fmt.Sprintf("    模型: %s", cfg.Agent.Model)))
	fmt.Println(dimStyle.Render("    /clear 清空  ·  /quit 退出  ·  /tr 切换思考  ·  滚动查看历史"))
	fmt.Println()
	fmt.Println(strings.Repeat("━", 70))
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024)

	var history []llm.Message
	roundNum := 0
	showReasoning := false

	for {
		fmt.Print(userStyle.Render("▎ ") + " ")

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
				fmt.Println(greenBold.Render("  ✓ 推理过程已开启"))
			} else {
				fmt.Println(dimStyle.Render("  ✗ 推理过程已关闭"))
			}
			continue
		}

		switch strings.ToLower(input) {
		case "quit", "exit", "q":
			fmt.Println(dimStyle.Render("  再见!"))
			return
		case "/clear":
			history = nil
			roundNum = 0
			fmt.Println()
			fmt.Println(greenBold.Render("  ✦ 对话已清空"))
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
				fmt.Print(reasoningLbl.Render("💭 ") + dimStyle.Render(reasoning))
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
			fmt.Println(errorStyle.Render(fmt.Sprintf("  ✖ %v", err)))
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
		fmt.Println(dimStyle.Render(fmt.Sprintf("  ╭─ Round %d · API: %d · 工具: %d · %s · 耗时: %v",
			roundNum, result.APICalls, result.ToolCalls, tokenInfo,
			result.Duration.Truncate(100*time.Millisecond))))
		if !result.Completed {
			fmt.Println(dimStyle.Render("  ╰─ 状态: 未完成"))
		}
		fmt.Println()

		// 使用 result.Messages 作为完整历史（包含 reasoning_content）
		// 跳过 system prompt（第一条消息）
		if len(result.Messages) > 0 {
			history = result.Messages[1:]
		}
	}
}

func printUsage() {
	fmt.Println("用法: nexus <command>")
	fmt.Println("可用命令:")
	fmt.Println("  chat      - 启动交互式对话")
	fmt.Println("  skill     - 管理技能")
	fmt.Println("  memory    - 管理记忆")
	fmt.Println("  cron      - 管理定时任务")
	fmt.Println("  config    - 配置管理 (show/validate)")
	fmt.Println("  provider  - LLM 提供者管理 (list/test)")
	fmt.Println("  tool      - 工具管理 (list/info)")
	fmt.Println("  session   - 会话管理 (list/search)")
	fmt.Println("  export    - 导出数据 (memory/config)")
	fmt.Println("  import    - 导入资源 (skills)")
}

func runSkill() {
	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", errorStyle.Render(fmt.Sprintf("加载配置失败: %v", err)))
		os.Exit(1)
	}
	tool.DiscoverBuiltin()
	fmt.Println(titleStyle.Render("已注册的可用工具"))
	fmt.Println(strings.Repeat("━", 60))
	defs := tool.GetRegistry().GetDefinitions(nil)
	for i, d := range defs {
		fmt.Printf("  %2d. %-20s  %s\n", i+1, d.Name, d.Description)
	}
	fmt.Printf("\n共 %d 个工具\n", len(defs))
	_ = cfg
}

func runMemory() {
	home, _ := os.UserHomeDir()
	memPath := fmt.Sprintf("%s/.nexus/MEMORY.md", home)
	fmt.Println(titleStyle.Render("持久记忆"))
	fmt.Println(strings.Repeat("━", 60))
	if data, err := os.ReadFile(memPath); err == nil {
		fmt.Println(string(data))
	} else {
		fmt.Fprintf(os.Stderr, "%s\n", dimStyle.Render(fmt.Sprintf("未找到 MEMORY.md (预期路径: %s)", memPath)))
	}
	userPath := fmt.Sprintf("%s/.nexus/USER.md", home)
	if data, err := os.ReadFile(userPath); err == nil {
		fmt.Println()
		fmt.Println(titleStyle.Render("用户记忆 (USER.md)"))
		fmt.Println(strings.Repeat("━", 60))
		fmt.Println(string(data))
	}
}

func runCron() {
	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", errorStyle.Render(fmt.Sprintf("加载配置失败: %v", err)))
		os.Exit(1)
	}
	fmt.Println(titleStyle.Render("Cron 定时任务"))
	fmt.Println(strings.Repeat("━", 60))
	if cfg.Cron.Enabled {
		fmt.Println(greenBold.Render("● 已启用"))
	} else {
		fmt.Println(dimStyle.Render("○ 未启用"))
	}
	fmt.Printf("%s\n", dimStyle.Render(fmt.Sprintf("最大并行任务数: %d", cfg.Cron.MaxParallelJobs)))
	fmt.Printf("%s\n", dimStyle.Render(fmt.Sprintf("检测间隔: %d 秒", cfg.Cron.TickIntervalSecs)))
}

// ───────────────────────────── config 子命令 ─────────────────────────────

func runConfig() {
	if len(os.Args) < 3 {
		fmt.Println("用法: nexus config <subcommand>")
		fmt.Println("可用子命令:")
		fmt.Println("  show      - 显示当前配置（脱敏后的 API Key）")
		fmt.Println("  validate  - 验证配置文件是否有效")
		os.Exit(1)
	}

	switch os.Args[2] {
	case "show":
		runConfigShow()
	case "validate":
		runConfigValidate()
	default:
		fmt.Fprintf(os.Stderr, "%s\n", errorStyle.Render(fmt.Sprintf("未知子命令: %s", os.Args[2])))
		os.Exit(1)
	}
}

// runConfigShow 显示当前配置（API Key 脱敏）
func runConfigShow() {
	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", errorStyle.Render(fmt.Sprintf("加载配置失败: %v", err)))
		os.Exit(1)
	}

	fmt.Println(titleStyle.Render("当前配置"))
	fmt.Println(strings.Repeat("━", 60))

	// 代理配置
	fmt.Println(greenBold.Render("  [Agent]"))
	fmt.Printf("    模型:       %s\n", cfg.Agent.Model)
	fmt.Printf("    提供者:     %s\n", cfg.Agent.Provider)
	fmt.Printf("    最大 Token:  %d\n", cfg.Agent.MaxTokens)
	fmt.Printf("    最大迭代:    %d\n", cfg.Agent.MaxIterations)
	fmt.Printf("    备选模型:    %s\n", cfg.Agent.FallbackModel)
	fmt.Println()

	// 提供者配置（脱敏）
	fmt.Println(greenBold.Render("  [Providers]"))
	if len(cfg.Providers) == 0 {
		fmt.Printf("    %s\n", dimStyle.Render("无已配置的提供者"))
	} else {
		for name, p := range cfg.Providers {
			maskedKey := maskAPIKey(p.APIKey)
			fmt.Printf("    %-20s  %s\n", name, maskedKey)
			fmt.Printf("      BaseURL: %s\n", p.BaseURL)
			fmt.Printf("      APIMode: %s\n", p.APIMode)
		}
	}
	fmt.Println()

	// 工具配置
	fmt.Println(greenBold.Render("  [Tools]"))
	fmt.Printf("    结果最大字符: %d\n", cfg.Tools.ResultMaxChars)
	fmt.Printf("    搜索后端:     %s\n", cfg.Tools.WebSearchBackend)
	fmt.Println()

	// 网关配置
	fmt.Println(greenBold.Render("  [Gateway]"))
	if cfg.Gateway.Enabled {
		fmt.Printf("    %s\n", greenBold.Render("● 已启用"))
	} else {
		fmt.Printf("    %s\n", dimStyle.Render("○ 未启用"))
	}
	fmt.Println()

	// 日志配置
	fmt.Println(greenBold.Render("  [Logging]"))
	fmt.Printf("    级别: %s\n", cfg.Logging.Level)
	fmt.Printf("    格式: %s\n", cfg.Logging.Format)
}

// maskAPIKey 将 API Key 脱敏显示（仅显示前后各 4 个字符）
func maskAPIKey(key string) string {
	if key == "" {
		return "(未设置)"
	}
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "..." + key[len(key)-4:]
}

// runConfigValidate 验证配置文件是否有效
func runConfigValidate() {
	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", errorStyle.Render(fmt.Sprintf("加载配置失败: %v", err)))
		os.Exit(1)
	}

	valid := true
	var issues []string

	// 检查至少有一个提供者
	if len(cfg.Providers) == 0 {
		issues = append(issues, "未配置任何 LLM 提供者")
		valid = false
	}

	// 检查每个提供者
	for name, p := range cfg.Providers {
		if p.APIKey == "" {
			issues = append(issues, fmt.Sprintf("提供者 %q 缺少 API Key", name))
			valid = false
		}
		if p.BaseURL == "" {
			issues = append(issues, fmt.Sprintf("提供者 %q 缺少 BaseURL", name))
			valid = false
		}
		if p.APIMode == "" {
			issues = append(issues, fmt.Sprintf("提供者 %q 未指定 APIMode (建议使用 chat_completions 或 anthropic_messages)", name))
		}
	}

	// 检查默认模型
	if cfg.Agent.Model == "" {
		issues = append(issues, "未指定默认模型 (agent.model)")
		valid = false
	}

	// 检查工具集配置
	for _, ts := range cfg.Tools.EnabledToolsets {
		if _, ok := tool.DefaultToolsets[ts]; !ok {
			issues = append(issues, fmt.Sprintf("未知工具集: %q", ts))
			valid = false
		}
	}

	// 检查结果
	fmt.Println(titleStyle.Render("配置验证结果"))
	fmt.Println(strings.Repeat("━", 60))

	if valid {
		fmt.Println(greenBold.Render("  ✓ 配置文件有效"))
	} else {
		fmt.Println(errorStyle.Render("  ✖ 配置文件存在问题"))
		fmt.Println()
		for _, issue := range issues {
			fmt.Printf("    %s %s\n", errorStyle.Render("•"), issue)
		}
		os.Exit(1)
	}

	fmt.Printf("\n  提供者数量: %d\n", len(cfg.Providers))
	fmt.Printf("  默认模型:   %s\n", cfg.Agent.Model)
	fmt.Printf("  沙箱后端:   %s\n", cfg.Sandbox.Backend)
	fmt.Printf("  审批模式:   %s\n", cfg.Approval.Mode)
	fmt.Println()
}

// ───────────────────────────── provider 子命令 ─────────────────────────────

func runProvider() {
	if len(os.Args) < 3 {
		fmt.Println("用法: nexus provider <subcommand>")
		fmt.Println("可用子命令:")
		fmt.Println("  list         - 列出已配置的 LLM 提供者")
		fmt.Println("  test <name>  - 测试指定提供者的连接")
		os.Exit(1)
	}

	switch os.Args[2] {
	case "list":
		runProviderList()
	case "test":
		if len(os.Args) < 4 {
			fmt.Fprintf(os.Stderr, "%s\n", errorStyle.Render("用法: nexus provider test <name>"))
			os.Exit(1)
		}
		runProviderTest(os.Args[3])
	default:
		fmt.Fprintf(os.Stderr, "%s\n", errorStyle.Render(fmt.Sprintf("未知子命令: %s", os.Args[2])))
		os.Exit(1)
	}
}

// runProviderList 列出已配置的 LLM 提供者
func runProviderList() {
	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", errorStyle.Render(fmt.Sprintf("加载配置失败: %v", err)))
		os.Exit(1)
	}

	fmt.Println(titleStyle.Render("已配置的 LLM 提供者"))
	fmt.Println(strings.Repeat("━", 60))

	if len(cfg.Providers) == 0 {
		fmt.Printf("  %s\n", dimStyle.Render("无已配置的提供者"))
		fmt.Println()
		fmt.Println(dimStyle.Render("  提示: 在 ~/.nexus/config.yaml 中配置 providers"))
		return
	}

	for name, p := range cfg.Providers {
		isDefault := ""
		if name == cfg.Agent.Provider {
			isDefault = greenBold.Render(" (默认)")
		}
		maskedKey := maskAPIKey(p.APIKey)
		fmt.Printf("  %s%s\n", greenBold.Render(name), isDefault)
		fmt.Printf("    BaseURL:  %s\n", p.BaseURL)
		fmt.Printf("    APIMode:  %s\n", p.APIMode)
		fmt.Printf("    API Key:  %s\n", maskedKey)
		if p.OAuthURL != "" {
			fmt.Printf("    OAuth:    %s\n", p.OAuthURL)
		}
		fmt.Println()
	}
	fmt.Printf("  共 %d 个提供者\n", len(cfg.Providers))
}

// runProviderTest 测试指定提供者的连接
func runProviderTest(name string) {
	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", errorStyle.Render(fmt.Sprintf("加载配置失败: %v", err)))
		os.Exit(1)
	}

	pc, err := cfg.ResolveProvider(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", errorStyle.Render(fmt.Sprintf("提供者错误: %v", err)))
		os.Exit(1)
	}

	fmt.Printf("正在测试提供者 %q ...\n", name)
	fmt.Printf("  BaseURL: %s\n", pc.BaseURL)
	fmt.Printf("  APIMode: %s\n", pc.APIMode)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// 创建提供者实例
	var provider llm.Provider
	switch pc.APIMode {
	case "openai", "chat_completions":
		provider = llm.NewOpenAIProvider(nil, pc.APIKey, "", pc.BaseURL)
	case "anthropic", "anthropic_messages":
		provider = llm.NewAnthropicProvider(nil, pc.APIKey, "", pc.BaseURL)
	case "gemini":
		provider = llm.NewGeminiProvider(nil, pc.APIKey, "", pc.BaseURL)
	default:
		provider = llm.NewOpenAIProvider(nil, pc.APIKey, "", pc.BaseURL)
	}

	if provider == nil {
		fmt.Fprintf(os.Stderr, "%s\n", errorStyle.Render("无法创建提供者实例"))
		os.Exit(1)
	}

	// 尝试列出模型
	_, err = provider.ListModels(ctx)
	if err != nil {
		fmt.Println()
		fmt.Fprintf(os.Stderr, "%s\n", errorStyle.Render(fmt.Sprintf("✖ 连接失败: %v", err)))
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println(greenBold.Render("  ✓ 连接成功"))
}

// ───────────────────────────── tool 子命令 ─────────────────────────────

func runTool() {
	if len(os.Args) < 3 {
		fmt.Println("用法: nexus tool <subcommand>")
		fmt.Println("可用子命令:")
		fmt.Println("  list         - 列出所有已注册工具")
		fmt.Println("  info <name>  - 显示指定工具的详细信息")
		os.Exit(1)
	}

	switch os.Args[2] {
	case "list":
		runToolList()
	case "info":
		if len(os.Args) < 4 {
			fmt.Fprintf(os.Stderr, "%s\n", errorStyle.Render("用法: nexus tool info <name>"))
			os.Exit(1)
		}
		runToolInfo(os.Args[3])
	default:
		fmt.Fprintf(os.Stderr, "%s\n", errorStyle.Render(fmt.Sprintf("未知子命令: %s", os.Args[2])))
		os.Exit(1)
	}
}

// runToolList 列出所有已注册工具
func runToolList() {
	tool.DiscoverBuiltin()

	fmt.Println(titleStyle.Render("已注册的可用工具"))
	fmt.Println(strings.Repeat("━", 60))

	registry := tool.GetRegistry()
	names := registry.ListTools()

	if len(names) == 0 {
		fmt.Printf("  %s\n", dimStyle.Render("无已注册的工具"))
		return
	}

	// 按工具集分组显示
	toolsetMap := make(map[string][]string)
	for _, name := range names {
		entry := registry.GetEntry(name)
		if entry != nil {
			ts := entry.Tool.Toolset()
			toolsetMap[ts] = append(toolsetMap[ts], name)
		}
	}

	for ts, tools := range toolsetMap {
		tsDef, ok := tool.DefaultToolsets[ts]
		desc := ""
		if ok {
			desc = " - " + tsDef.Description
		}
		fmt.Printf("\n  %s%s\n", greenBold.Render("["+ts+"]"), desc)
		for _, name := range tools {
			entry := registry.GetEntry(name)
			if entry != nil {
				status := ""
				if entry.Tool.IsAvailable() {
					status = greenBold.Render("✓")
				} else {
					status = dimStyle.Render("○")
				}
				fmt.Printf("    %s %-25s %s\n", status, name, entry.Tool.Description())
			}
		}
	}

	fmt.Printf("\n  共 %d 个工具\n", len(names))
	fmt.Println()
	fmt.Printf("  %s = 可用  %s = 不可用\n", greenBold.Render("✓"), dimStyle.Render("○"))
}

// runToolInfo 显示指定工具的详细信息
func runToolInfo(name string) {
	tool.DiscoverBuiltin()

	registry := tool.GetRegistry()
	entry := registry.GetEntry(name)
	if entry == nil {
		fmt.Fprintf(os.Stderr, "%s\n", errorStyle.Render(fmt.Sprintf("未找到工具: %s", name)))
		os.Exit(1)
	}

	t := entry.Tool

	fmt.Println(titleStyle.Render(fmt.Sprintf("工具信息: %s", name)))
	fmt.Println(strings.Repeat("━", 60))

	if t.IsAvailable() {
		fmt.Printf("  %s %s\n", greenBold.Render("状态:"), greenBold.Render("可用"))
	} else {
		fmt.Printf("  %s %s\n", errorStyle.Render("状态:"), errorStyle.Render("不可用"))
	}
	fmt.Printf("  %s %s\n", dimStyle.Render("描述:"), t.Description())
	fmt.Printf("  %s %s\n", dimStyle.Render("工具集:"), t.Toolset())
	fmt.Printf("  %s %s\n", dimStyle.Render("图标:"), t.Emoji())
	fmt.Printf("  %s %d\n", dimStyle.Render("最大结果字符:"), entry.MaxResultChars)

	// 显示 Schema
	schema := t.Schema()
	if schema != nil && schema.Parameters != nil {
		fmt.Println()
		fmt.Println(greenBold.Render("  参数 Schema:"))
		paramsJSON, err := json.MarshalIndent(schema.Parameters, "    ", "  ")
		if err != nil {
			fmt.Printf("    (无法序列化: %v)\n", err)
		} else {
			fmt.Printf("    %s\n", string(paramsJSON))
		}
	}
}

// ───────────────────────────── session 子命令 ─────────────────────────────

func runSession() {
	if len(os.Args) < 3 {
		fmt.Println("用法: nexus session <subcommand>")
		fmt.Println("可用子命令:")
		fmt.Println("  list           - 列出最近的会话")
		fmt.Println("  search <query> - 搜索会话（使用 FTS5 全文搜索）")
		os.Exit(1)
	}

	switch os.Args[2] {
	case "list":
		runSessionList()
	case "search":
		if len(os.Args) < 4 {
			fmt.Fprintf(os.Stderr, "%s\n", errorStyle.Render("用法: nexus session search <query>"))
			os.Exit(1)
		}
		runSessionSearch(strings.Join(os.Args[3:], " "))
	default:
		fmt.Fprintf(os.Stderr, "%s\n", errorStyle.Render(fmt.Sprintf("未知子命令: %s", os.Args[2])))
		os.Exit(1)
	}
}

// runSessionList 列出最近的会话
func runSessionList() {
	store, err := state.NewStore(getDBPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", errorStyle.Render(fmt.Sprintf("打开数据库失败: %v", err)))
		os.Exit(1)
	}
	defer store.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sessions, err := store.ListRecentSessions(ctx, 20)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", errorStyle.Render(fmt.Sprintf("查询会话失败: %v", err)))
		os.Exit(1)
	}

	fmt.Println(titleStyle.Render("最近会话 (Top 20)"))
	fmt.Println(strings.Repeat("━", 70))

	if len(sessions) == 0 {
		fmt.Printf("  %s\n", dimStyle.Render("无会话记录"))
		fmt.Println()
		fmt.Println(dimStyle.Render("  提示: 使用 nexus chat 开始对话以创建会话"))
		return
	}

	for i, sess := range sessions {
		status := dimStyle.Render("○")
		if sess.EndedAt == 0 {
			status = greenBold.Render("●")
		}

		title := sess.Title
		if title == "" {
			title = "(无标题)"
		}

		fmt.Printf("  %2d. %s %s\n", i+1, status, title)
		fmt.Printf("      ID:     %s\n", sess.ID[:min(12, len(sess.ID))])
		fmt.Printf("      模型:   %s  来源: %s\n", sess.Model, sess.Source)
		fmt.Printf("      消息:   %d  工具调用: %d  Token: %d\n",
			sess.MessageCount, sess.ToolCallCount, sess.InputTokens+sess.OutputTokens)
		if sess.EstimatedCostUSD > 0 {
			fmt.Printf("      费用:   $%.4f\n", sess.EstimatedCostUSD)
		}
		fmt.Println()
	}

	fmt.Printf("  共 %d 个会话\n", len(sessions))
}

// runSessionSearch 使用 FTS5 搜索会话
func runSessionSearch(query string) {
	store, err := state.NewStore(getDBPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", errorStyle.Render(fmt.Sprintf("打开数据库失败: %v", err)))
		os.Exit(1)
	}
	defer store.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	fmt.Printf("搜索会话: %q\n\n", query)

	results, err := store.SearchMessages(ctx, query, 20)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", errorStyle.Render(fmt.Sprintf("搜索失败: %v", err)))
		os.Exit(1)
	}

	if len(results) == 0 {
		fmt.Printf("  %s\n", dimStyle.Render("未找到匹配的会话消息"))
		return
	}

	fmt.Printf("找到 %d 条匹配消息:\n", len(results))
	fmt.Println(strings.Repeat("━", 70))

	for i, r := range results {
		fmt.Printf("  %2d. [会话 %s]  排名: %.4f\n", i+1, r.SessionID[:min(8, len(r.SessionID))], r.Rank)
		// 显示内容片段
		snippet := r.Content
		if len(snippet) > 100 {
			snippet = snippet[:100] + "..."
		}
		fmt.Printf("      %s\n", snippet)
		fmt.Println()
	}
}

// getDBPath 返回状态数据库路径
func getDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "nexus.db"
	}
	return home + "/.nexus/nexus.db"
}

// ───────────────────────────── export 子命令 ─────────────────────────────

func runExport() {
	if len(os.Args) < 3 {
		fmt.Println("用法: nexus export <subcommand>")
		fmt.Println("可用子命令:")
		fmt.Println("  memory      - 导出 MEMORY.md 内容")
		fmt.Println("  config      - 导出当前配置（脱敏）")
		os.Exit(1)
	}

	switch os.Args[2] {
	case "memory":
		runExportMemory()
	case "config":
		runExportConfig()
	default:
		fmt.Fprintf(os.Stderr, "%s\n", errorStyle.Render(fmt.Sprintf("未知子命令: %s", os.Args[2])))
		os.Exit(1)
	}
}

// runExportMemory 导出 MEMORY.md 内容
func runExportMemory() {
	home, _ := os.UserHomeDir()
	memPath := fmt.Sprintf("%s/.nexus/MEMORY.md", home)

	data, err := os.ReadFile(memPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", errorStyle.Render(fmt.Sprintf("读取 MEMORY.md 失败: %v", err)))
		os.Exit(1)
	}

	// 直接输出原始内容（便于管道重定向）
	fmt.Print(string(data))
}

// runExportConfig 导出当前配置（JSON 格式，API Key 脱敏）
func runExportConfig() {
	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", errorStyle.Render(fmt.Sprintf("加载配置失败: %v", err)))
		os.Exit(1)
	}

	// 创建脱敏副本
	type ProviderConfigExport struct {
		BaseURL string `json:"base_url"`
		APIKey  string `json:"api_key"`
		APIMode string `json:"api_mode"`
		OAuthURL string `json:"oauth_url,omitempty"`
	}
	type ConfigExport struct {
		Agent     map[string]any                    `json:"agent"`
		Providers map[string]ProviderConfigExport   `json:"providers"`
		Gateway   map[string]any                    `json:"gateway"`
		Tools     map[string]any                    `json:"tools"`
		Memory    map[string]any                    `json:"memory"`
		Logging   map[string]any                    `json:"logging"`
		Approval  map[string]any                    `json:"approval"`
		Sandbox   map[string]any                    `json:"sandbox"`
		Cron      map[string]any                    `json:"cron"`
	}

	export := ConfigExport{
		Agent:     map[string]any{"model": cfg.Agent.Model, "provider": cfg.Agent.Provider, "max_tokens": cfg.Agent.MaxTokens, "max_iterations": cfg.Agent.MaxIterations, "fallback_model": cfg.Agent.FallbackModel},
		Providers: make(map[string]ProviderConfigExport),
		Gateway:   map[string]any{"enabled": cfg.Gateway.Enabled},
		Tools:     map[string]any{"result_max_chars": cfg.Tools.ResultMaxChars, "web_search_backend": cfg.Tools.WebSearchBackend},
		Memory:    map[string]any{"memory_max_chars": cfg.Memory.MemoryMaxChars, "user_max_chars": cfg.Memory.UserMaxChars},
		Logging:   map[string]any{"level": cfg.Logging.Level, "format": cfg.Logging.Format},
		Approval:  map[string]any{"mode": cfg.Approval.Mode, "cron_mode": cfg.Approval.CronMode},
		Sandbox:   map[string]any{"backend": cfg.Sandbox.Backend, "timeout_secs": cfg.Sandbox.TimeoutSecs},
		Cron:      map[string]any{"enabled": cfg.Cron.Enabled, "max_parallel_jobs": cfg.Cron.MaxParallelJobs, "tick_interval_secs": cfg.Cron.TickIntervalSecs},
	}

	for name, p := range cfg.Providers {
		export.Providers[name] = ProviderConfigExport{
			BaseURL:  p.BaseURL,
			APIKey:   maskAPIKey(p.APIKey),
			APIMode:  p.APIMode,
			OAuthURL: p.OAuthURL,
		}
	}

	jsonData, err := json.MarshalIndent(export, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", errorStyle.Render(fmt.Sprintf("序列化配置失败: %v", err)))
		os.Exit(1)
	}

	fmt.Println(string(jsonData))
}

// ───────────────────────────── import 子命令 ─────────────────────────────

func runImport() {
	if len(os.Args) < 3 {
		fmt.Println("用法: nexus import <subcommand>")
		fmt.Println("可用子命令:")
		fmt.Println("  skills <url>  - 从 URL 安装技能")
		os.Exit(1)
	}

	switch os.Args[2] {
	case "skills":
		if len(os.Args) < 4 {
			fmt.Fprintf(os.Stderr, "%s\n", errorStyle.Render("用法: nexus import skills <url>"))
			os.Exit(1)
		}
		runImportSkills(os.Args[3])
	default:
		fmt.Fprintf(os.Stderr, "%s\n", errorStyle.Render(fmt.Sprintf("未知子命令: %s", os.Args[2])))
		os.Exit(1)
	}
}

// runImportSkills 从 URL 安装技能
func runImportSkills(url string) {
	fmt.Printf("正在从 URL 安装技能: %s\n", url)

	// 确定目标目录
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", errorStyle.Render(fmt.Sprintf("获取用户主目录失败: %v", err)))
		os.Exit(1)
	}
	skillsDir := home + "/.nexus/skills"

	// 确保技能目录存在
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", errorStyle.Render(fmt.Sprintf("创建技能目录失败: %v", err)))
		os.Exit(1)
	}

	// 从 URL 提取技能名称
	skillName := extractSkillName(url)
	if skillName == "" {
		skillName = fmt.Sprintf("skill_%d", time.Now().Unix())
	}

	targetDir := skillsDir + "/" + skillName

	// 检查是否已存在
	if _, err := os.Stat(targetDir); err == nil {
		fmt.Fprintf(os.Stderr, "%s\n", errorStyle.Render(fmt.Sprintf("技能 %q 已存在，请先删除后重试", skillName)))
		os.Exit(1)
	}

	slog.Info("开始安装技能", "url", url, "name", skillName, "target", targetDir)
	fmt.Printf("  技能名称: %s\n", skillName)
	fmt.Printf("  目标目录: %s\n", targetDir)
	fmt.Println()

	// 尝试使用 git clone（如果 URL 是 git 仓库）
	if isGitURL(url) {
		if err := cloneGitRepo(url, targetDir); err != nil {
			fmt.Fprintf(os.Stderr, "%s\n", errorStyle.Render(fmt.Sprintf("克隆仓库失败: %v", err)))
			os.Exit(1)
		}
		fmt.Println(greenBold.Render("  ✓ 技能安装成功"))
		fmt.Printf("  提示: 在 config.yaml 中将 %q 添加到 skills.external_dirs 以启用\n", targetDir)
		return
	}

	// 否则尝试 HTTP 下载（假设是 SKILL.md 文件的直接链接）
	if err := downloadSkillFile(url, targetDir); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", errorStyle.Render(fmt.Sprintf("下载技能文件失败: %v", err)))
		os.Exit(1)
	}

	fmt.Println(greenBold.Render("  ✓ 技能文件下载成功"))
	fmt.Printf("  提示: 在 config.yaml 中将 %q 添加到 skills.external_dirs 以启用\n", targetDir)
}

// extractSkillName 从 URL 提取技能名称
func extractSkillName(url string) string {
	// 处理 git URL: git@github.com:user/repo.git
	if strings.HasPrefix(url, "git@") {
		// 提取 repo 部分
		parts := strings.Split(url, "/")
		if len(parts) >= 2 {
			name := parts[len(parts)-1]
			name = strings.TrimSuffix(name, ".git")
			return name
		}
	}

	// 处理 HTTPS URL
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		// 去除查询参数
		url = strings.Split(url, "?")[0]
		// 去除尾部斜杠
		url = strings.TrimRight(url, "/")

		parts := strings.Split(url, "/")
		if len(parts) >= 2 {
			last := parts[len(parts)-1]
			if last == "" && len(parts) >= 3 {
				last = parts[len(parts)-2]
			}
			// 去除 .git 或 .md 后缀
			last = strings.TrimSuffix(last, ".git")
			last = strings.TrimSuffix(last, ".md")
			return last
		}
	}

	return ""
}

// isGitURL 判断 URL 是否为 git 仓库地址
func isGitURL(url string) bool {
	return strings.HasPrefix(url, "git@") ||
		strings.HasSuffix(url, ".git") ||
		(strings.HasPrefix(url, "https://github.com") && !strings.HasSuffix(url, ".md"))
}

// cloneGitRepo 使用 git clone 克隆仓库
func cloneGitRepo(url, targetDir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 检查 git 是否在 PATH 中
	if _, err := gitLookPath("git"); err != nil {
		return fmt.Errorf("系统未安装 git，无法克隆仓库")
	}

	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", url, targetDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone 失败: %v\n%s", err, string(output))
	}

	slog.Info("git clone 成功", "url", url, "target", targetDir)
	return nil
}

// downloadSkillFile 从 HTTP URL 下载技能文件
func downloadSkillFile(url, targetDir string) error {
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("创建目录失败: %v", err)
	}

	// 使用 net/http 下载
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("创建请求失败: %v", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("下载失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP 错误: %s", resp.Status)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("读取响应失败: %v", err)
	}

	// 保存到 SKILL.md
	targetPath := targetDir + "/SKILL.md"
	if err := os.WriteFile(targetPath, data, 0644); err != nil {
		return fmt.Errorf("写入文件失败: %v", err)
	}

	slog.Info("技能文件下载成功", "url", url, "path", targetPath)
	return nil
}

// gitLookPath 包装 os/exec.LookPath 以便测试
func gitLookPath(name string) (string, error) {
	return exec.LookPath(name)
}
