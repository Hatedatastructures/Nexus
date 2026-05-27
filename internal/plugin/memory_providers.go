// Package plugin 提供外部内存 Provider 的集成框架。
// 为 Honcho、Mem0、Supermemory、Holographic 等外部内存服务提供统一接口。
package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"nexus-agent/internal/llm"
	"nexus-agent/internal/memory"
)

// ───────────────────────────── Honcho Provider ─────────────────────────────

// HonchoProvider 通过 Honcho API 提供对话记忆功能。
type HonchoProvider struct {
	memory.BaseProvider
	client   *http.Client
	baseURL  string
	apiKey   string
	appID    string
	userID   string
}

// NewHonchoProvider 创建 Honcho 内存提供者。
func NewHonchoProvider() *HonchoProvider {
	return &HonchoProvider{
		client:  &http.Client{Timeout: 30 * time.Second},
		baseURL: os.Getenv("HONCHO_BASE_URL"),
		apiKey:  os.Getenv("HONCHO_API_KEY"),
		appID:   os.Getenv("HONCHO_APP_ID"),
	}
}

func (p *HonchoProvider) Name() string { return "honcho" }

func (p *HonchoProvider) Initialize(ctx context.Context, sessionID string) error {
	if p.apiKey == "" {
		return fmt.Errorf("HONCHO_API_KEY 未设置")
	}
	p.userID = sessionID
	return nil
}

func (p *HonchoProvider) SystemPromptBlock() string {
	return ""
}

func (p *HonchoProvider) SyncTurn(ctx context.Context, userContent, assistantContent string) error {
	// 同步对话轮次到 Honcho
	messages := []map[string]string{
		{"role": "user", "content": userContent},
		{"role": "assistant", "content": assistantContent},
	}
	body, _ := json.Marshal(map[string]any{
		"app_id":    p.appID,
		"user_id":   p.userID,
		"messages":  messages,
	})

	req, err := http.NewRequestWithContext(ctx, "POST",
		fmt.Sprintf("%s/v1/conversations", p.baseURL),
		bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("Honcho API 返回 HTTP %d: %s", resp.StatusCode, string(body))
	}
	io.Copy(io.Discard, resp.Body)

	return nil
}

func (p *HonchoProvider) Prefetch(ctx context.Context, query string) (string, error) {
	u := fmt.Sprintf("%s/v1/conversations/%s/context", p.baseURL, url.PathEscape(p.userID))
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return "", err
	}
	q := req.URL.Query()
	q.Set("query", query)
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("Honcho API 返回 HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Context string `json:"context"`
	}
	json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&result)
	return result.Context, nil
}

func (p *HonchoProvider) GetToolSchemas() []llm.ToolSchema { return nil }
func (p *HonchoProvider) HandleToolCall(ctx context.Context, toolName string, args map[string]any) (string, error) {
	return "", fmt.Errorf("honcho 不支持工具调用")
}
func (p *HonchoProvider) Shutdown(ctx context.Context) error { return nil }

// ───────────────────────────── Mem0 Provider ─────────────────────────────

// Mem0Provider 通过 Mem0 API 提供记忆功能。
type Mem0Provider struct {
	memory.BaseProvider
	client  *http.Client
	baseURL string
	apiKey  string
	userID  string
}

// NewMem0Provider 创建 Mem0 内存提供者。
func NewMem0Provider() *Mem0Provider {
	return &Mem0Provider{
		client:  &http.Client{Timeout: 30 * time.Second},
		baseURL: "https://api.mem0.ai/v1",
		apiKey:  os.Getenv("MEM0_API_KEY"),
	}
}

func (p *Mem0Provider) Name() string { return "mem0" }

func (p *Mem0Provider) Initialize(ctx context.Context, sessionID string) error {
	if p.apiKey == "" {
		return fmt.Errorf("MEM0_API_KEY 未设置")
	}
	p.userID = sessionID
	return nil
}

func (p *Mem0Provider) SystemPromptBlock() string {
	return ""
}

func (p *Mem0Provider) SyncTurn(ctx context.Context, userContent, assistantContent string) error {
	body, _ := json.Marshal(map[string]any{
		"messages": []map[string]string{
			{"role": "user", "content": userContent},
			{"role": "assistant", "content": assistantContent},
		},
		"user_id": p.userID,
	})

	req, err := http.NewRequestWithContext(ctx, "POST",
		fmt.Sprintf("%s/memories/", p.baseURL),
		bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("Mem0 API 返回 HTTP %d: %s", resp.StatusCode, string(body))
	}
	io.Copy(io.Discard, resp.Body)

	return nil
}

func (p *Mem0Provider) Prefetch(ctx context.Context, query string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"query":   query,
		"user_id": p.userID,
	})

	req, err := http.NewRequestWithContext(ctx, "POST",
		fmt.Sprintf("%s/memories/search/", p.baseURL),
		bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("Mem0 API 返回 HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Memories []struct {
			Memory string `json:"memory"`
		} `json:"memories"`
	}
	json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&result)

	var memories []string
	for _, m := range result.Memories {
		memories = append(memories, m.Memory)
	}
	return joinMemories(memories), nil
}

func (p *Mem0Provider) GetToolSchemas() []llm.ToolSchema { return nil }
func (p *Mem0Provider) HandleToolCall(ctx context.Context, toolName string, args map[string]any) (string, error) {
	return "", fmt.Errorf("mem0 不支持工具调用")
}
func (p *Mem0Provider) Shutdown(ctx context.Context) error { return nil }

