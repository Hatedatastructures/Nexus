// Package plugin 提供外部内存 Provider 的集成框架。
// 为 Holographic 等外部内存服务提供统一接口。
package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"nexus-agent/internal/llm"
	"nexus-agent/internal/memory"

	pkgerrors "nexus-agent/internal/errors"
)

// ───────────────────────────── Holographic Provider ─────────────────────────────

// HolographicProvider 使用本地文件系统提供记忆功能。
type HolographicProvider struct {
	memory.BaseProvider
	baseDir string
	userID  string
}

// NewHolographicProvider 创建 Holographic 内存提供者。
func NewHolographicProvider() *HolographicProvider {
	home, _ := os.UserHomeDir()
	return &HolographicProvider{
		baseDir: home + "/.nexus/holographic",
	}
}

func (p *HolographicProvider) Name() string { return "holographic" }

// safeUserIDRe 仅允许安全字符 [a-zA-Z0-9_-]。
var safeUserIDRe = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// sanitizeUserID 清洗 userID，仅保留安全字符并验证路径不逃逸 baseDir。
func sanitizeUserID(userID, baseDir string) (string, error) {
	clean := safeUserIDRe.ReplaceAllString(userID, "_")
	path := filepath.Join(baseDir, clean+".jsonl")
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", pkgerrors.Wrap(pkgerrors.FileIO, "路径解析失败", err)
	}
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", pkgerrors.Wrap(pkgerrors.FileIO, "基础路径解析失败", err)
	}
	if !strings.HasPrefix(absPath, absBase+string(filepath.Separator)) {
		return "", pkgerrors.New(pkgerrors.FileSafety, "userID 路径穿越检测")
	}
	return absPath, nil
}

func (p *HolographicProvider) Initialize(ctx context.Context, sessionID string) error {
	p.userID = sessionID
	return os.MkdirAll(p.baseDir, 0755)
}

func (p *HolographicProvider) SystemPromptBlock() string { return "" }

func (p *HolographicProvider) SyncTurn(ctx context.Context, userContent, assistantContent string) error {
	path, err := sanitizeUserID(p.userID, p.baseDir)
	if err != nil {
		return pkgerrors.Wrap(pkgerrors.FileIO, "userID 清洗失败", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	entry := map[string]string{
		"user":      userContent,
		"assistant": assistantContent,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return pkgerrors.Wrap(pkgerrors.MemoryProvider, "serialize holographic entry", err)
	}
	data = append(data, '\n')
	if _, err := f.Write(data); err != nil {
		return pkgerrors.Wrap(pkgerrors.FileIO, "写入 holographic 条目失败", err)
	}
	return nil
}

func (p *HolographicProvider) Prefetch(ctx context.Context, query string) (string, error) {
	path, err := sanitizeUserID(p.userID, p.baseDir)
	if err != nil {
		return "", pkgerrors.Wrap(pkgerrors.FileIO, "userID 清洗失败", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}

	queryLower := strings.ToLower(query)
	var matches []string
	lines := strings.Split(string(data), "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var entry map[string]string
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}

		for _, v := range entry {
			if strings.Contains(strings.ToLower(v), queryLower) {
				matches = append(matches, fmt.Sprintf("- User: %s\n  Assistant: %s", entry["user"], entry["assistant"]))
				break
			}
		}
	}

	if len(matches) == 0 {
		return "", nil
	}

	if len(matches) > 10 {
		matches = matches[:10]
	}
	return strings.Join(matches, "\n"), nil
}

func (p *HolographicProvider) GetToolSchemas() []llm.ToolSchema { return nil }
func (p *HolographicProvider) HandleToolCall(ctx context.Context, toolName string, args map[string]any) (string, error) {
	return "", pkgerrors.New(pkgerrors.MemoryProvider, "holographic 不支持工具调用")
}
func (p *HolographicProvider) Shutdown(ctx context.Context) error { return nil }

// ───────────────────────────── Provider 注册 ─────────────────────────────

// MemoryProviderFactory 创建内存提供者的工厂函数。
type MemoryProviderFactory func() memory.Provider

// providerRegistry 内存提供者注册表。
var providerRegistry = map[string]MemoryProviderFactory{
	"honcho":      func() memory.Provider { return NewHonchoProvider() },
	"mem0":        func() memory.Provider { return NewMem0Provider() },
	"supermemory": func() memory.Provider { return NewSupermemoryProvider() },
	"holographic": func() memory.Provider { return NewHolographicProvider() },
}

// CreateMemoryProvider 根据名称创建内存提供者。
func CreateMemoryProvider(name string) (memory.Provider, error) {
	factory, ok := providerRegistry[name]
	if !ok {
		return nil, pkgerrors.New(pkgerrors.MemoryProvider, fmt.Sprintf("未知的内存提供者: %s", name))
	}
	return factory(), nil
}

// ListMemoryProviders 列出所有可用的内存提供者。
func ListMemoryProviders() []string {
	var names []string
	for name := range providerRegistry {
		names = append(names, name)
	}
	return names
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

func joinMemories(memories []string) string {
	if len(memories) == 0 {
		return ""
	}
	parts := make([]string, len(memories))
	for i, m := range memories {
		parts[i] = "- " + m
	}
	return strings.Join(parts, "\n")
}
