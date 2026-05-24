// Package tool 提供 MoA (Mixture of Agents) 混合代理工具。
// 通过并行调用多个 LLM 模型并聚合响应来提升输出质量。
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
	"sync"
	"time"
)

// ───────────────────────────── 常量 ─────────────────────────────

const (
	moaMinSuccessfulReferences = 1
	moaMaxRetries              = 6
	moaReferenceTemperature    = 0.6
	moaAggregatorTemperature   = 0.4
	moaDefaultTimeout          = 120 * time.Second
)

// 参考模型列表
var moaReferenceModels = []string{
	"anthropic/claude-opus-4-6",
	"anthropic/claude-sonnet-4-6",
	"openai/gpt-4.1",
	"google/gemini-2.5-pro",
}

// 聚合模型
const moaAggregatorModel = "anthropic/claude-opus-4-6"

// 聚合器系统提示
const moaAggregatorSystemPrompt = `你是一个响应聚合器。你的任务是批判性地评估多个 AI 模型的响应，并生成一个精炼、准确、全面的最终响应。

指导原则：
1. 不要简单复制任何单个响应
2. 识别各响应中的正确和错误信息
3. 综合各响应的优点
4. 添加你自己的分析和见解
5. 确保最终响应清晰、准确、有用
6. 保持适当的格式和结构

请基于以下参考响应生成最终答案。`

// ───────────────────────────── MoATool ─────────────────────────────

// MoATool MoA 混合代理工具。
type MoATool struct {
	llmProvider   LLMProvider
	openRouterKey string
	initOnce      sync.Once
}

// MoAToolResult MoA 工具结果。
type MoAToolResult struct {
	Success    bool     `json:"success"`
	Response   string   `json:"response"`
	ModelsUsed []string `json:"models_used"`
	Error      string   `json:"error,omitempty"`
}

// LLMProvider LLM 提供者接口。
type LLMProvider interface {
	Call(ctx context.Context, model string, systemPrompt string, userPrompt string, temperature float64) (string, error)
}

// OpenRouterProvider OpenRouter 提供者。
type OpenRouterProvider struct {
	apiKey string
}

// Call 调用 OpenRouter API。
func (p *OpenRouterProvider) Call(ctx context.Context, model string, systemPrompt string, userPrompt string, temperature float64) (string, error) {
	// 使用 OpenRouter API
	url := "https://openrouter.ai/api/v1/chat/completions"

	body := map[string]any{
		"model": model,
		"messages": []map[string]any{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
		"temperature": temperature,
	}

	bodyBytes, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("HTTP-Referer", "https://github.com/nexus-agent")
	req.Header.Set("X-Title", "Nexus Agent")

	client := &http.Client{Timeout: moaDefaultTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseSize))
	if err != nil {
		return "", err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("API 错误 (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("解析响应失败: %w", err)
	}

	choices, ok := result["choices"].([]any)
	if !ok {
		return "", fmt.Errorf("无响应内容")
	}

	firstChoice, ok := choices[0].(map[string]any)
	if !ok {
		return "", fmt.Errorf("响应格式错误")
	}
	message := getMap(firstChoice, "message")
	content := getString(message, "content", "")

	return content, nil
}

// NewMoATool 创建 MoA 工具。
func NewMoATool() *MoATool {
	openRouterKey := os.Getenv("OPENROUTER_API_KEY")
	return &MoATool{
		openRouterKey: openRouterKey,
	}
}

// SetLLMProvider 设置 LLM 提供者。
func (t *MoATool) SetLLMProvider(provider LLMProvider) {
	t.llmProvider = provider
}

// ───────────────────────────── 工具接口 ─────────────────────────────

// Name 返回工具名称。
func (t *MoATool) Name() string { return "mixture_of_agents" }

// Description 返回工具描述。
func (t *MoATool) Description() string {
	return "使用多个 AI 模型并行生成响应并聚合，提升输出质量。"
}

// Schema 返回工具 Schema。
func (t *MoATool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"user_prompt": map[string]any{
					"type":        "string",
					"description": "用户问题或提示词",
				},
			},
			"required": []string{"user_prompt"},
		},
	}
}

// Toolset 返回工具集名称。
func (t *MoATool) Toolset() string { return "llm" }

// IsAvailable 检查工具是否可用。
func (t *MoATool) IsAvailable() bool {
	return os.Getenv("OPENROUTER_API_KEY") != ""
}

