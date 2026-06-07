// Package batch 提供轨迹压缩功能。
// 通过 LLM 摘要中间对话轮次来压缩轨迹长度，保留首尾轮次和训练信号。
package batch

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"nexus-agent/internal/llm"
)

// ───────────────────────────── 数据结构 ─────────────────────────────

// CompressionConfig 轨迹压缩配置。
type CompressionConfig struct {
	ProtectFirstN        int           // 保护前 N 轮 (默认 4: system + human + gpt + tool)
	ProtectLastN         int           // 保护后 N 轮 (默认 2)
	MaxSummaryLen        int           // 摘要最大字符数 (默认 2000)
	Concurrency          int           // 并发压缩数 (默认 3)
	PerTrajectoryTimeout time.Duration // 每轨迹超时 (默认 5 分钟)
}

// DefaultCompressionConfig 返回默认配置。
func DefaultCompressionConfig() CompressionConfig {
	return CompressionConfig{
		ProtectFirstN:        4,
		ProtectLastN:         2,
		MaxSummaryLen:        2000,
		Concurrency:          3,
		PerTrajectoryTimeout: 5 * time.Minute,
	}
}

// Compressor 轨迹压缩器。
type Compressor struct {
	cfg      CompressionConfig
	provider llm.Provider
	sem      chan struct{}
}

// NewCompressor 创建轨迹压缩器。
func NewCompressor(provider llm.Provider, cfg CompressionConfig) *Compressor {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 3
	}
	return &Compressor{
		cfg:      cfg,
		provider: provider,
		sem:      make(chan struct{}, cfg.Concurrency),
	}
}

// ───────────────────────────── 压缩函数 ─────────────────────────────

// CompressTrajectory 压缩单条轨迹。
func (c *Compressor) CompressTrajectory(ctx context.Context, turns []TrajectoryTurn) ([]TrajectoryTurn, error) {
	if len(turns) <= c.cfg.ProtectFirstN+c.cfg.ProtectLastN {
		return turns, nil // 不需要压缩
	}

	// 分离保护轮次和中间轮次
	protected := turns[:c.cfg.ProtectFirstN]
	middle := turns[c.cfg.ProtectFirstN : len(turns)-c.cfg.ProtectLastN]
	tail := turns[len(turns)-c.cfg.ProtectLastN:]

	// 生成中间轮次摘要
	summary, err := c.summarizeMiddle(ctx, middle)
	if err != nil {
		slog.Warn("trajectory compression failed, keeping original", "err", err)
		return turns, nil
	}

	// 组装压缩后的轨迹
	compressed := make([]TrajectoryTurn, 0, len(protected)+1+len(tail))
	compressed = append(compressed, protected...)
	compressed = append(compressed, TrajectoryTurn{
		From:  "system",
		Value: fmt.Sprintf("[对话摘要: %s]", summary),
	})
	compressed = append(compressed, tail...)

	return compressed, nil
}

// CompressBatch 并行压缩多条轨迹。
func (c *Compressor) CompressBatch(ctx context.Context, trajectories [][]TrajectoryTurn) ([][]TrajectoryTurn, error) {
	results := make([][]TrajectoryTurn, len(trajectories))
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	for i, turns := range trajectories {
		wg.Add(1)
		go func(idx int, t []TrajectoryTurn) {
			defer wg.Done()

			// 信号量限流
			c.sem <- struct{}{}
			defer func() { <-c.sem }()

			// 每轨迹超时
			trajCtx, cancel := context.WithTimeout(ctx, c.cfg.PerTrajectoryTimeout)
			defer cancel()

			compressed, err := c.CompressTrajectory(trajCtx, t)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				results[idx] = t // 失败时保留原始
				return
			}
			results[idx] = compressed
		}(i, turns)
	}

	wg.Wait()
	return results, firstErr
}

// summarizeMiddle 使用 LLM 生成中间轮次的摘要。
func (c *Compressor) summarizeMiddle(ctx context.Context, middle []TrajectoryTurn) (string, error) {
	if c.provider == nil {
		return summarizeSimple(middle), nil
	}

	// 构建摘要提示
	var conversation strings.Builder
	for _, turn := range middle {
		fmt.Fprintf(&conversation, "[%s]: %s\n\n", turn.From, truncateStr(turn.Value, 500))
	}

	prompt := fmt.Sprintf(`请将以下对话压缩为简洁摘要（不超过 %d 字符）。
保留关键信息：用户意图、工具调用结果、重要决策。
只输出摘要文本。

%s`, c.cfg.MaxSummaryLen, conversation.String())

	req := &llm.ChatRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: prompt},
		},
		MaxTokens: 500,
	}

	resp, err := c.provider.CreateChatCompletion(ctx, req)
	if err != nil {
		// 回退到简单摘要
		return summarizeSimple(middle), nil
	}

	summary := strings.TrimSpace(resp.Content)
	if len(summary) > c.cfg.MaxSummaryLen {
		summary = summary[:c.cfg.MaxSummaryLen]
	}
	return summary, nil
}

// summarizeSimple 不使用 LLM 的简单摘要（提取关键信息）。
func summarizeSimple(middle []TrajectoryTurn) string {
	var parts []string
	for _, turn := range middle {
		if turn.ToolUse != "" {
			parts = append(parts, fmt.Sprintf("工具 %s 被调用", turn.ToolUse))
		} else if turn.From == "human" {
			content := turn.Value
			if len(content) > 100 {
				content = content[:100] + "..."
			}
			parts = append(parts, fmt.Sprintf("用户: %s", content))
		} else if turn.From == "gpt" {
			content := turn.Value
			if len(content) > 100 {
				content = content[:100] + "..."
			}
			parts = append(parts, fmt.Sprintf("助手: %s", content))
		}
	}
	return strings.Join(parts, "; ")
}
