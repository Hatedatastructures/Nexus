// Package context 提供上下文工程能力。
// 包含系统提示词构建器 (Builder) 和上下文压缩器 (Compressor)。
package context

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

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

	// 3. 用户/网关的系统消息
	if opts != nil && opts.SystemMessage != "" {
		sb.WriteString("\n\n")
		// 安全扫描: 检测用户提供的系统消息中的 injection 模式
		msg := opts.SystemMessage
		if threats := scanContextContent(msg); len(threats) > 0 {
			slog.Warn("系统消息包含潜在的 prompt injection 模式",
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

	// 7. 时间戳 + Session ID + 模型信息
	sb.WriteString(b.buildEnvironmentInfo(opts))

	// 8. 平台特定提示
	sb.WriteString(b.buildPlatformHint())

	systemPrompt := sb.String()

	slog.Debug("系统提示词构建完成",
		"session_id", maybeStr(opts, func(o *BuildOptions) string { return o.SessionID }),
		"length", len(systemPrompt),
	)

	return systemPrompt, nil
}

// buildToolGuidance 返回工具使用行为指导文本。
// 这是一段硬编码的指导，帮助模型正确使用工具系统。
func (b *Builder) buildToolGuidance() string {
	return `## 工具使用行为指导

你可以通过工具调用来完成用户请求的任务。使用工具时请遵循:

1. 仔细阅读工具描述和参数定义
2. 一次可以调用多个工具 (并行执行)
3. 工具调用后，等待结果再决定下一步
4. 终端命令需要审批的会被自动检查
5. 文件编辑使用精确的替换操作

当你完成用户任务后，提供清晰的总结。
如果遇到错误，解释错误原因并提供替代方案。
`
}

// buildEnvironmentInfo 构建环境信息块 (时间戳、会话、模型)。
func (b *Builder) buildEnvironmentInfo(opts *BuildOptions) string {
	var sb strings.Builder
	sb.WriteString("\n\n## 运行环境\n\n")

	// 时间戳
	now := time.Now()
	sb.WriteString(fmt.Sprintf("- 当前时间: %s\n", now.Format("2006-01-02 15:04:05 UTC")))

	// 会话 ID
	if opts != nil && opts.SessionID != "" {
		sb.WriteString(fmt.Sprintf("- 会话 ID: %s\n", opts.SessionID))
	}

	// 模型信息
	if opts != nil && opts.Model != "" {
		sb.WriteString(fmt.Sprintf("- 模型: %s\n", opts.Model))
	}

	// 平台
	if b.platform != "" {
		sb.WriteString(fmt.Sprintf("- 平台: %s\n", b.platform))
	}

	return sb.String()
}

// buildPlatformHint 返回平台特定的格式提示。
// 不同平台 (CLI / Telegram / Discord 等) 有不同的格式和长度限制。
func (b *Builder) buildPlatformHint() string {
	switch strings.ToLower(b.platform) {
	case "telegram":
		return "\n\n## 平台提示 (Telegram)\n\n" +
			"- 使用 MarkdownV2 格式化回复 (仅支持: *bold*, _italic_, `code`, ```code block```)\n" +
			"- 回复须在 4096 字符内，超长分段发送\n" +
			"- 避免使用 HTML 标签\n"

	case "discord":
		return "\n\n## 平台提示 (Discord)\n\n" +
			"- 使用 Discord Markdown 格式化 (支持: **bold**, *italic*, `code`, ```code block```)\n" +
			"- 单条消息限制 2000 字符\n" +
			"- 长回复使用多条消息或附件\n"

	case "slack":
		return "\n\n## 平台提示 (Slack)\n\n" +
			"- 使用 Slack mrkdwn 格式化\n" +
			"- 支持 Block Kit 结构\n" +
			"- 代码块使用 ``` 包裹\n"

	case "cli":
		return "\n\n## 平台提示 (CLI)\n\n" +
			"- 使用纯文本或终端 ANSI 颜色 (如需要)\n" +
			"- 可以使用 Markdown 格式 (渲染为终端样式)\n" +
			"- 无长度限制\n"

	default:
		return "\n\n## 平台提示\n\n- 使用标准 Markdown 格式化回复\n"
	}
}

// readContextFile 读取指定路径的上下文文件，返回格式化内容。
func (b *Builder) readContextFile(path string) string {
	content, err := readFile(path)
	if err != nil {
		slog.Warn("无法读取上下文文件", "path", path, "err", err)
		return ""
	}
	return formatContextFileContent(fileName(path), content)
}

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
