// Package tool 提供图像生成工具。
// 通过 OpenAI DALL-E 兼容 API 根据文本描述生成图片。
package tool

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ───────────────────────────── 图像生成工具 ─────────────────────────────

// ImageGenTool 实现文本到图像的生成。
// 通过 OpenAI DALL-E API 或兼容端点，根据文本描述生成图片。
type ImageGenTool struct {
	client *http.Client
}

// Name 返回工具名称。
func (t *ImageGenTool) Name() string { return "image_generation" }

// Description 返回工具描述。
func (t *ImageGenTool) Description() string {
	return "根据文本描述生成图片。使用 AI 图像生成模型 (如 DALL-E) 将文字描述转换为高质量图片。支持自定义尺寸和输出路径。"
}

// Toolset 返回工具所属工具集。
func (t *ImageGenTool) Toolset() string { return "image" }

// Emoji 返回工具图标。
func (t *ImageGenTool) Emoji() string { return "🎨" }

// IsAvailable 检查图像生成是否可用。
// 需要配置 OpenAI API Key 或兼容的图像生成 API Key。
func (t *ImageGenTool) IsAvailable() bool {
	return os.Getenv("OPENAI_API_KEY") != "" || os.Getenv("HERMES_LLM_API_KEY") != ""
}

// MaxResultChars 返回结果最大字符数。
func (t *ImageGenTool) MaxResultChars() int { return 10000 }

// Schema 返回工具的 JSON Schema。
func (t *ImageGenTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "image_generation",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prompt": map[string]any{
					"type":        "string",
					"description": "图片的文本描述，越详细越好",
				},
				"size": map[string]any{
					"type":        "string",
					"description": "图片尺寸，可选值: 1024x1024, 1024x1792, 1792x1024，默认 1024x1024",
				},
				"output_path": map[string]any{
					"type":        "string",
					"description": "输出图片的本地文件路径 (可选)，如果不指定则返回 URL",
				},
				"model": map[string]any{
					"type":        "string",
					"description": "使用的生成模型，如 dall-e-3, dall-e-2 等，默认 dall-e-3",
				},
				"quality": map[string]any{
					"type":        "string",
					"description": "图片质量，可选: standard, hd，默认 standard",
				},
			},
			"required": []string{"prompt"},
		},
	}
}

// Execute 执行图像生成。
// 调用图像生成 API → 保存图片或返回 URL。
func (t *ImageGenTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	prompt, ok := args["prompt"].(string)
	if !ok || prompt == "" {
		return ToolError("参数 prompt 是必填项且必须为字符串"), nil
	}

	size := "1024x1024"
	if s, ok := args["size"].(string); ok && s != "" {
		size = s
	}

	outputPath, _ := args["output_path"].(string)

	model := "dall-e-3"
	if m, ok := args["model"].(string); ok && m != "" {
		model = m
	}

	quality := "standard"
	if q, ok := args["quality"].(string); ok && q != "" {
		quality = q
	}

	// 调用 API 生成
	imageURL, base64Data, err := t.generateImage(ctx, model, prompt, size, quality)
	if err != nil {
		slog.Error("image generation failed", "prompt", prompt, "err", err)
		return ToolError(fmt.Sprintf("图像生成失败: %v", err)), nil
	}

	// 如果指定了输出路径，保存图片
	var savedPath string
	if outputPath != "" {
		// 安全敏感路径检查
		if isPathSensitive(outputPath) {
			slog.Warn("image save blocked (sensitive path)", "path", outputPath)
			return ToolError(fmt.Sprintf("安全限制: 不允许写入敏感路径 %s", outputPath)), nil
		}

		if err := t.saveImage(outputPath, base64Data, imageURL); err != nil {
			slog.Error("failed to save image", "path", outputPath, "err", err)
			return ToolError(fmt.Sprintf("保存图片失败: %v", err)), nil
		}
		savedPath = outputPath
	}

	slog.Info("image generation succeeded", "prompt", prompt[:min(50, len(prompt))], "model", model)

	result := map[string]any{
		"output": fmt.Sprintf("图片生成成功 (模型: %s, 尺寸: %s)", model, size),
		"model":  model,
		"size":   size,
	}
	if savedPath != "" {
		result["output"] = fmt.Sprintf("图片已保存到: %s", savedPath)
		result["saved_path"] = savedPath
	} else if imageURL != "" {
		result["url"] = imageURL
		result["output"] = fmt.Sprintf("图片生成成功，URL: %s", imageURL)
	}

	return ToolResult(result), nil
}

