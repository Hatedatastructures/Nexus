// Package tool 提供 Home Assistant 工具。
// 通过 REST API 控制智能家居设备。
package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

// ───────────────────────────── 常量 ─────────────────────────────

const (
	haDefaultAPIURL  = "https://homeassistant.local:8123/api"
	haRequestTimeout = 15 * time.Second
	haMaxResultChars = 50000
)

// 危险 domain 列表（阻止调用）
var haBlockedDomains = []string{
	"shell_command",
	"python_script",
	"rest_command",
	"script",
	"automation",
	"scene",
}

// ───────────────────────────── HomeAssistantTool ─────────────────────────────

// HomeAssistantTool Home Assistant 工具。
type HomeAssistantTool struct {
	apiURL     string
	token      string
	httpClient *http.Client
}

// NewHomeAssistantTool 创建 Home Assistant 工具。
func NewHomeAssistantTool() *HomeAssistantTool {
	apiURL := os.Getenv("HASS_API_URL")
	if apiURL == "" {
		apiURL = haDefaultAPIURL
	}

	token := os.Getenv("HASS_TOKEN")

	return &HomeAssistantTool{
		apiURL:     apiURL,
		token:      token,
		httpClient: &http.Client{Timeout: haRequestTimeout},
	}
}

// ───────────────────────────── 工具接口 ─────────────────────────────

// Name 返回工具名称。
func (t *HomeAssistantTool) Name() string { return "homeassistant" }

// Description 返回工具描述。
func (t *HomeAssistantTool) Description() string {
	return "控制 Home Assistant 智能家居设备。支持列出实体、查询状态、调用服务。"
}

// Schema 返回工具 Schema。
func (t *HomeAssistantTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "操作类型",
					"enum":        []string{"list_entities", "get_state", "list_services", "call_service"},
				},
				"entity_id": map[string]any{
					"type":        "string",
					"description": "实体 ID (如 light.living_room)",
				},
				"domain": map[string]any{
					"type":        "string",
					"description": "域过滤器 (如 light, switch)",
				},
				"service": map[string]any{
					"type":        "string",
					"description": "服务名称 (如 turn_on, turn_off)",
				},
				"data": map[string]any{
					"type":        "object",
					"description": "服务调用参数",
				},
			},
			"required": []string{"action"},
		},
	}
}

// Toolset 返回工具集名称。
func (t *HomeAssistantTool) Toolset() string { return "iot" }

// IsAvailable 检查工具是否可用。
func (t *HomeAssistantTool) IsAvailable() bool {
	return os.Getenv("HASS_TOKEN") != ""
}

// Emoji 返回工具图标。
func (t *HomeAssistantTool) Emoji() string { return "🏠" }

// MaxResultChars 返回最大结果字符数。
func (t *HomeAssistantTool) MaxResultChars() int { return haMaxResultChars }

// Execute 执行 Home Assistant 工具。
func (t *HomeAssistantTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	if t.token == "" {
		return "", fmt.Errorf("HASS_TOKEN 未配置")
	}

	action := getString(args, "action", "")
	if action == "" {
		return "", fmt.Errorf("action 参数是必填项")
	}

	switch action {
	case "list_entities":
		return t.listEntities(ctx, args)
	case "get_state":
		return t.getState(ctx, args)
	case "list_services":
		return t.listServices(ctx, args)
	case "call_service":
		return t.callService(ctx, args)
	default:
		return "", fmt.Errorf("未知操作: %s", action)
	}
}

// ───────────────────────────── 操作实现 ─────────────────────────────

// listEntities 列出实体。
func (t *HomeAssistantTool) listEntities(ctx context.Context, args map[string]any) (string, error) {
	endpoint := "/states"
	domainFilter := getString(args, "domain", "")

	resp, err := t.callAPI(ctx, "GET", endpoint, nil)
	if err != nil {
		return "", err
	}

	// 从 resp 中提取数组
	var entities []any
	if rawList, ok := resp["raw"].([]any); ok {
		entities = rawList
	} else {
		entities = getListAnyFromMap(resp, "entities")
	}

	var filtered []map[string]any
	for _, entity := range entities {
		entityMap, ok := entity.(map[string]any)
		if !ok {
			continue
		}

		entityID := getString(entityMap, "entity_id", "")
		if domainFilter != "" {
			if !strings.HasPrefix(entityID, domainFilter+".") {
				continue
			}
		}

		// 简化实体信息
		filtered = append(filtered, map[string]any{
			"entity_id":    entityID,
			"state":        getString(entityMap, "state", ""),
			"name":         getString(getMap(entityMap, "attributes"), "friendly_name", entityID),
			"last_changed": getString(entityMap, "last_changed", ""),
		})
	}

	return jsonResult(map[string]any{
		"success":  true,
		"count":    len(filtered),
		"entities": filtered,
	})
}

