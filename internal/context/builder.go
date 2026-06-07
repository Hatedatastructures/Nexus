// Package context 提供上下文工程能力。
// 包含系统提示词构建器 (Builder) 和上下文压缩器 (Compressor)。
package context

import (
	"context"
	"log/slog"
	"os"
	"runtime"
	"strings"

	"nexus-agent/internal/llm"
	"nexus-agent/internal/memory"
	"nexus-agent/internal/skill"
)

// ───────────────────────────── 构建选项 ─────────────────────────────

// BuildOptions 定义系统提示词的构建参数。
// 所有字段均为可选，Builder 根据可用数据拼接最终提示词。
type BuildOptions struct {
	SystemMessage string   // 用户/网关提供的额外系统消息 (附加在最后)
	ContextFiles  []string // 上下文文件路径 (AGENTS.md 等，可覆盖默认搜索)
	SessionID     string   // 会话唯一标识
	Model         string   // 当前使用的模型名称
}

// ───────────────────────────── 构建器 ─────────────────────────────

// Builder 负责组装系统提示词。
// 拼接顺序: 身份 → 工具指导 → 记忆 → 技能 → 上下文文件 → 环境提示
type Builder struct {
	identity      string          // SOUL.md 内容
	platform      string          // 平台标识
	memoryManager *memory.Manager // 记忆管理器
	skillManager  *skill.Manager  // 技能管理器
}

// NewBuilder 创建系统提示词构建器
func NewBuilder(identity, platform string, memMgr *memory.Manager, skillMgr *skill.Manager) *Builder {
	return &Builder{
		identity:      identity,
		platform:      platform,
		memoryManager: memMgr,
		skillManager:  skillMgr,
	}
}

// Build 组装完整的系统提示词。
//
// 组装顺序严格按照以下步骤:
//  1. Agent 身份 (SOUL.md 内容)
//  2. 工具使用行为指导 (硬编码)
//  3. 持久记忆 (调用 memoryManager.SystemPromptBlock())
//  4. 技能索引 (调用 skillManager.GetActiveSkillsIndex())
//  5. 上下文文件 (AGENTS.md / CLAUDE.md 等)
//  6. 时间戳 + Session ID + 模型信息
//  7. 平台特定提示
//
// 返回完整拼接的系统提示词字符串。
func (b *Builder) Build(ctx context.Context, opts *BuildOptions) (string, error) {
	var sb strings.Builder

	// 1. Agent 身份
	if b.identity != "" {
		sb.WriteString(b.identity)
		sb.WriteString("\n\n")
	}

	// 2. 工具使用行为指导
	sb.WriteString(b.buildToolGuidance())

	// Static/dynamic boundary: everything above is cacheable
	sb.WriteString("\n\n__SYSTEM_PROMPT_DYNAMIC_BOUNDARY__\n\n")

	// 3. 用户/网关的系统消息
	if opts != nil && opts.SystemMessage != "" {
		sb.WriteString("\n\n")
		// 安全扫描: 检测用户提供的系统消息中的 injection 模式
		msg := opts.SystemMessage
		if threats := scanContextContent(msg); len(threats) > 0 {
			slog.Warn("system message contains potential prompt injection pattern",
				"session_id", opts.SessionID, "threats", strings.Join(threats, ", "))
			cleaned, _ := sanitizeContextContent(msg)
			msg = cleaned
		}
		sb.WriteString(msg)
	}

	// 4. 持久记忆块
	if b.memoryManager != nil {
		memBlock := b.memoryManager.SystemPromptBlock()
		if memBlock != "" {
			sb.WriteString("\n\n")
			sb.WriteString(memBlock)
		}
	}

	// 5. 技能索引
	if b.skillManager != nil {
		skillsIndex := b.skillManager.GetActiveSkillsIndex()
		if skillsIndex != "" {
			sb.WriteString("\n\n")
			sb.WriteString(skillsIndex)
		}
		// 追加详细技能列表
		skillsPrompt := b.buildSkillsPrompt()
		if skillsPrompt != "" {
			sb.WriteString(skillsPrompt)
		}
	}

	// 6. 上下文文件
	if opts != nil && len(opts.ContextFiles) > 0 {
		// 使用显式指定的上下文文件
		for _, path := range opts.ContextFiles {
			// 读取外部文件路径
			sb.WriteString(b.readContextFile(path))
		}
	} else {
		// 自动搜索上下文文件
		ctxFiles := b.loadContextFiles()
		if ctxFiles != "" {
			sb.WriteString(ctxFiles)
		}
	}

	// 6.5. Git 上下文自动注入
	if gitCtx := DiscoverGitContext(""); gitCtx != nil {
		sb.WriteString(gitCtx.Render())
	}

	// 7. 时间戳 + Session ID + 模型信息
	sb.WriteString(b.buildEnvironmentInfo(opts))

	// 7.5. 运行时配置
	sb.WriteString(b.buildRuntimeConfigSection())

	// 8. 平台特定提示
	sb.WriteString(b.buildPlatformHint())

	systemPrompt := sb.String()

	slog.Debug("system prompt build completed",
		"session_id", maybeStr(opts, func(o *BuildOptions) string { return o.SessionID }),
		"length", len(systemPrompt),
	)

	return systemPrompt, nil
}

