// Package agent 提供 AI 代理的核心实现。
// ThinkScrubber 从流式输出中分离思考内容 (<think> / <|thinking|> / <scratchpad> 标签)。
package agent

import (
	"strings"
	"unicode/utf8"
)

// ───────────────────────────── 思考标签配置 ─────────────────────────────

// thinkTagConfig 描述一种思考标签的开始/结束标记。
type thinkTagConfig struct {
	Open  string // 开始标签, 如 "<think>"
	Close string // 结束标签, 如 "</think>"
}

// providerThinkTags 根据 provider 名称返回对应的标签配置。
// 未知 provider 使用 generic 格式 (<scratchpad>)。
var providerThinkTags = map[string]thinkTagConfig{
	"anthropic": {Open: "<think>", Close: "</think>"},
	"deepseek":  {Open: "<|thinking|>", Close: "<|/thinking|>"},
	"generic":   {Open: "<scratchpad>", Close: "</scratchpad>"},
}

// ───────────────────────────── 状态机状态 ─────────────────────────────

// scrubState 表示状态机的当前状态。
type scrubState int

const (
	// stateIdle 空闲状态: 累积用户可见文本, 扫描开始标签首字符。
	stateIdle scrubState = iota

	// stateInTag 正在匹配开始标签: 逐字符比对 openTag, 缓冲已匹配前缀。
	stateInTag

	// stateInContent 思考内容中: 累积思考文本, 扫描结束标签首字符。
	stateInContent

	// stateOutTag 正在匹配结束标签: 逐字符比对 closeTag, 缓冲已匹配前缀。
	stateOutTag
)

// ───────────────────────────── ThinkScrubber ─────────────────────────────

// ThinkScrubber 从流式输出中分离思考内容。
// 调用方逐块调用 Scrub(delta), 获取用户可见文本;
// 流结束后通过 ThinkContent() 获取完整的思考内容。
type ThinkScrubber struct {
	provider     string       // 提供者名称, 决定标签格式
	openTag      string       // 开始标签
	closeTag     string       // 结束标签
	buffer       string       // 部分标签累积缓冲 (匹配失败时回吐)
	state        scrubState   // 当前状态
	thinkContent strings.Builder // 捕获的思考内容
	onThink      func(string) // 思考内容增量回调 (可选)
}

// NewThinkScrubber 创建 ThinkScrubber 实例。
// provider 决定标签格式 (anthropic / deepseek / generic);
// onThink 回调在捕获到思考内容增量时触发 (可为 nil)。
func NewThinkScrubber(provider string, onThink func(string)) *ThinkScrubber {
	cfg, ok := providerThinkTags[provider]
	if !ok {
		cfg = providerThinkTags["generic"]
	}

	return &ThinkScrubber{
		provider: provider,
		openTag:  cfg.Open,
		closeTag: cfg.Close,
		state:    stateIdle,
		onThink:  onThink,
	}
}

// Scrub 处理一个流式 delta, 返回用户可见的文本。
// 思考标签及其内容被过滤, 不会出现在返回值中。
func (s *ThinkScrubber) Scrub(delta string) string {
	var out strings.Builder

	for _, ch := range delta {
		result := s.processChar(ch)
		out.WriteString(result)
	}

	return out.String()
}

// ThinkContent 返回捕获的完整思考内容。
func (s *ThinkScrubber) ThinkContent() string {
	return s.thinkContent.String()
}

// Reset 重置状态机到初始状态, 清空所有缓冲。
func (s *ThinkScrubber) Reset() {
	s.buffer = ""
	s.state = stateIdle
	s.thinkContent.Reset()
}

// ───────────────────────────── 状态机核心 ─────────────────────────────


// firstRune returns the first rune of a string.
func firstRune(s string) rune {
	for _, r := range s {
		return r
	}
	return 0
}

// processChar 处理单个字符, 返回用户可见的文本 (可能为空)。
func (s *ThinkScrubber) processChar(ch rune) string {
	switch s.state {
	case stateIdle:
		return s.processIdle(ch)
	case stateInTag:
		return s.processInTag(ch)
	case stateInContent:
		return s.processInContent(ch)
	case stateOutTag:
		return s.processOutTag(ch)
	default:
		return string(ch)
	}
}

// processIdle 空闲状态: 检测开始标签的首字符, 否则输出字符。
func (s *ThinkScrubber) processIdle(ch rune) string {
	s.buffer = string(ch)

	// 检查是否匹配开始标签的首字符
	if firstRune(s.openTag) == ch {
		if len(s.openTag) == 1 {
			// 标签只有一个字符 (极端情况), 直接进入思考内容
			s.buffer = ""
			s.state = stateInContent
			return ""
		}
		s.state = stateInTag
		return ""
	}

	// 不匹配, 输出字符
	result := s.buffer
	s.buffer = ""
	return result
}

// processInTag 匹配开始标签: 逐字符比对, 失败则回吐缓冲。
func (s *ThinkScrubber) processInTag(ch rune) string {
	s.buffer += string(ch)
	pos := len(s.buffer)

	// 检查当前缓冲是否仍匹配开始标签前缀
	if pos <= utf8.RuneCountInString(s.openTag) && string([]rune(s.openTag)[:pos]) == s.buffer {
		if pos == len(s.openTag) {
			// 完整匹配开始标签, 切换到思考内容状态
			s.buffer = ""
			s.state = stateInContent
			return ""
		}
		// 继续匹配下一个字符
		return ""
	}

	// 匹配失败: 回吐缓冲中的所有字符
	result := s.buffer
	s.buffer = ""
	s.state = stateIdle
	return result
}

// processInContent 思考内容状态: 累积思考文本, 检测结束标签首字符。
func (s *ThinkScrubber) processInContent(ch rune) string {
	s.buffer = string(ch)

	// 检查是否匹配结束标签的首字符
	if firstRune(s.closeTag) == ch {
		if len(s.closeTag) == 1 {
			// 标签只有一个字符, 直接结束
			s.buffer = ""
			s.state = stateIdle
			return ""
		}
		s.state = stateOutTag
		return ""
	}

	// 思考内容字符, 累积到 thinkContent
	s.thinkContent.WriteRune(ch)
	if s.onThink != nil {
		s.onThink(string(ch))
	}
	s.buffer = ""
	return ""
}

// processOutTag 匹配结束标签: 逐字符比对, 失败则将缓冲作为思考内容回吐。
func (s *ThinkScrubber) processOutTag(ch rune) string {
	s.buffer += string(ch)
	pos := len(s.buffer)

	// 检查当前缓冲是否仍匹配结束标签前缀
	if pos <= utf8.RuneCountInString(s.closeTag) && string([]rune(s.closeTag)[:pos]) == s.buffer {
		if pos == len(s.closeTag) {
			// 完整匹配结束标签, 回到空闲状态
			s.buffer = ""
			s.state = stateIdle
			return ""
		}
		// 继续匹配下一个字符
		return ""
	}

	// 匹配失败: 缓冲内容实际上是思考内容, 累积并通知
	s.thinkContent.WriteString(s.buffer)
	if s.onThink != nil {
		s.onThink(s.buffer)
	}
	s.buffer = ""
	s.state = stateInContent
	return ""
}