// Emoji 返回工具图标。
func (t *MoATool) Emoji() string { return "🧠" }

// MaxResultChars 返回最大结果字符数。
func (t *MoATool) MaxResultChars() int { return 50000 }

// Execute 执行 MoA 工具。
func (t *MoATool) Execute(ctx context.Context, args map[string]any) (string, error) {
	userPrompt := getString(args, "user_prompt", "")
	if userPrompt == "" {
		return "", fmt.Errorf("user_prompt 参数是必填项")
	}

	// 初始化 LLM 提供者
	t.initOnce.Do(func() {
		if t.llmProvider == nil && t.openRouterKey != "" {
			t.llmProvider = &OpenRouterProvider{apiKey: t.openRouterKey}
		}
	})
	if t.llmProvider == nil {
		return "", fmt.Errorf("OPENROUTER_API_KEY 未配置")
	}

	// Layer 1: 并行调用参考模型
	referenceResponses := t.callReferenceModels(ctx, userPrompt)

	// 检查最小成功数
	if len(referenceResponses) < moaMinSuccessfulReferences {
		return "", fmt.Errorf("参考模型响应不足 (需要 %d，成功 %d)", moaMinSuccessfulReferences, len(referenceResponses))
	}

	// Layer 2: 聚合响应
	finalResponse := t.aggregateResponses(ctx, userPrompt, referenceResponses)

	// 构建结果
	result := MoAToolResult{
		Success:    true,
		Response:   finalResponse,
		ModelsUsed: moaReferenceModels,
	}

	resultBytes, _ := json.Marshal(result)
	return string(resultBytes), nil
}

// callReferenceModels 并行调用参考模型。
func (t *MoATool) callReferenceModels(ctx context.Context, userPrompt string) []string {
	var responses []string
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, model := range moaReferenceModels {
		wg.Add(1)
		go func(m string) {
			defer wg.Done()

			response, err := t.callWithRetry(ctx, m, "", userPrompt, moaReferenceTemperature)
			if err != nil {
				slog.Warn("[MoA] reference model call failed", "model", m, "err", err)
				return
			}

			mu.Lock()
			responses = append(responses, response)
			mu.Unlock()
		}(model)
	}

	wg.Wait()
	return responses
}

// callWithRetry 带重试的模型调用。
func (t *MoATool) callWithRetry(ctx context.Context, model, systemPrompt, userPrompt string, temperature float64) (string, error) {
	backoff := []time.Duration{1 * time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second, 60 * time.Second}

	for i := 0; i < moaMaxRetries; i++ {
		response, err := t.llmProvider.Call(ctx, model, systemPrompt, userPrompt, temperature)
		if err == nil {
			return response, nil
		}

		slog.Debug("[MoA] model call failed, retrying", "model", model, "attempt", i+1, "err", err)

		if i < len(backoff) {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(backoff[i]):
			}
		}
	}

	return "", fmt.Errorf("模型 %s 调用失败，已重试 %d 次", model, moaMaxRetries)
}

// aggregateResponses 聚合多个响应。
func (t *MoATool) aggregateResponses(ctx context.Context, userPrompt string, responses []string) string {
	// 构建聚合器提示
	aggregatorPrompt := t.constructAggregatorPrompt(userPrompt, responses)

	// 调用聚合模型
	finalResponse, err := t.callWithRetry(ctx, moaAggregatorModel, moaAggregatorSystemPrompt, aggregatorPrompt, moaAggregatorTemperature)
	if err != nil {
		slog.Warn("[MoA] aggregation failed, returning first reference response", "err", err)
		if len(responses) > 0 {
			return responses[0]
		}
		return ""
	}

	return finalResponse
}

// constructAggregatorPrompt 构建聚合器提示。
func (t *MoATool) constructAggregatorPrompt(userPrompt string, responses []string) string {
	var builder strings.Builder

	builder.WriteString("用户问题:\n")
	builder.WriteString(userPrompt)
	builder.WriteString("\n\n")
	builder.WriteString("参考响应:\n")

	for i, response := range responses {
		builder.WriteString(fmt.Sprintf("\n--- 模型 %d 响应 ---\n", i+1))
		builder.WriteString(response)
		builder.WriteString("\n")
	}

	builder.WriteString("\n请基于以上参考响应生成最终答案。")

	return builder.String()
}

// ───────────────────────────── 注册工具 ─────────────────────────────

func init() {
	GetRegistry().Register(&MoATool{})
}