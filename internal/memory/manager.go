// Package memory 提供记忆系统的编排管理。
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"nexus-agent/internal/llm"

	pkgerrors "nexus-agent/internal/errors"
)

// ───────────────────────────── 记忆管理器 ─────────────────────────────

// Manager 编排内置和外部记忆提供者。
// 确保最多一个外部提供者激活，外加内置提供者。
// 失败在一个提供者中不会阻塞其他提供者。
type Manager struct {
	mu            sync.RWMutex
	builtin       Provider            // 内置提供者 (总是存在)
	external      Provider            // 外部提供者 (可选)
	toolProviders map[string]Provider // 工具名 -> 提供者映射
}

// NewManager 创建记忆管理器
func NewManager(builtin Provider) *Manager {
	m := &Manager{
		builtin:       builtin,
		toolProviders: make(map[string]Provider),
	}
	// 索引内置提供者的工具
	if builtin != nil {
		for _, schema := range builtin.GetToolSchemas() {
			m.toolProviders[schema.Name] = builtin
		}
	}
	return m
}

// SetExternal 设置外部记忆提供者
func (m *Manager) SetExternal(p Provider) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.external = p
	// 索引外部提供者的工具 (仅当不与内置工具冲突时)
	if p != nil {
		for _, schema := range p.GetToolSchemas() {
			if _, exists := m.toolProviders[schema.Name]; !exists {
				m.toolProviders[schema.Name] = p
			} else {
				slog.Warn("memory tool name conflict",
					"tool", schema.Name,
					"existing", m.toolProviders[schema.Name].Name(),
					"rejected", p.Name(),
				)
			}
		}
	}
}

// SystemPromptBlock 返回所有提供者的系统提示词块拼接
func (m *Manager) SystemPromptBlock() string {
	var result string
	if m.builtin != nil {
		result += m.builtin.SystemPromptBlock()
	}
	m.mu.RLock()
	ext := m.external
	m.mu.RUnlock()
	if ext != nil {
		result += "\n" + ext.SystemPromptBlock()
	}
	return result
}

// ───────────────────────────── 编排方法 ─────────────────────────────

// PrefetchAll 对所有提供者执行记忆预取，返回合并后的上下文。
// query 是用户当前的输入文本。
// 空提供者被跳过。一个提供者失败不会阻塞其他提供者。
func (m *Manager) PrefetchAll(ctx context.Context, query string) (string, error) {
	var parts []string
	providers := m.allProviders()

	for _, p := range providers {
		result, err := p.Prefetch(ctx, query)
		if err != nil {
			slog.Debug("memory prefetch failed (non-fatal)",
				"provider", p.Name(),
				"error", err,
			)
			continue
		}
		if strings.TrimSpace(result) != "" {
			parts = append(parts, result)
		}
	}

	return strings.Join(parts, "\n\n"), nil
}

// SyncAll 将已完成的对话回合同步到所有提供者。
func (m *Manager) SyncAll(ctx context.Context, userContent, assistantContent string) error {
	providers := m.allProviders()
	var errs []error

	for _, p := range providers {
		if err := p.SyncTurn(ctx, userContent, assistantContent); err != nil {
			slog.Warn("memory sync failed",
				"provider", p.Name(),
				"error", err,
			)
			errs = append(errs, fmt.Errorf("%s: %w", p.Name(), err))
		}
	}

	if len(errs) > 0 {
		return pkgerrors.New(pkgerrors.MemoryProvider, fmt.Sprintf("部分提供者同步失败: %v", errs))
	}
	return nil
}

// GetToolSchemas 收集所有提供者的工具 Schema 列表。
// 冲突的工具名以首次注册为准。
func (m *Manager) GetToolSchemas() []llm.ToolSchema {
	m.mu.RLock()
	defer m.mu.RUnlock()

	seen := make(map[string]struct{})
	var schemas []llm.ToolSchema
	providers := []Provider{m.builtin}
	if m.external != nil {
		providers = append(providers, m.external)
	}

	for _, p := range providers {
		if p == nil {
			continue
		}
		for _, schema := range p.GetToolSchemas() {
			if _, exists := seen[schema.Name]; !exists {
				schemas = append(schemas, schema)
				seen[schema.Name] = struct{}{}
			}
		}
	}

	return schemas
}

// HandleToolCall 将工具调用路由到正确的提供者。
// 返回 JSON 格式的结果字符串。
func (m *Manager) HandleToolCall(ctx context.Context, toolName string, args map[string]any) (string, error) {
	m.mu.RLock()
	provider := m.toolProviders[toolName]
	m.mu.RUnlock()

	if provider == nil {
		msg := fmt.Sprintf("没有提供者处理工具 '%s'", toolName)
		data, _ := json.Marshal(map[string]any{
			"success": false,
			"error":   msg,
		})
		return string(data), nil
	}

	result, err := provider.HandleToolCall(ctx, toolName, args)
	if err != nil {
		slog.Error("memory tool call failed",
			"provider", provider.Name(),
			"tool", toolName,
			"error", err,
		)
		data, _ := json.Marshal(map[string]any{
			"success": false,
			"error":   fmt.Sprintf("记忆工具 '%s' 失败: %v", toolName, err),
		})
		return string(data), nil
	}

	return result, nil
}

// InitializeAll 初始化所有提供者。
func (m *Manager) InitializeAll(ctx context.Context, sessionID string) error {
	providers := m.allProviders()
	for _, p := range providers {
		if err := p.Initialize(ctx, sessionID); err != nil {
			slog.Warn("memory provider initialization failed",
				"provider", p.Name(),
				"error", err,
			)
			return pkgerrors.Wrap(pkgerrors.MemoryProvider, fmt.Sprintf("提供者 '%s' 初始化失败", p.Name()), err)
		}
	}
	return nil
}

// ShutdownAll 关闭所有提供者 (逆序关闭以确保优雅清理)。
func (m *Manager) ShutdownAll(ctx context.Context) error {
	providers := m.allProviders()
	// 逆序关闭
	for i := len(providers) - 1; i >= 0; i-- {
		p := providers[i]
		if err := p.Shutdown(ctx); err != nil {
			slog.Warn("memory provider shutdown failed",
				"provider", p.Name(),
				"error", err,
			)
		}
	}
	return nil
}

// allProviders 返回所有已注册的提供者列表 (内置优先)。
func (m *Manager) allProviders() []Provider {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var providers []Provider
	if m.builtin != nil {
		providers = append(providers, m.builtin)
	}
	if m.external != nil {
		providers = append(providers, m.external)
	}
	return providers
}
