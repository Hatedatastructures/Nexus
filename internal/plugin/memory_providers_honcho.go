// Package plugin 提供外部内存 Provider 的集成框架。
// 为 Honcho、Mem0、Supermemory 等外部内存服务提供统一接口。
package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"time"

	"nexus-agent/internal/llm"
	"nexus-agent/internal/memory"

	pkgerrors "nexus-agent/internal/errors"
)

// ───────────────────────────── Honcho Provider ─────────────────────────────

// HonchoProvider 通过 Honcho API 提供对话记忆功能。
type HonchoProvider struct {
	memory.BaseProvider
	client  *http.Client
	baseURL string
	apiKey  string
	appID   string
	userID  string
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
		return pkgerrors.New(pkgerrors.ConfigMissing, "HONCHO_API_KEY 未设置")
	}
	if p.baseURL == "" {
		return pkgerrors.New(pkgerrors.ConfigMissing, "HONCHO_BASE_URL 未设置")
	}
	p.userID = sessionID
	return nil
}

func (p *HonchoProvider) SystemPromptBlock() string {
	return ""
}

func (p *HonchoProvider) SyncTurn(ctx context.Context, userContent, assistantContent string) error {
	messages := []map[string]string{
		{"role": "user", "content": userContent},
		{"role": "assistant", "content": assistantContent},
	}
	body, err := json.Marshal(map[string]any{
		"app_id":   p.appID,
		"user_id":  p.userID,
		"messages": messages,
	})
	if err != nil {
		return pkgerrors.Wrap(pkgerrors.MemoryProvider, "serialize honcho request", err)
	}

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
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.ReadAll(io.LimitReader(resp.Body, 2048))
		slog.Warn("Honcho SyncTurn API error", "status", resp.StatusCode)
		return pkgerrors.New(pkgerrors.ProviderAPI, fmt.Sprintf("Honcho API 返回 HTTP %d", resp.StatusCode))
	}
	_, _ = io.Copy(io.Discard, resp.Body)

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
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.ReadAll(io.LimitReader(resp.Body, 2048))
		slog.Warn("Honcho Prefetch API error", "status", resp.StatusCode)
		return "", pkgerrors.New(pkgerrors.ProviderAPI, fmt.Sprintf("Honcho API 返回 HTTP %d", resp.StatusCode))
	}

	var result struct {
		Context string `json:"context"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&result); err != nil {
		slog.Warn("honcho prefetch: decode response failed", "error", err)
		return "", nil
	}
	return result.Context, nil
}

func (p *HonchoProvider) GetToolSchemas() []llm.ToolSchema { return nil }
func (p *HonchoProvider) HandleToolCall(ctx context.Context, toolName string, args map[string]any) (string, error) {
	return "", pkgerrors.New(pkgerrors.MemoryProvider, "honcho 不支持工具调用")
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
		return pkgerrors.New(pkgerrors.ConfigMissing, "MEM0_API_KEY 未设置")
	}
	p.userID = sessionID
	return nil
}

func (p *Mem0Provider) SystemPromptBlock() string {
	return ""
}

func (p *Mem0Provider) SyncTurn(ctx context.Context, userContent, assistantContent string) error {
	body, err := json.Marshal(map[string]any{
		"messages": []map[string]string{
			{"role": "user", "content": userContent},
			{"role": "assistant", "content": assistantContent},
		},
		"user_id": p.userID,
	})
	if err != nil {
		return pkgerrors.Wrap(pkgerrors.MemoryProvider, "serialize mem0 request", err)
	}

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
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.ReadAll(io.LimitReader(resp.Body, 2048))
		slog.Warn("Mem0 SyncTurn API error", "status", resp.StatusCode)
		return pkgerrors.New(pkgerrors.ProviderAPI, fmt.Sprintf("Mem0 API 返回 HTTP %d", resp.StatusCode))
	}
	_, _ = io.Copy(io.Discard, resp.Body)

	return nil
}

func (p *Mem0Provider) Prefetch(ctx context.Context, query string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"query":   query,
		"user_id": p.userID,
	})
	if err != nil {
		return "", pkgerrors.Wrap(pkgerrors.MemoryProvider, "serialize mem0 search request", err)
	}

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
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.ReadAll(io.LimitReader(resp.Body, 2048))
		slog.Warn("Mem0 Prefetch API error", "status", resp.StatusCode)
		return "", pkgerrors.New(pkgerrors.ProviderAPI, fmt.Sprintf("Mem0 API 返回 HTTP %d", resp.StatusCode))
	}

	var result struct {
		Memories []struct {
			Memory string `json:"memory"`
		} `json:"memories"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&result); err != nil {
		slog.Warn("mem0 prefetch: decode response failed", "error", err)
		return "", nil
	}

	var memories []string
	for _, m := range result.Memories {
		memories = append(memories, m.Memory)
	}
	return joinMemories(memories), nil
}

func (p *Mem0Provider) GetToolSchemas() []llm.ToolSchema { return nil }
func (p *Mem0Provider) HandleToolCall(ctx context.Context, toolName string, args map[string]any) (string, error) {
	return "", pkgerrors.New(pkgerrors.MemoryProvider, "mem0 不支持工具调用")
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
		return pkgerrors.New(pkgerrors.ConfigMissing, "SUPERMEMORY_API_KEY 未设置")
	}
	p.userID = sessionID
	return nil
}

func (p *SupermemoryProvider) SystemPromptBlock() string { return "" }

func (p *SupermemoryProvider) SyncTurn(ctx context.Context, userContent, assistantContent string) error {
	content := fmt.Sprintf("User: %s\nAssistant: %s", userContent, assistantContent)
	body, err := json.Marshal(map[string]any{
		"content":  content,
		"metadata": map[string]string{"user_id": p.userID},
	})
	if err != nil {
		return pkgerrors.Wrap(pkgerrors.MemoryProvider, "serialize supermemory request", err)
	}

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
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.ReadAll(io.LimitReader(resp.Body, 2048))
		slog.Warn("Supermemory SyncTurn API error", "status", resp.StatusCode)
		return pkgerrors.New(pkgerrors.ProviderAPI, fmt.Sprintf("Supermemory API 返回 HTTP %d", resp.StatusCode))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

func (p *SupermemoryProvider) Prefetch(ctx context.Context, query string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"q":       query,
		"user_id": p.userID,
	})
	if err != nil {
		return "", pkgerrors.Wrap(pkgerrors.MemoryProvider, "serialize supermemory search request", err)
	}

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
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.ReadAll(io.LimitReader(resp.Body, 2048))
		slog.Warn("Supermemory Prefetch API error", "status", resp.StatusCode)
		return "", pkgerrors.New(pkgerrors.ProviderAPI, fmt.Sprintf("Supermemory API 返回 HTTP %d", resp.StatusCode))
	}

	var result struct {
		Data []struct {
			Content string `json:"content"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&result); err != nil {
		slog.Warn("supermemory prefetch: decode response failed", "error", err)
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
	return "", pkgerrors.New(pkgerrors.MemoryProvider, "supermemory 不支持工具调用")
}
func (p *SupermemoryProvider) Shutdown(ctx context.Context) error { return nil }
