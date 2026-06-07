// Package memory 提供内置的文件记忆存储 (MEMORY.md / USER.md)。
package memory

import (
	"context"
	"fmt"
	"strings"
	"unicode"
)

// ───────────────────────────── 搜索相关 ─────────────────────────────

// Prefetch 为即将到来的对话回合返回与查询相关的记忆条目子集。
//
// 匹配策略:
//   - 如果 query 为空，返回完整记忆
//   - 将 query 按空格和标点分词
//   - 检查每个词及其变体是否出现在记忆条目的内容中
//   - 按匹配次数降序排序，返回 top 3
//
// 返回空字符串表示无相关记忆。
func (p *BuiltinProvider) Prefetch(ctx context.Context, query string) (string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// query 为空时返回完整记忆
	if strings.TrimSpace(query) == "" {
		all := p.formatAllEntries()
		if all == "" {
			return "", nil
		}
		return all, nil
	}

	// 分词: 按空格和标点分割
	tokens := tokenizeQuery(query)
	if len(tokens) == 0 {
		return "", nil
	}

	// 收集所有候选条目 (memory + user)
	var candidates []scored

	allEntries := append(append([]string(nil), p.memory...), p.user...)
	for _, e := range allEntries {
		score := scoreEntry(e, tokens)
		if score > 0 {
			candidates = append(candidates, scored{entry: e, score: score})
		}
	}

	if len(candidates) == 0 {
		return "", nil
	}

	// 按得分降序排序
	sortScored(candidates)

	// 取 top 3
	limit := 3
	if len(candidates) < limit {
		limit = len(candidates)
	}

	var matched []string
	for i := 0; i < limit; i++ {
		matched = append(matched, candidates[i].entry)
	}

	return fmt.Sprintf("[记忆匹配: %d 条目]\n%s", len(matched), strings.Join(matched, "\n")), nil
}

// tokenizeQuery 将查询文本按空格和标点符号分词。
// 返回小写化的词列表，过滤掉空词和过短的词 (长度 < 2)。
func tokenizeQuery(query string) []string {
	var buf strings.Builder
	for _, r := range query {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			buf.WriteRune(unicode.ToLower(r))
		} else if r >= 0x4e00 && r <= 0x9fff { // CJK 统一汉字
			buf.WriteRune(' ')
			buf.WriteRune(r)
			buf.WriteRune(' ')
		} else if r >= 0x3040 && r <= 0x30ff { // 日文平假名/片假名
			buf.WriteRune(' ')
			buf.WriteRune(r)
			buf.WriteRune(' ')
		} else {
			buf.WriteRune(' ')
		}
	}

	words := strings.Fields(buf.String())
	var tokens []string
	seen := make(map[string]bool)
	addToken := func(w string) {
		if !seen[w] {
			seen[w] = true
			tokens = append(tokens, w)
		}
	}
	for _, w := range words {
		isCJK := false
		for _, r := range w {
			if r >= 0x4e00 && r <= 0x9fff || r >= 0x3040 && r <= 0x30ff {
				isCJK = true
				break
			}
		}
		if isCJK || len(w) >= 2 {
			addToken(w)
		}
	}
	return tokens
}

// scoreEntry 计算条目与词表的匹配得分。
// 直接匹配得 2 分，词根前缀匹配得 1 分。
func scoreEntry(entry string, tokens []string) int {
	lower := strings.ToLower(entry)
	score := 0
	for _, token := range tokens {
		// 精确子串匹配
		if strings.Contains(lower, token) {
			score += 2
			continue
		}
		// 前缀变体匹配 (词的前 3 个字符出现在条目中)
		if len(token) >= 4 {
			prefix := token[:3]
			if strings.Contains(lower, prefix) {
				score += 1
			}
		}
	}
	return score
}

// scored 表示带匹配得分的记忆条目。
type scored struct {
	entry string
	score int
}

// sortScored 按得分降序排序 (插入排序，列表较短时高效)。
func sortScored(candidates []scored) {
	for i := 1; i < len(candidates); i++ {
		key := candidates[i]
		j := i - 1
		for j >= 0 && candidates[j].score < key.score {
			candidates[j+1] = candidates[j]
			j--
		}
		candidates[j+1] = key
	}
}

// formatAllEntries 返回所有记忆条目的格式化字符串。
func (p *BuiltinProvider) formatAllEntries() string {
	var parts []string
	if len(p.memory) > 0 {
		parts = append(parts, "=== MEMORY ===")
		parts = append(parts, p.memory...)
	}
	if len(p.user) > 0 {
		parts = append(parts, "=== USER PROFILE ===")
		parts = append(parts, p.user...)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n")
}
