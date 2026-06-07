// Package llm 提供 LLM 提供者抽象层。
package llm

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ── Gemini 请求体构建 ─────────────────────────────────────────────────────

// geminiRequestBody Gemini generateContent 请求体。
type geminiRequestBody struct {
	Contents          []geminiRequestContent   `json:"contents"`
	SystemInstruction *geminiSystemInstruction `json:"systemInstruction,omitempty"`
	Tools             []geminiToolDeclaration  `json:"tools,omitempty"`
	ToolConfig        *geminiToolConfig        `json:"toolConfig,omitempty"`
	GenerationConfig  *geminiGenerationConfig  `json:"generationConfig,omitempty"`
	CachedContent     string                   `json:"cachedContent,omitempty"` // 缓存内容资源名称
}

// geminiRequestContent Gemini 请求内容。
type geminiRequestContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

// geminiSystemInstruction Gemini 系统指令。
type geminiSystemInstruction struct {
	Parts []geminiPart `json:"parts"`
}

// geminiToolDeclaration Gemini 工具声明。
type geminiToolDeclaration struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations,omitempty"`
}

// geminiFunctionDeclaration Gemini 函数声明。
type geminiFunctionDeclaration struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// geminiToolConfig Gemini 工具配置。
type geminiToolConfig struct {
	FunctionCallingConfig *geminiFunctionCallingConfig `json:"functionCallingConfig,omitempty"`
}

// geminiFunctionCallingConfig Gemini 函数调用配置。
type geminiFunctionCallingConfig struct {
	Mode                 string   `json:"mode,omitempty"`
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

// geminiGenerationConfig Gemini 生成配置。
type geminiGenerationConfig struct {
	Temperature     *float64              `json:"temperature,omitempty"`
	MaxOutputTokens int                   `json:"maxOutputTokens,omitempty"`
	TopP            *float64              `json:"topP,omitempty"`
	StopSequences   []string              `json:"stopSequences,omitempty"`
	ThinkingConfig  *geminiThinkingConfig `json:"thinkingConfig,omitempty"`
}

// geminiThinkingConfig Gemini 思维配置。
type geminiThinkingConfig struct {
	ThinkingBudget  int    `json:"thinkingBudget,omitempty"`
	IncludeThoughts bool   `json:"includeThoughts,omitempty"`
	ThinkingLevel   string `json:"thinkingLevel,omitempty"`
}

// buildGeminiRequestBody 构建 Gemini API 请求体。
func buildGeminiRequestBody(req *ChatRequest) *geminiRequestBody {
	body := &geminiRequestBody{}

	// 分离 system 消息构建系统指令
	var systemTextParts []string
	var contentList []geminiRequestContent

	// 构建 callID → toolName 索引，用于 RoleTool 消息查找函数名
	callIDToName := make(map[string]string)
	for i := range req.Messages {
		if req.Messages[i].Role == RoleAssistant && len(req.Messages[i].ToolCalls) > 0 {
			for _, tc := range req.Messages[i].ToolCalls {
				callIDToName[tc.ID] = tc.Name
			}
		}
	}

	for _, msg := range req.Messages {
		if msg.Role == RoleSystem {
			systemTextParts = append(systemTextParts, msg.Content)
			continue
		}

		gemMsg := convertMessageToGemini(&msg, callIDToName)
		if gemMsg != nil {
			contentList = append(contentList, *gemMsg)
		}
	}

	body.Contents = contentList

	// 系统指令
	joinedSystem := strings.Join(systemTextParts, "\n")
	if strings.TrimSpace(joinedSystem) != "" {
		body.SystemInstruction = &geminiSystemInstruction{
			Parts: []geminiPart{{Text: joinedSystem}},
		}
	}

	// 工具定义
	if len(req.Tools) > 0 {
		declarations := make([]geminiFunctionDeclaration, 0, len(req.Tools))
		for _, t := range req.Tools {
			params := t.Parameters
			paramsMap, ok := params.(map[string]any)
			if !ok {
				paramsMap = map[string]any{
					"type": "object",
				}
			}
			declarations = append(declarations, geminiFunctionDeclaration{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  paramsMap,
			})
		}
		body.Tools = []geminiToolDeclaration{{FunctionDeclarations: declarations}}
	}

	// 生成配置
	genCfg := &geminiGenerationConfig{}
	hasGenCfg := false

	if req.MaxTokens > 0 {
		genCfg.MaxOutputTokens = req.MaxTokens
		hasGenCfg = true
	}

	if req.Temperature > 0 {
		temp := req.Temperature
		genCfg.Temperature = &temp
		hasGenCfg = true
	}

	if hasGenCfg {
		body.GenerationConfig = genCfg
	}

	// 缓存控制：通过 Metadata 中的 "cached_content" 字段引用预创建的缓存内容
	if req.Metadata != nil {
		if cc, ok := req.Metadata["cached_content"].(string); ok && cc != "" {
			body.CachedContent = cc
		}
		// 思维链配置（通过 Metadata 传递）
		if thinkingBudget, ok := req.Metadata["thinking_budget"].(int); ok && thinkingBudget > 0 {
			if body.GenerationConfig == nil {
				body.GenerationConfig = &geminiGenerationConfig{}
			}
			body.GenerationConfig.ThinkingConfig = &geminiThinkingConfig{
				ThinkingBudget:  thinkingBudget,
				IncludeThoughts: true,
			}
		}
		// 推理级别（通过 Metadata 传递）
		if thinkingLevel, ok := req.Metadata["thinking_level"].(string); ok && thinkingLevel != "" {
			if body.GenerationConfig == nil {
				body.GenerationConfig = &geminiGenerationConfig{}
			}
			if body.GenerationConfig.ThinkingConfig == nil {
				body.GenerationConfig.ThinkingConfig = &geminiThinkingConfig{
					IncludeThoughts: true,
				}
			}
			body.GenerationConfig.ThinkingConfig.ThinkingLevel = thinkingLevel
		}
	}

	return body
}

// convertMessageToGemini 将统一消息转换为 Gemini 格式。
func convertMessageToGemini(msg *Message, callIDToName map[string]string) *geminiRequestContent {
	switch msg.Role {
	case RoleUser:
		parts := []geminiPart{}
		if msg.Content != "" {
			parts = append(parts, geminiPart{Text: msg.Content})
		}
		return &geminiRequestContent{
			Role:  "user",
			Parts: parts,
		}

	case RoleAssistant:
		parts := []geminiPart{}
		// 文本内容
		if msg.Content != "" {
			parts = append(parts, geminiPart{Text: msg.Content})
		}
		// 工具调用
		for _, tc := range msg.ToolCalls {
			var args map[string]any
			if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
				args = map[string]any{"_raw": tc.Arguments}
			}
			part := geminiPart{
				FunctionCall: &geminiFunctionCall{
					Name: tc.Name,
					Args: args,
				},
			}
			// 保留 thought_signature
			if tc.Extra != nil {
				if sig, ok := tc.Extra["thought_signature"].(string); ok && sig != "" {
					part.ThoughtSignature = sig
				}
			}
			parts = append(parts, part)
		}
		return &geminiRequestContent{
			Role:  "model",
			Parts: parts,
		}

	case RoleTool:
		// 工具结果也作为 user 角色的 functionResponse
		content := msg.Content
		if content == "" {
			content = "{}"
		}
		var responseMap map[string]any
		if err := json.Unmarshal([]byte(content), &responseMap); err != nil {
			responseMap = map[string]any{"output": content}
		}
		// 查找函数名：优先 msg.Name，然后从 callIDToName 查找
		funcName := msg.Name
		if funcName == "" {
			funcName = callIDToName[msg.ToolCallID]
		}
		if funcName == "" {
			funcName = "unknown_function"
		}
		return &geminiRequestContent{
			Role: "user",
			Parts: []geminiPart{{
				FunctionResponse: &geminiFunctionResponse{
					Name:     funcName,
					Response: responseMap,
				},
			}},
		}

	default:
		return &geminiRequestContent{
			Role:  "user",
			Parts: []geminiPart{{Text: msg.Content}},
		}
	}
}

