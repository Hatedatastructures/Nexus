// Package sandbox 提供 Modal 云沙箱执行环境。
// 通过 Modal REST API 创建和管理云端沙箱容器。
package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"
)

// ───────────────────────────── Modal 环境 ─────────────────────────────

// ModalEnvironment 通过 Modal API 执行命令的沙箱环境。
type ModalEnvironment struct {
	client    *http.Client
	baseURL   string // Modal API 基础 URL
	token     string // Modal API token
	appName   string // Modal App 名称
	sandboxID string // 当前沙箱 ID
	cwd       string // 当前工作目录
	mu        sync.Mutex
	createMu  sync.Mutex // 序列化沙箱创建，防止并发重复创建
}

// NewModalEnvironment 创建 Modal 沙箱环境。
func NewModalEnvironment() *ModalEnvironment {
	return &ModalEnvironment{
		client:  &http.Client{Timeout: 300 * time.Second},
		baseURL: "https://api.modal.com/v1",
		token:   os.Getenv("MODAL_TOKEN"),
		appName: "nexus-sandbox",
		cwd:     "/root",
	}
}

// Execute 在 Modal 沙箱中执行命令。
func (e *ModalEnvironment) Execute(ctx context.Context, command string, opts *ExecuteOptions) (*ExecuteResult, error) {
	if e.token == "" {
		return nil, fmt.Errorf("MODAL_TOKEN 未设置")
	}

	// 确保沙箱存在（序列化创建，防止并发重复创建）
	e.createMu.Lock()
	sid := e.sandboxID
	if sid == "" {
		if err := e.createSandbox(ctx); err != nil {
			e.createMu.Unlock()
			return nil, fmt.Errorf("创建 Modal 沙箱失败: %w", err)
		}
	}
	e.createMu.Unlock()

	e.mu.Lock()
	sid = e.sandboxID
	cwd := e.cwd
	e.mu.Unlock()

	// 执行命令
	reqBody := map[string]any{
		"command": command,
		"cwd":     cwd,
	}
	if opts != nil && opts.Timeout > 0 {
		reqBody["timeout"] = int(opts.Timeout.Seconds())
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("序列化请求失败: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST",
		fmt.Sprintf("%s/sandboxes/%s/exec", e.baseURL, sid),
		bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.token)

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("Modal API 返回 HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		ExitCode int    `json:"exit_code"`
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		CWD      string `json:"cwd"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&result); err != nil {
		return nil, err
	}

	if result.CWD != "" {
		e.mu.Lock()
		e.cwd = result.CWD
		e.mu.Unlock()
	}

	e.mu.Lock()
	finalCWD := e.cwd
	e.mu.Unlock()

	return &ExecuteResult{
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
		ExitCode: result.ExitCode,
		CWD:      finalCWD,
	}, nil
}

// ExecuteBackground 在 Modal 沙箱中后台执行命令。
func (e *ModalEnvironment) ExecuteBackground(ctx context.Context, command string, opts *ExecuteOptions) (ProcessHandle, error) {
	return nil, fmt.Errorf("Modal 沙箱不支持后台执行")
}

// CWD 返回当前工作目录。
func (e *ModalEnvironment) CWD() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cwd
}

// UpdateCWD 更新当前工作目录。
func (e *ModalEnvironment) UpdateCWD(cwd string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cwd = cwd
}

// Cleanup 清理 Modal 沙箱。
func (e *ModalEnvironment) Cleanup() error {
	e.mu.Lock()
	sid := e.sandboxID
	e.mu.Unlock()
	if sid == "" {
		return nil
	}

	req, err := http.NewRequestWithContext(context.Background(), "DELETE",
		fmt.Sprintf("%s/sandboxes/%s", e.baseURL, sid), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+e.token)

	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	slog.Info("Modal sandbox cleaned up", "sandbox_id", sid)
	e.mu.Lock()
	e.sandboxID = ""
	e.mu.Unlock()
	return nil
}

func (e *ModalEnvironment) createSandbox(ctx context.Context) error {
	reqBody := map[string]any{
		"app_name": e.appName,
		"image":    "python:3.11-slim",
		"timeout":  3600,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("序列化请求失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		fmt.Sprintf("%s/sandboxes", e.baseURL),
		bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.token)

	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("Modal API 返回 HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&result); err != nil {
		return err
	}

	e.mu.Lock()
	e.sandboxID = result.ID
	e.mu.Unlock()
	slog.Info("Modal sandbox created", "sandbox_id", result.ID)
	return nil
}
