// Package memory 提供流式上下文清洗器，用于过滤流式输出中的敏感标签。
package memory

import (
	"regexp"
	"strings"
)

// ───────────────────────────── 标签常量 ─────────────────────────────

const (
	openTag  = "<memory-context>"
	closeTag = "</memory-context>"
)

// ───────────────────────────── 流式上下文清洗器 ─────────────────────────────

// StreamingScrubber 对流式 LLM 输出中的 <memory-context> 标签进行有状态过滤。
//
// 因为 <memory-context> 标签可能被分割在两个连续的 delta 之间
// (例如 "<memory-conte" 在一个 delta 中，"xt>" 在下一个 delta 中)，
// 简单的正则表达式无法处理分块边界。
// 本清洗器使用一个轻量状态机来按流式 delta 进行过滤。
//
// 使用方式:
//
//	scrubber := NewStreamingScrubber()
//	for delta := range stream {
//	    clean := scrubber.Process(delta)
//	    if clean != "" {
//	        emit(clean)
//	    }
//	}
//	// 流结束时的尾随缓冲
//	tail := scrubber.Flush()
//	if tail != "" {
//	    emit(tail)
//	}
type StreamingScrubber struct {
	inSpan bool   // 是否在 <memory-context> 内部
	buf    string // 保留的部分标签尾缓冲
}

// NewStreamingScrubber 创建新的流式清洗器实例。
func NewStreamingScrubber() *StreamingScrubber {
	return &StreamingScrubber{}
}

// Reset 重置清洗器状态，以便在新回合中复用。
func (s *StreamingScrubber) Reset() {
	s.inSpan = false
	s.buf = ""
}

// Process 处理流式 delta 文本，返回过滤后的安全文本。
//
// <memory-context> 内部的所有内容 (包括标签本身) 都会被丢弃。
// 跨 delta 边界的部分标签会被内部缓冲，并在接收到剩余部分时解析。
//
// 返回空字符串表示没有可输出的安全文本 (所有内容或被过滤，或正被缓冲)。
func (s *StreamingScrubber) Process(delta string) string {
	if delta == "" {
		return ""
	}

	buf := s.buf + delta
	s.buf = ""
	var out []string
	lowerBuf := strings.ToLower(buf)

	for len(buf) > 0 {
		if s.inSpan {
			// 在标签内 — 查找关闭标签
			idx := strings.Index(lowerBuf, closeTag)
			if idx == -1 {
				// 关闭标签不在当前块中 — 保留潜在的部分关闭标签尾部
				held := s.maxPartialSuffix(buf, closeTag)
				s.buf = ""
				if held > 0 {
					s.buf = buf[len(buf)-held:]
				}
				return strings.Join(out, "")
			}
			// 找到关闭标签 — 跳过标签内容 + 标签本身，继续
			buf = buf[idx+len(closeTag):]
			lowerBuf = lowerBuf[idx+len(closeTag):]
			s.inSpan = false
		} else {
			// 不在标签内 — 查找开启标签
			idx := strings.Index(lowerBuf, openTag)
			if idx == -1 {
				// 未找到开启标签 — 保留潜在的部分开启标签尾部
				held := s.maxPartialSuffix(buf, openTag)
				if held > 0 {
					out = append(out, buf[:len(buf)-held])
					s.buf = buf[len(buf)-held:]
				} else {
					out = append(out, buf)
				}
				return strings.Join(out, "")
			}
			// 在标签前输出文本，进入标签内
			if idx > 0 {
				out = append(out, buf[:idx])
			}
			buf = buf[idx+len(openTag):]
			lowerBuf = lowerBuf[idx+len(openTag):]
			s.inSpan = true
		}
	}

	return strings.Join(out, "")
}