// ───────────────────────────── Supermemory Provider ─────────────────────────────

// SupermemoryProvider 通过 Supermemory API 提供记忆功能。
type SupermemoryProvider struct {
	memory.BaseProvider
	client  *http.Client
	baseURL string
	apiKey  string
	userID  string
}

// NewSupermemoryProvider 创建 Supermemory 内存提供者。
func NewSupermemoryProvider() *SupermemoryProvider {
	return &SupermemoryProvider{
		client:  &http.Client{Timeout: 30 * time.Second},
		baseURL: "https://api.supermemory.ai/v1",
		apiKey:  os.Getenv("SUPERMEMORY_API_KEY"),
	}
}

func (p *SupermemoryProvider) Name() string { return "supermemory" }

func (p *SupermemoryProvider) Initialize(ctx context.Context, sessionID string) error {
	if p.apiKey == "" {
		return fmt.Errorf("SUPERMEMORY_API_KEY 未设置")
	}
	p.userID = sessionID
	return nil
}

func (p *SupermemoryProvider) SystemPromptBlock() string { return "" }

func (p *SupermemoryProvider) SyncTurn(ctx context.Context, userContent, assistantContent string) error {
	// Supermemory 通过添加记忆 API 同步对话轮次
	content := fmt.Sprintf("User: %s\nAssistant: %s", userContent, assistantContent)
	body, _ := json.Marshal(map[string]any{
		"content":  content,
		"metadata": map[string]string{"user_id": p.userID},
	})

	req, err := http.NewRequestWithContext(ctx, "POST",
		fmt.Sprintf("%s/memories", p.baseURL),
		bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("Supermemory API 返回 HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	io.Copy(io.Discard, resp.Body)
	return nil
}

func (p *SupermemoryProvider) Prefetch(ctx context.Context, query string) (string, error) {
	// Supermemory 通过搜索记忆 API 检索相关记忆
	body, _ := json.Marshal(map[string]any{
		"q":       query,
		"user_id": p.userID,
	})

	req, err := http.NewRequestWithContext(ctx, "POST",
		fmt.Sprintf("%s/search", p.baseURL),
		bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("Supermemory API 返回 HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Data []struct {
			Content string `json:"content"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&result); err != nil {
		return "", nil
	}

	var memories []string
	for _, m := range result.Data {
		if m.Content != "" {
			memories = append(memories, m.Content)
		}
	}
	return joinMemories(memories), nil
}

func (p *SupermemoryProvider) GetToolSchemas() []llm.ToolSchema { return nil }
func (p *SupermemoryProvider) HandleToolCall(ctx context.Context, toolName string, args map[string]any) (string, error) {
	return "", fmt.Errorf("supermemory 不支持工具调用")
}
func (p *SupermemoryProvider) Shutdown(ctx context.Context) error { return nil }

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

func (p *HolographicProvider) Initialize(ctx context.Context, sessionID string) error {
	p.userID = sessionID
	return os.MkdirAll(p.baseDir, 0755)
}

func (p *HolographicProvider) SystemPromptBlock() string { return "" }

func (p *HolographicProvider) SyncTurn(ctx context.Context, userContent, assistantContent string) error {
	// 追加到本地文件
	path := fmt.Sprintf("%s/%s.jsonl", p.baseDir, p.userID)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	entry := map[string]string{
		"user":      userContent,
		"assistant": assistantContent,
	}
	data, _ := json.Marshal(entry)
	data = append(data, '\n')
	f.Write(data)
	return nil
}

func (p *HolographicProvider) Prefetch(ctx context.Context, query string) (string, error) {
	// 读取 JSONL 文件，按关键词匹配检索相关记忆
	path := fmt.Sprintf("%s/%s.jsonl", p.baseDir, p.userID)
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

		// 简单关键词匹配: 检查查询词是否出现在用户或助手内容中
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

	// 限制返回数量
	if len(matches) > 10 {
		matches = matches[:10]
	}
	return strings.Join(matches, "\n"), nil
}

func (p *HolographicProvider) GetToolSchemas() []llm.ToolSchema { return nil }
func (p *HolographicProvider) HandleToolCall(ctx context.Context, toolName string, args map[string]any) (string, error) {
	return "", fmt.Errorf("holographic 不支持工具调用")
}
func (p *HolographicProvider) Shutdown(ctx context.Context) error { return nil }

// ───────────────────────────── Provider 注册 ─────────────────────────────

// MemoryProviderFactory 创建内存提供者的工厂函数。
type MemoryProviderFactory func() memory.Provider

// providerRegistry 内存提供者注册表。
var providerRegistry = map[string]MemoryProviderFactory{
	"honcho":       func() memory.Provider { return NewHonchoProvider() },
	"mem0":         func() memory.Provider { return NewMem0Provider() },
	"supermemory":  func() memory.Provider { return NewSupermemoryProvider() },
	"holographic":  func() memory.Provider { return NewHolographicProvider() },
}

// CreateMemoryProvider 根据名称创建内存提供者。
func CreateMemoryProvider(name string) (memory.Provider, error) {
	factory, ok := providerRegistry[name]
	if !ok {
		return nil, fmt.Errorf("未知的内存提供者: %s", name)
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
	result := ""
	for i, m := range memories {
		if i > 0 {
			result += "\n"
		}
		result += "- " + m
	}
	return result
}
