package platforms

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ───────────────────────────── 权限检查 ─────────────────────────────

// isDMAllowed 检查私聊权限。
func (a *QQBotAdapter) isDMAllowed(userID string) bool {
	if a.dmPolicy == "disabled" {
		return false
	}
	if a.dmPolicy == "allowlist" {
		return entryMatches(a.allowFrom, userID)
	}
	return true
}

// isGroupAllowed 检查群聊权限。
func (a *QQBotAdapter) isGroupAllowed(groupID, userID string) bool {
	if a.groupPolicy == "disabled" {
		return false
	}
	if a.groupPolicy == "allowlist" {
		return entryMatches(a.groupAllowFrom, groupID)
	}
	return true
}

// ───────────────────────────── API 调用 ─────────────────────────────

// callAPI 调用 QQ Bot REST API。
func (a *QQBotAdapter) callAPI(ctx context.Context, endpoint string, body map[string]any) (map[string]any, error) {
	url := a.apiURL + endpoint

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	token := a.accessToken
	a.mu.Unlock()

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-App-Id", a.appID)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("API 错误 (HTTP %d)", resp.StatusCode)
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	return result, nil
}

// callExternalAPI 调用外部 API。
func (a *QQBotAdapter) callExternalAPI(ctx context.Context, url string, body map[string]any) (map[string]any, error) {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("外部 API 错误 (HTTP %d)", resp.StatusCode)
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	return result, nil
}
