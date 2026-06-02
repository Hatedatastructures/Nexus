// Package tool 提供视觉分析工具。
// 通过 LLM 的多模态能力分析图片内容。
package tool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ───────────────────────────── 视觉分析工具 ─────────────────────────────

// VisionAnalyzeTool 实现图片视觉分析功能。
// 通过读取图片文件并以 base64 编码发送给支持多模态的 LLM 进行分析。
type VisionAnalyzeTool struct {
	client *http.Client
	once   sync.Once
}

// Name 返回工具名称。
func (t *VisionAnalyzeTool) Name() string { return "vision_analyze" }

// Description 返回工具描述。
func (t *VisionAnalyzeTool) Description() string {
	return "分析图片内容。读取本地图片文件并通过多模态 LLM 进行视觉理解，返回图片描述、对象识别、文字提取等分析结果。"
}

// Toolset 返回工具所属工具集。
func (t *VisionAnalyzeTool) Toolset() string { return "vision" }

// Emoji 返回工具图标。
func (t *VisionAnalyzeTool) Emoji() string { return "👁️" }

// IsAvailable 检查视觉分析是否可用。
// 需要配置 OpenAI 兼容的 API Key 和支持视觉的模型。
func (t *VisionAnalyzeTool) IsAvailable() bool {
	return os.Getenv("OPENAI_API_KEY") != "" || os.Getenv("HERMES_LLM_API_KEY") != ""
}

// MaxResultChars 返回结果最大字符数。
func (t *VisionAnalyzeTool) MaxResultChars() int { return 50000 }

// Schema 返回工具的 JSON Schema。
func (t *VisionAnalyzeTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "vision_analyze",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"image_path": map[string]any{
					"type":        "string",
					"description": "要分析的图片文件路径 (支持 JPG, PNG, GIF, WebP)",
				},
				"prompt": map[string]any{
					"type":        "string",
					"description": "分析提示词，例如 '描述这张图片'、'识别图中的文字'、'统计图中物体数量' 等",
				},
				"model": map[string]any{
					"type":        "string",
					"description": "使用的模型名称，默认使用环境变量配置的模型",
				},
			},
			"required": []string{"image_path"},
		},
	}
}

// Execute 执行视觉分析。
// 读取图片 → 编码为 base64 → 发送给多模态 LLM → 返回分析结果。
func (t *VisionAnalyzeTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	imagePath, ok := args["image_path"].(string)
	if !ok || imagePath == "" {
		return ToolError("参数 image_path 是必填项且必须为字符串"), nil
	}

	prompt := "描述这张图片"
	if p, ok := args["prompt"].(string); ok && p != "" {
		prompt = p
	}

	model := "gpt-4o"
	if m, ok := args["model"].(string); ok && m != "" {
		model = m
	}

	// 安全检查: 路径安全 + 敏感路径
	if _, err := checkPathSecurity(imagePath, true); err != nil {
		return ToolError(fmt.Sprintf("安全限制: %v", err)), nil
	}
	if isPathSensitive(imagePath) {
		slog.Warn("vision analysis blocked (sensitive path)", "path", imagePath)
		return ToolError(fmt.Sprintf("安全限制: 不允许访问敏感路径 %s", imagePath)), nil
	}

	// 读取图片文件
	imageData, mimeType, err := t.readImage(imagePath)
	if err != nil {
		slog.Warn("image read failed", "path", imagePath, "err", err)
		return ToolError(fmt.Sprintf("读取图片失败: %v", err)), nil
	}

	// 编码为 base64
	base64Data := base64.StdEncoding.EncodeToString(imageData)

	// 调用多模态 LLM
	result, apiErr := t.analyzeImage(ctx, model, prompt, base64Data, mimeType)
	if apiErr != nil {
		slog.Error("vision analysis API call failed", "model", model, "err", apiErr)
		return ToolError(fmt.Sprintf("视觉分析失败: %v", apiErr)), nil
	}

	slog.Info("vision analysis succeeded", "path", imagePath, "model", model)
	return ToolResult(map[string]any{
		"output":  result,
		"image":   imagePath,
		"model":   model,
		"prompt":  prompt,
	}), nil
}

// readImage 读取图片文件并返回数据和 MIME 类型。
func (t *VisionAnalyzeTool) readImage(path string) ([]byte, string, error) {
	// 检查文件大小 (限制 20MB)
	info, err := os.Stat(path)
	if err != nil {
		return nil, "", fmt.Errorf("无法访问文件: %w", err)
	}
	if info.Size() > 20*1024*1024 {
		return nil, "", fmt.Errorf("图片文件过大 (最大 20MB): %s", formatSize(info.Size()))
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("读取文件失败: %w", err)
	}

	// 检测 MIME 类型
	ext := strings.ToLower(filepath.Ext(path))
	mimeType := mime.TypeByExtension(ext)
	if mimeType == "" {
		// 默认 JPEG
		mimeType = "image/jpeg"
	}
	// 确保是图片类型
	if !strings.HasPrefix(mimeType, "image/") {
		mimeType = "image/jpeg"
	}

	return data, mimeType, nil
}

// analyzeImage 调用多模态 LLM API 分析图片。
func (t *VisionAnalyzeTool) analyzeImage(ctx context.Context, model, prompt, base64Data, mimeType string) (string, error) {
	t.once.Do(func() {
		t.client = &http.Client{Timeout: 120 * time.Second}
	})

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("HERMES_LLM_API_KEY")
	}
	if apiKey == "" {
		return "", fmt.Errorf("未配置 API Key，请设置 OPENAI_API_KEY 或 HERMES_LLM_API_KEY")
	}

	baseURL := os.Getenv("OPENAI_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	baseURL = strings.TrimRight(baseURL, "/")

	// 构建 OpenAI Vision 请求体
	reqBody := map[string]any{
		"model": model,
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{
						"type": "text",
						"text": prompt,
					},
					{
						"type": "image_url",
						"image_url": map[string]any{
							"url": fmt.Sprintf("data:%s;base64,%s", mimeType, base64Data),
						},
					},
				},
			},
		},
		"max_tokens": 4096,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("序列化请求体失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/v1/chat/completions", strings.NewReader(string(bodyBytes)))
	if err != nil {
		return "", fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return "", fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		slog.Warn("vision API error response", "status", resp.StatusCode, "body", string(respBody))
		return "", fmt.Errorf("视觉分析 API 返回错误 (HTTP %d)", resp.StatusCode)
	}

	// 解析响应
	var apiResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return "", fmt.Errorf("解析响应失败: %w", err)
	}

	if len(apiResp.Choices) == 0 || apiResp.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("API 返回空响应")
	}

	return apiResp.Choices[0].Message.Content, nil
}

// formatSize 格式化字节大小为可读字符串。
func formatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	units := []string{"KB", "MB", "GB"}
	return fmt.Sprintf("%.1f %s", float64(bytes)/float64(div), units[exp])
}

// ───────────────────────────── init 注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&VisionAnalyzeTool{})
}
