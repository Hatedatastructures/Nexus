// Package logutil 提供结构化日志的初始化和辅助函数。
// 基于 log/slog 标准库，支持 JSON/Text 两种格式输出。
// 所有日志自动附加调用位置信息 (source: file:line)。
package logutil

import (
	"log/slog"
	"os"
	"path/filepath"
)

// ───────────────────────────── 初始化 ─────────────────────────────

// InitLogger 根据配置初始化全局日志器。
// level: "debug" / "info" / "warn" / "error" (默认 "info")
// format: "json" / "text" (默认 "text")
// logDir: 日志文件目录 (空 = 仅输出到 stderr)
// 返回一个关闭函数，调用者可 defer closeFn() 来刷盘日志文件。
func InitLogger(level, format, logDir string) (closeFn func()) {
	// 解析日志级别
	var slogLevel slog.Level
	switch level {
	case "debug":
		slogLevel = slog.LevelDebug
	case "warn":
		slogLevel = slog.LevelWarn
	case "error":
		slogLevel = slog.LevelError
	default:
		slogLevel = slog.LevelInfo
	}

	// 构建处理器选项
	opts := &slog.HandlerOptions{
		Level:     slogLevel,
		AddSource: true, // 自动附加调用位置
	}

	// 构建写入器
	var writer *multiWriteCloser
	if logDir != "" {
		// 确保日志目录存在
		if err := os.MkdirAll(logDir, 0755); err != nil {
			slog.Warn("无法创建日志目录，仅输出到 stderr", "dir", logDir, "err", err)
			logDir = ""
		}
	}
	if logDir != "" {
		logFile, err := os.OpenFile(
			filepath.Join(logDir, "agent.log"),
			os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600,
		)
		if err != nil {
			slog.Warn("无法打开日志文件，仅输出到 stderr", "err", err)
		} else {
			writer = newMultiWriter(os.Stderr, logFile)
		}
	}

	// 使用默认写入器
	if writer == nil {
		writer = newMultiWriter(os.Stderr)
	}

	// 构建处理器
	var handler slog.Handler
	switch format {
	case "json":
		handler = slog.NewJSONHandler(writer, opts)
	default:
		handler = slog.NewTextHandler(writer, opts)
	}

	// 设置为全局默认 logger
	slog.SetDefault(slog.New(handler))

	return func() {
		if writer != nil {
			_ = writer.Close()
		}
	}
}

// ───────────────────────────── 写入器类型 ─────────────────────────────

// WriteClose 组合 io.Writer 和 io.Closer 接口
type writeClose interface {
	Write([]byte) (int, error)
	Close() error
}

// ───────────────────────────── 多输出写入器 ─────────────────────────────

// multiWriteCloser 将数据同时写入多个 writeClose 接口实现
type multiWriteCloser struct {
	writers []writeClose
}

// newMultiWriter 创建一个多输出写入器
func newMultiWriter(writers ...writeClose) *multiWriteCloser {
	return &multiWriteCloser{writers: writers}
}

// Write 向所有写入器写入数据
func (m *multiWriteCloser) Write(p []byte) (int, error) {
	for _, w := range m.writers {
		if _, err := w.Write(p); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// Close 关闭所有写入器
func (m *multiWriteCloser) Close() error {
	var lastErr error
	for _, w := range m.writers {
		if err := w.Close(); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// ───────────────────────────── 便捷函数 ─────────────────────────────

// WithSession 创建带有会话 ID 的日志器
func WithSession(sessionID string) *slog.Logger {
	return slog.Default().With("session_id", sessionID)
}

// WithComponent 创建带有组件名称的日志器
func WithComponent(name string) *slog.Logger {
	return slog.Default().With("component", name)
}
