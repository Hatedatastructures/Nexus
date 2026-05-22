package llm

// EstimateTokensRough 粗略估算消息列表的 token 数。
// 基于经验法则: 英文约 4 个字符 = 1 token，中文约 1.5 个字符 = 1 token。
// 每条消息额外增加 10 token 的格式开销。
// 这是一个快速估算，不精确但对上下文窗口检查足够。
func EstimateTokensRough(messages []Message) int {
	total := 0
	for _, msg := range messages {
		charCount := 0
		for _, r := range msg.Content {
			if r >= 0x4e00 && r <= 0x9fff { // CJK 统一汉字
				charCount += 2 // CJK: ~1.5 字符/token, 用 2 近似
			} else {
				charCount++
			}
		}
		total += charCount/4 + 10
		for _, tc := range msg.ToolCalls {
			total += len(tc.Arguments) / 4
		}
	}
	return total
}
