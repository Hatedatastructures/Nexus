// Package telemetry 提供结构化遥测事件记录。
// TelemetrySink 接口定义事件接收器，JsonlTelemetrySink 实现 JSONL 文件输出。
package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ───────────────────────────── 事件类型 ─────────────────────────────

// EventType 定义遥测事件类型枚举。
type EventType string

const (
	EventHTTPStarted       EventType = "HttpRequestStarted"
	EventHTTPSucceeded     EventType = "HttpRequestSucceeded"
	EventHTTPFailed        EventType = "HttpRequestFailed"
	EventTurnStarted       EventType = "TurnStarted"
	EventTurnCompleted     EventType = "TurnCompleted"
	EventTurnFailed        EventType = "TurnFailed"
	EventToolStarted       EventType = "ToolExecutionStarted"
	EventToolFinished      EventType = "ToolExecutionFinished"
	EventCompTriggered     EventType = "CompressionTriggered"
	EventCompCompleted     EventType = "CompressionCompleted"
)

// Event 表示一个遥测事件。
type Event struct {
	Type      EventType       `json:"type"`
	Timestamp int64           `json:"timestamp"`
	SessionID string          `json:"session_id,omitempty"`
	Duration  int64           `json:"duration_ms,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	Error     string          `json:"error,omitempty"`
}

// ───────────────────────────── Sink 接口 ─────────────────────────────

// Sink 是遥测事件接收器接口。
type Sink interface {
	Record(event *Event)
	Close() error
}

// ───────────────────────────── JSONL Sink ─────────────────────────────

// JsonlTelemetrySink 将遥测事件写入 JSONL 文件。
type JsonlTelemetrySink struct {
	mu       sync.Mutex
	file     *os.File
	encoder  *json.Encoder
	count    int64
}

// NewJsonlTelemetrySink 创建 JSONL 遥测输出。
// path 为输出文件路径，目录会自动创建。
func NewJsonlTelemetrySink(path string) (*JsonlTelemetrySink, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}

	return &JsonlTelemetrySink{
		file:    f,
		encoder: json.NewEncoder(f),
	}, nil
}

// Record 记录一个遥测事件。
func (s *JsonlTelemetrySink) Record(event *Event) {
	if event.Timestamp == 0 {
		event.Timestamp = time.Now().UnixMilli()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	_ = s.encoder.Encode(event)
	s.count++
}

// Close 关闭输出文件。
func (s *JsonlTelemetrySink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.file.Close()
}

// Count 返回已记录的事件数。
func (s *JsonlTelemetrySink) Count() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count
}

// ───────────────────────────── 全局 Sink ─────────────────────────────

var (
	globalSink Sink
	globalMu   sync.RWMutex
)

// SetGlobalSink 设置全局遥测 Sink。
func SetGlobalSink(s Sink) {
	globalMu.Lock()
	defer globalMu.Unlock()
	globalSink = s
}

// Record 向全局 Sink 记录事件。
// 如果未设置全局 Sink，静默忽略。
func Record(event *Event) {
	globalMu.RLock()
	defer globalMu.RUnlock()
	if globalSink != nil {
		globalSink.Record(event)
	}
}

// RecordSimple 便捷函数: 记录简单事件。
func RecordSimple(eventType EventType, sessionID string, data any) {
	var rawData json.RawMessage
	if data != nil {
		rawData, _ = json.Marshal(data)
	}
	Record(&Event{
		Type:      eventType,
		SessionID: sessionID,
		Data:      rawData,
	})
}

// CloseGlobal 关闭全局 Sink。
func CloseGlobal() error {
	globalMu.Lock()
	defer globalMu.Unlock()
	if globalSink != nil {
		err := globalSink.Close()
		globalSink = nil
		return err
	}
	return nil
}
