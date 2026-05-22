// Package agent 提供重试逻辑。
package agent

import (
	"context"
	"time"
)

// retryWithBackoff 带退避的重试包装器。
func retryWithBackoff(ctx context.Context, maxRetries int, fn func() error) error {
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		lastErr = fn()
		if lastErr == nil {
			return nil
		}

		if i < maxRetries-1 {
			backoff := time.Duration(1<<uint(i)) * time.Second
			// 使用 select 等待退避时间，同时监听 context 取消信号。
			// 原实现使用 time.Sleep，无法在 context 取消时及时退出。
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}
	}
	return lastErr
}