// ───────────────────────────── 上下文文件读取 ─────────────────────────────

// readContextFile 读取指定路径的上下文文件，返回格式化内容。
func (b *Builder) readContextFile(path string) string {
	content, err := readFile(path)
	if err != nil {
		slog.Warn("failed to read context file", "path", path, "err", err)
		return ""
	}
	return formatContextFileContent(fileName(path), content)
}

// ───────────────────────────── Shell 检测 ─────────────────────────────

// detectShell 从环境变量推断当前 shell（保留向后兼容）。
func detectShell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	if s := os.Getenv("COMSPEC"); s != "" {
		return s
	}
	return ""
}

// detectShellWithHint 返回 shell 路径及语法提示。
// 在 Windows 上使用 bash 时提示 Unix 语法；其他情况附带平台对应的语法说明。
func detectShellWithHint() string {
	shell := detectShell()
	if shell == "" {
		return ""
	}

	// 检测是否为 Unix-like shell（bash/zsh/fish/sh）
	shellLower := strings.ToLower(shell)
	isUnixShell := strings.Contains(shellLower, "bash") ||
		strings.Contains(shellLower, "zsh") ||
		strings.Contains(shellLower, "fish") ||
		strings.Contains(shellLower, "/sh")

	if isUnixShell && runtime.GOOS == "windows" {
		return shell + " (使用 Unix shell 语法——如 /dev/null 而非 NUL，路径使用正斜杠)"
	}
	if isUnixShell {
		return shell
	}
	// Windows cmd/powershell
	if runtime.GOOS == "windows" {
		return shell
	}
	return shell
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

// fileName 从文件路径提取文件名。
func fileName(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[i+1:]
		}
	}
	return path
}

// maybeStr 安全地从可选的 opts 指针取值。
func maybeStr(opts *BuildOptions, fn func(*BuildOptions) string) string {
	if opts == nil {
		return ""
	}
	return fn(opts)
}

// ───────────────────────────── 文件读取 ─────────────────────────────

// readFile 读取文件内容 (平台无关的封装)。
// 这是一个包级函数，供 builder_context.go 和 builder.go 共用。
func readFile(path string) (string, error) {
	data, err := readFileBytes(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// readFileBytes 使用 os.ReadFile 读取文件字节。
// 这是与操作系统交互的唯一入口，便于测试时 mock。
var readFileBytes = os.ReadFile

// ───────────────────────────── 压缩器 ─────────────────────────────

// Compressor 负责在 token 用量超限时压缩对话历史。
// 保留头部 N 条消息和尾部 token 预算内的消息，用 LLM 总结中间部分。
type Compressor struct {
	protectFirstN    int          // 头部保护消息数
	tailTokenBudget  int          // 尾部保护 token 预算
	auxProvider      llm.Provider // 辅助 LLM 提供者 (用于生成总结)
	thresholdPercent float64      // 压缩触发阈值百分比 (默认 0.75)

	// Anti-thrash 保护: 追踪连续无效压缩次数
	consecutiveSummaries int // 连续未显著减少 token 的压缩次数
	antiThrashCooldown   int // 反抖动冷却计数器: 每次调用 ShouldCompress 递减，归零后恢复

	// summaryModel 用于生成总结的模型名称 (默认 "claude-sonnet-4-20250514")
	summaryModel string

	// SummaryTemplate 自定义摘要模板 (可选)。
	// 非空时替代默认的结构化模板，用于生成上下文压缩摘要。
	// 模板中可使用 {{.ToolCalls}}、{{.Decisions}}、{{.PendingTasks}}、{{.CurrentContext}} 占位符。
	SummaryTemplate string
}

// NewCompressor 创建上下文压缩器
func NewCompressor(protectFirstN, tailTokenBudget int) *Compressor {
	if protectFirstN <= 0 {
		protectFirstN = 3
	}
	if tailTokenBudget <= 0 {
		tailTokenBudget = 20000
	}
	return &Compressor{
		protectFirstN:    protectFirstN,
		tailTokenBudget:  tailTokenBudget,
		thresholdPercent: 0.75,
		summaryModel:     "claude-sonnet-4-20250514",
	}
}

// SetAuxProvider 设置辅助 LLM 提供者 (用于总结生成)。
// 压缩器需要一个 LLM 提供者来生成中间对话的总结。
// 如果未设置，Compress 将使用降级策略 (无总结的静默压缩)。
func (c *Compressor) SetAuxProvider(p llm.Provider) {
	c.auxProvider = p
}

// TailTokenBudget 返回尾部保护 token 预算。
func (c *Compressor) TailTokenBudget() int {
	return c.tailTokenBudget
}

// SetThresholdPercent 设置压缩触发阈值百分比 (默认 0.75)。
// 值越大压缩触发越延迟，值越小压缩触发越频繁。
func (c *Compressor) SetThresholdPercent(pct float64) {
	if pct > 0 && pct <= 1 {
		c.thresholdPercent = pct
	}
}