// Flush 在流结束时刷新缓冲的数据。
//
// 如果仍然在未终止的标签内，剩余内容将被丢弃
// (泄漏部分记忆上下文比截断回答更危险)。
// 否则，保留的标签尾部将按原样输出 (它被证明不是真实标签的一部分)。
func (s *StreamingScrubber) Flush() string {
	if s.inSpan {
		s.buf = ""
		s.inSpan = false
		return ""
	}
	tail := s.buf
	s.buf = ""
	return tail
}

// maxPartialSuffix 返回可以作为标签前缀的最长 buf 后缀长度。
// 不区分大小写。如果没有任何后缀可能作为标签前缀，则返回 0。
func (s *StreamingScrubber) maxPartialSuffix(buf, tag string) int {
	tagLower := strings.ToLower(tag)
	bufLower := strings.ToLower(buf)
	maxCheck := len(bufLower)
	if maxCheck > len(tagLower)-1 {
		maxCheck = len(tagLower) - 1
	}
	for i := maxCheck; i > 0; i-- {
		if strings.HasPrefix(tagLower, bufLower[len(bufLower)-i:]) {
			return i
		}
	}
	return 0
}

// ───────────────────────────── PII 清洗器 ─────────────────────────────

// ScrubPII 对文本进行 PII (个人可识别信息) 清洗。
//
// 替换规则:
//   - 邮箱地址 -> [EMAIL]
//   - 手机号码 -> [PHONE]
//   - IP 地址 -> [IP]
//   - URL 中的密码参数 (password=xxx) -> password=[REDACTED]
//   - API Key 模式 -> [API_KEY]
func ScrubPII(text string) string {
	if text == "" {
		return ""
	}
	return scrubCore(text)
}

// scrubCore 执行核心的 PII 替换逻辑。
func scrubCore(text string) string {
	for _, r := range piiRules {
		text = r.re.ReplaceAllString(text, r.repl)
	}
	return text
}

// piiRule 定义一条 PII 替换规则。
type piiRule struct {
	re   *regexp.Regexp // 匹配正则
	repl string         // 替换文本
}

// piiRules 按优先级排列的 PII 规则列表。
// 注意: 邮箱规则放在前面，避免被 URL 密码规则误匹配。
var piiRules = []piiRule{
	// 1. 邮箱地址: user@domain.tld
	{
		re:   regexp.MustCompile(`(?i)\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`),
		repl: "[EMAIL]",
	},
	// 2. 手机号码: 国际格式 +86-138-xxxx-xxxx、中国大陆 11 位手机号等
	{
		re:   regexp.MustCompile(`(?:\+?\d{1,3}[-.\s]?)?(?:1[3-9]\d{9}|\b\d{3}[-.\s]?\d{3}[-.\s]?\d{4}\b)`),
		repl: "[PHONE]",
	},
	// 3. IPv4 地址
	{
		re:   regexp.MustCompile(`\b(?:(?:25[0-5]|2[0-4]\d|[01]?\d\d?)\.){3}(?:25[0-5]|2[0-4]\d|[01]?\d\d?)\b`),
		repl: "[IP]",
	},
	// 4. IPv6 地址 (简化的完整格式)
	{
		re:   regexp.MustCompile(`(?i)\b(?:[0-9a-f]{1,4}:){7}[0-9a-f]{1,4}\b`),
		repl: "[IP]",
	},
	// 5. URL 中的密码参数 (?password=xxx 或 &password=xxx)
	{
		re:   regexp.MustCompile(`(?i)([?&])(password|passwd|pwd|secret|token|api_key|apikey|access_key|access_token)=([^&\s]+)`),
		repl: "${1}${2}=[REDACTED]",
	},
	// 6. API Key 模式 (sk-xxx、ghp_xxx、glpat-xxx 等)
	{
		re:   regexp.MustCompile(`(?i)\b(?:sk|pk|rk|ghp|gho|glpat|xox[bp]|Bearer)\-[A-Za-z0-9_\-]{16,}`),
		repl: "[API_KEY]",
	},
	// 7. AWS 密钥模式 (AKIA 开头)
	{
		re:   regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
		repl: "[API_KEY]",
	},
}