// generateImage 调用图像生成 API。
func (t *ImageGenTool) generateImage(ctx context.Context, model, prompt, size, quality string) (url string, b64Data string, err error) {
	if t.client == nil {
		t.client = &http.Client{Timeout: 180 * time.Second}
	}

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("HERMES_LLM_API_KEY")
	}
	if apiKey == "" {
		return "", "", fmt.Errorf("未配置 API Key，请设置 OPENAI_API_KEY 或 HERMES_LLM_API_KEY")
	}

	baseURL := os.Getenv("OPENAI_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	baseURL = strings.TrimRight(baseURL, "/")

	// 构建请求体
	reqBody := map[string]any{
		"model":  model,
		"prompt": prompt,
		"size":   size,
		"n":      1,
	}
	if quality != "" && model == "dall-e-3" {
		reqBody["quality"] = quality
	}

	// 优先请求 base64 格式 (方便本地保存)
	reqBody["response_format"] = "b64_json"

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", "", fmt.Errorf("序列化请求体失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/v1/images/generations", strings.NewReader(string(bodyBytes)))
	if err != nil {
		return "", "", fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := t.client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return "", "", fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("API 错误 (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	// 解析响应
	var apiResp struct {
		Created int64 `json:"created"`
		Data    []struct {
			URL           string `json:"url"`
			RevisedPrompt string `json:"revised_prompt"`
			B64JSON       string `json:"b64_json"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return "", "", fmt.Errorf("解析响应失败: %w", err)
	}

	if len(apiResp.Data) == 0 {
		return "", "", fmt.Errorf("API 返回空数据")
	}

	img := apiResp.Data[0]
	return img.URL, img.B64JSON, nil
}

// saveImage 将生成的图片保存到本地文件。
func (t *ImageGenTool) saveImage(path string, b64Data string, imageURL string) error {
	// 确保父目录存在
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	var imgData []byte
	var err error

	// 优先使用 base64 数据
	if b64Data != "" {
		imgData, err = base64.StdEncoding.DecodeString(b64Data)
		if err != nil {
			return fmt.Errorf("base64 解码失败: %w", err)
		}
	} else if imageURL != "" {
		// 通过 URL 下载
		if t.client == nil {
			t.client = &http.Client{Timeout: 120 * time.Second}
		}
		req, reqErr := http.NewRequest("GET", imageURL, nil)
		if reqErr != nil {
			return fmt.Errorf("创建下载请求失败: %w", reqErr)
		}
		resp, reqErr := t.client.Do(req)
		if reqErr != nil {
			return fmt.Errorf("下载图片失败: %w", reqErr)
		}
		defer resp.Body.Close()
		imgData, err = io.ReadAll(io.LimitReader(resp.Body, 50*1024*1024)) // 限制 50MB
		if err != nil {
			return fmt.Errorf("读取图片数据失败: %w", err)
		}
	} else {
		return fmt.Errorf("没有可用的图片数据")
	}

	if err := os.WriteFile(path, imgData, 0600); err != nil {
		return fmt.Errorf("写入文件失败: %w", err)
	}

	slog.Info("image saved", "path", path, "size", len(imgData))
	return nil
}

// ───────────────────────────── init 注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&ImageGenTool{})
}