// getState 获取单个实体状态。
func (t *HomeAssistantTool) getState(ctx context.Context, args map[string]any) (string, error) {
	entityID := getString(args, "entity_id", "")
	if entityID == "" {
		return "", fmt.Errorf("entity_id 参数是必填项")
	}

	if err := validateHAInput(entityID); err != nil {
		return "", fmt.Errorf("entity_id 无效: %w", err)
	}

	endpoint := "/states/" + entityID

	resp, err := t.callAPI(ctx, "GET", endpoint, nil)
	if err != nil {
		return "", err
	}

	return jsonResult(map[string]any{
		"success": true,
		"entity":  resp,
	})
}

// listServices 列出可用服务。
func (t *HomeAssistantTool) listServices(ctx context.Context, args map[string]any) (string, error) {
	endpoint := "/services"

	resp, err := t.callAPI(ctx, "GET", endpoint, nil)
	if err != nil {
		return "", err
	}

	// 从 resp 中提取数组
	var services []any
	if rawList, ok := resp["raw"].([]any); ok {
		services = rawList
	} else {
		services = getListAnyFromMap(resp, "services")
	}

	var filtered []map[string]any
	for _, service := range services {
		serviceMap, ok := service.(map[string]any)
		if !ok {
			continue
		}

		domain := getString(serviceMap, "domain", "")
		if isBlockedDomain(domain) {
			continue
		}

		filtered = append(filtered, serviceMap)
	}

	return jsonResult(map[string]any{
		"success":  true,
		"count":    len(filtered),
		"services": filtered,
	})
}

// callService 调用服务。
func (t *HomeAssistantTool) callService(ctx context.Context, args map[string]any) (string, error) {
	domain := getString(args, "domain", "")
	service := getString(args, "service", "")
	entityID := getString(args, "entity_id", "")

	if domain == "" || service == "" {
		return "", fmt.Errorf("domain 和 service 参数是必填项")
	}

	if err := validateHAInput(domain); err != nil {
		return "", fmt.Errorf("domain 无效: %w", err)
	}
	if err := validateHAInput(service); err != nil {
		return "", fmt.Errorf("service 无效: %w", err)
	}

	// 检查危险 domain
	if isBlockedDomain(domain) {
		return "", fmt.Errorf("domain %s 已被阻止，不允许调用", domain)
	}

	// 构建请求体
	body := map[string]any{}
	if entityID != "" {
		body["entity_id"] = entityID
	}

	// 合并额外参数
	data := getMap(args, "data")
	for k, v := range data {
		body[k] = v
	}
	endpoint := "/services/" + domain + "/" + service

	resp, err := t.callAPI(ctx, "POST", endpoint, body)
	if err != nil {
		return "", err
	}

	return jsonResult(map[string]any{
		"success":  true,
		"message":  fmt.Sprintf("服务 %s.%s 已调用", domain, service),
		"response": resp,
	})
}

// ───────────────────────────── API 调用 ─────────────────────────────

// callAPI 调用 Home Assistant REST API。
func (t *HomeAssistantTool) callAPI(ctx context.Context, method string, endpoint string, body map[string]any) (map[string]any, error) {
	url := t.apiURL + endpoint

	var bodyBytes []byte
	if body != nil {
		var marshalErr error
		bodyBytes, marshalErr = json.Marshal(body)
		if marshalErr != nil {
			return nil, fmt.Errorf("序列化请求体失败: %w", marshalErr)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.token)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		slog.Warn("homeassistant API error response", "status", resp.StatusCode, "body", string(respBody))
		return nil, fmt.Errorf("home assistant API error (HTTP %d)", resp.StatusCode)
	}

	// 尝试解析为 JSON
	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		// 可能是数组
		return map[string]any{"raw": string(respBody)}, nil
	}

	return result, nil
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

func isBlockedDomain(domain string) bool {
	for _, blocked := range haBlockedDomains {
		if domain == blocked {
			return true
		}
	}
	return false
}

func validateHAInput(s string) error {
	if strings.Contains(s, "/") || strings.Contains(s, "\\") ||
		strings.Contains(s, "..") || strings.ContainsAny(s, "?#") {
		return fmt.Errorf("值包含非法字符: %s", s)
	}
	if strings.ContainsRune(s, 0) {
		return fmt.Errorf("值包含空字节")
	}
	return nil
}