// ── Gemini API 类型 ────────────────────────────────────────────────────────

// geminiGenerateResponse Gemini generateContent 响应。
type geminiGenerateResponse struct {
	Candidates    []geminiCandidate   `json:"candidates"`
	UsageMetadata geminiUsageMetadata `json:"usageMetadata,omitempty"`
	ModelVersion  string              `json:"modelVersion,omitempty"`
}

// geminiCandidate 候选响应。
type geminiCandidate struct {
	Content       *geminiContent `json:"content"`
	FinishReason  string         `json:"finishReason,omitempty"`
	SafetyRatings []any          `json:"safetyRatings,omitempty"`
}

// geminiContent Gemini 内容。
type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts,omitempty"`
}

// geminiPart Gemini 内容部分。
type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	Thought          bool                    `json:"thought,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
	ThoughtSignature string                  `json:"thoughtSignature,omitempty"`
	InlineData       *geminiInlineData       `json:"inlineData,omitempty"`
}

// geminiFunctionCall Gemini 函数调用。
type geminiFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

// geminiFunctionResponse Gemini 函数响应。
type geminiFunctionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

// geminiInlineData Gemini 内联数据（图片等）。
type geminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

// geminiUsageMetadata Gemini token 用量元数据。
type geminiUsageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount,omitempty"`
	CandidatesTokenCount    int `json:"candidatesTokenCount,omitempty"`
	TotalTokenCount         int `json:"totalTokenCount,omitempty"`
	CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
}

// geminiModelListResponse Gemini 模型列表 API 响应。
type geminiModelListResponse struct {
	Models []geminiModelEntry `json:"models"`
}

// geminiModelEntry Gemini 模型条目。
type geminiModelEntry struct {
	Name                       string   `json:"name"`
	DisplayName                string   `json:"displayName,omitempty"`
	Description                string   `json:"description,omitempty"`
	InputTokenLimit            int      `json:"inputTokenLimit,omitempty"`
	OutputTokenLimit           int      `json:"outputTokenLimit,omitempty"`
	SupportedGenerationMethods []string `json:"supportedGenerationMethods,omitempty"`
	Deprecated                 bool     `json:"deprecated,omitempty"`
}

// ── 工具函数 ──────────────────────────────────────────────────────────────

// mapGeminiStopReason 将 Gemini finishReason 映射为统一的停止原因。
func mapGeminiStopReason(reason string, hasToolCalls bool) string {
	if hasToolCalls {
		return StopToolUse
	}
	switch strings.ToUpper(reason) {
	case "STOP":
		return StopEndTurn
	case "MAX_TOKENS":
		return StopMaxTokens
	case "SAFETY", "RECITATION":
		return StopContentFilter
	default:
		return StopEndTurn
	}
}

// convertGeminiUsage 将 Gemini usageMetadata 转换为统一的 TokenUsage。
func convertGeminiUsage(meta *geminiUsageMetadata) *TokenUsage {
	if meta == nil {
		return nil
	}
	return &TokenUsage{
		PromptTokens:     meta.PromptTokenCount,
		CompletionTokens: meta.CandidatesTokenCount,
		TotalTokens:      meta.TotalTokenCount,
		CacheReadTokens:  meta.CachedContentTokenCount,
	}
}

// generateShortID 生成简短的唯一 ID (使用 crypto/rand)。
func generateShortID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return fmt.Sprintf("%016x", b)
}
