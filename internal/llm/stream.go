// Package llm 提供 LLM 提供者抽象层。
package llm

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"strings"
)

// SSEEvent 表示单个 SSE（Server-Sent Events）事件。
type SSEEvent struct {
	Event string // 事件类型字段（可选）
	Data  string // 数据字段，多条 data 行用 \n 拼接
	ID    string // 事件 ID（可选）
	Retry int    // 重试间隔毫秒数（可选）
}

// ParseSSEStream 从 io.Reader 逐行读取 SSE 流，通过 channel 发送解析后的事件。
// 当流结束或发生错误时，关闭 channel。
// 支持：
//   - data: 行解析
//   - event: 行解析
//   - id: 行解析
//   - retry: 行解析
//   - 连续的 data: 行用 \n 连接
//   - [DONE] 标记（流结束信号）
func ParseSSEStream(ctx context.Context, body io.ReadCloser) <-chan *SSEEvent {
	ch := make(chan *SSEEvent, 256) // 带缓冲通道，避免写入阻塞

	go func() {
		defer close(ch)
		defer body.Close()

		scanner := bufio.NewScanner(body)
		// 增大扫描缓冲区以处理长 data 行
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		var currentEvent SSEEvent
		var dataLines []string

		flushEvent := func() {
			// 如果有累积的 data 行，发送事件
			if len(dataLines) > 0 {
				e := currentEvent
				e.Data = strings.Join(dataLines, "\n")
				select {
				case ch <- &e:
				case <-ctx.Done():
					return
				}
			}
			// 重置状态
			currentEvent = SSEEvent{}
			dataLines = dataLines[:0]
		}

		for scanner.Scan() {
			// 检查 context 是否已取消
			select {
			case <-ctx.Done():
				return
			default:
			}

			line := scanner.Text()

			// 空行表示事件分隔符
			if line == "" {
				flushEvent()
				continue
			}

			// 注释行（以冒号开头），忽略
			if strings.HasPrefix(line, ":") {
				continue
			}

			// 解析字段: field:value
			colonIdx := strings.Index(line, ":")
			if colonIdx < 0 {
				continue // 无效行，忽略
			}

			field := line[:colonIdx]
			value := ""
			if colonIdx+1 < len(line) {
				value = line[colonIdx+1:]
				// 如果值以空格开头，去除前导空格
				value = strings.TrimPrefix(line[colonIdx+1:], " ")
			}

			switch field {
			case "data":
				// [DONE] 标记表示流结束
				if value == "[DONE]" {
					flushEvent()
					return
				}
				dataLines = append(dataLines, value)
			case "event":
				currentEvent.Event = value
			case "id":
				currentEvent.ID = value
			case "retry":
				// 解析重试间隔（毫秒）；忽略解析错误
				retryVal := 0
				for _, c := range value {
					if c >= '0' && c <= '9' {
						retryVal = retryVal*10 + int(c-'0')
					} else {
						break
					}
				}
				currentEvent.Retry = retryVal
			}
		}

		// 处理最后一组事件（没有尾随空行的情况）
		if len(dataLines) > 0 {
			e := currentEvent
			e.Data = strings.Join(dataLines, "\n")
			select {
			case ch <- &e:
			case <-ctx.Done():
			}
		}

		// 扫描错误
		if err := scanner.Err(); err != nil && err != io.EOF {
			slog.Error("SSE stream read error", "error", err)
		}
	}()

	return ch
}

// ReadSSEStream 从 SSE 流 channel 中收集所有事件，返回完整的 data 字符串和可能的错误。
// 用于将流式响应聚合为单个字符串。
func ReadSSEStream(ctx context.Context, body io.ReadCloser) (string, error) {
	var sb strings.Builder
	for event := range ParseSSEStream(ctx, body) {
		if event.Data != "" {
			sb.WriteString(event.Data)
		}
	}
	return sb.String(), nil
}
