// Package llm 提供 LLM 提供者抽象层。
package llm

import (
	"sync"
)

// TransportRegistry 管理 API 模式到 Transport 实现的映射。
// 并发安全，支持动态注册和查询。
type TransportRegistry struct {
	mu         sync.RWMutex
	transports map[string]Transport // apiMode → Transport 实现
}

// NewTransportRegistry 创建一个新的传输层注册表。
func NewTransportRegistry() *TransportRegistry {
	return &TransportRegistry{
		transports: make(map[string]Transport),
	}
}

// Register 注册一个 Transport 实现。
// 如果 apiMode 已被注册，则覆盖旧实现。
func (r *TransportRegistry) Register(apiMode string, transport Transport) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.transports[apiMode] = transport
}

// Get 根据 apiMode 获取已注册的 Transport 实现。
// 返回值和 bool 指示是否找到。
func (r *TransportRegistry) Get(apiMode string) (Transport, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.transports[apiMode]
	return t, ok
}

// List 返回所有已注册的 API 模式名称。
func (r *TransportRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	modes := make([]string, 0, len(r.transports))
	for mode := range r.transports {
		modes = append(modes, mode)
	}
	return modes
}

// DefaultRegistry 是全局默认的传输层注册表实例。
// 在 init() 中由各传输实现注册自身。
var DefaultRegistry = NewTransportRegistry()

// RegisterTransport 向全局默认注册表注册传输实现。
// 各传输实现在其 init() 中调用此函数。
func RegisterTransport(apiMode string, transport Transport) {
	DefaultRegistry.Register(apiMode, transport)
}

// GetTransport 从全局默认注册表获取传输实现。
func GetTransport(apiMode string) (Transport, bool) {
	return DefaultRegistry.Get(apiMode)
}
