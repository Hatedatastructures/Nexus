// Package tool 提供文本转语音 (TTS) 工具。
// 通过 OpenAI TTS API 将文本转换为音频文件。
package tool

import (
	"context"
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

// ───────────────────────────── 文本转语音工具 ─────────────────────────────

// TTSTool 实现文本到语音的转换。
// 通过 OpenAI TTS API 将文本转换为 MP3 等格式的音频文件。
type TTSTool struct {
	client *http.Client
}

// Name 返回工具名称。
func (t *TTSTool) Name() string { return "text_to_speech" }

// Description 返回工具描述。
func (t *TTSTool) Description() string {
	return "将文本转换为语音音频文件。支持多种语音、语言和输出格式，可用于朗读、播客制作等场景。"
}

// Toolset 返回工具所属工具集。
func (t *TTSTool) Toolset() string { return "audio" }

// Emoji 返回工具图标。
func (t *TTSTool) Emoji() string { return "🔊" }

// IsAvailable 检查 TTS 是否可用。
// 需要配置 OpenAI API Key。
func (t *TTSTool) IsAvailable() bool {
	return os.Getenv("OPENAI_API_KEY") != "" || os.Getenv("HERMES_LLM_API_KEY") != ""
}

// MaxResultChars 返回结果最大字符数。
func (t *TTSTool) MaxResultChars() int { return 5000 }

// Schema 返回工具的 JSON Schema。
func (t *TTSTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "text_to_speech",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{
					"type":        "string",
					"description": "要转换为语音的文本内容",
				},
				"output_path": map[string]any{
					"type":        "string",
					"description": "输出音频文件的路径 (支持 .mp3, .wav, .opus, .aac, .flac)",
				},
				"voice": map[string]any{
					"type":        "string",
					"description": "语音类型，可选: alloy, echo, fable, onyx, nova, shimmer，默认 alloy",
				},
				"language": map[string]any{
					"type":        "string",
					"description": "语言代码，如 zh, en, ja, ko 等 (可选，部分模型支持)",
				},
				"model": map[string]any{
					"type":        "string",
					"description": "使用的 TTS 模型，如 tts-1, tts-1-hd，默认 tts-1",
				},
				"speed": map[string]any{
					"type":        "number",
					"description": "语速倍率，范围 0.25 ~ 4.0，默认 1.0",
				},
			},
			"required": []string{"text", "output_path"},
		},
	}
}

// Execute 执行文本转语音。
// 调用 TTS API → 保存音频文件 → 返回结果。
func (t *TTSTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	text, ok := args["text"].(string)
	if !ok || text == "" {
		return ToolError("参数 text 是必填项且必须为字符串"), nil
	}

	outputPath, ok := args["output_path"].(string)
	if !ok || outputPath == "" {
		return ToolError("参数 output_path 是必填项且必须为字符串"), nil
	}

	voice := "alloy"
	if v, ok := args["voice"].(string); ok && v != "" {
		voice = v
	}

	language, _ := args["language"].(string)

	model := "tts-1"
	if m, ok := args["model"].(string); ok && m != "" {
		model = m
	}

	speed := 1.0
	if s, ok := args["speed"].(float64); ok && s > 0 {
		speed = s
		if speed < 0.25 {
			speed = 0.25
		}
		if speed > 4.0 {
			speed = 4.0
		}
	}

	// 安全敏感路径检查
	if isPathSensitive(outputPath) {
		slog.Warn("TTS save blocked (sensitive path)", "path", outputPath)
		return ToolError(fmt.Sprintf("安全限制: 不允许写入敏感路径 %s", outputPath)), nil
	}

	// 调用 TTS API
	audioData, err := t.convertToSpeech(ctx, model, text, voice, language, speed)
	if err != nil {
		slog.Error("TTS conversion failed", "err", err)
		return ToolError(fmt.Sprintf("语音转换失败: %v", err)), nil
	}

	// 保存音频文件
	if err := t.saveAudio(outputPath, audioData); err != nil {
		slog.Error("failed to save audio", "path", outputPath, "err", err)
		return ToolError(fmt.Sprintf("保存音频失败: %v", err)), nil
	}

	slog.Info("text-to-speech succeeded", "output", outputPath, "size", len(audioData))
	return ToolResult(map[string]any{
		"output":     fmt.Sprintf("语音生成成功，已保存到: %s (%s)", outputPath, formatFileSize(len(audioData))),
		"path":       outputPath,
		"size_bytes": len(audioData),
		"voice":      voice,
		"model":      model,
		"speed":      speed,
	}), nil
}

// convertToSpeech 调用 TTS API 将文本转换为语音。
func (t *TTSTool) convertToSpeech(ctx context.Context, model, text, voice, language string, speed float64) ([]byte, error) {
	if t.client == nil {
		t.client = &http.Client{Timeout: 120 * time.Second}
	}

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("HERMES_LLM_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("未配置 API Key，请设置 OPENAI_API_KEY 或 HERMES_LLM_API_KEY")
	}

	baseURL := os.Getenv("OPENAI_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	baseURL = strings.TrimRight(baseURL, "/")

	// 构建请求体
	reqBody := map[string]any{
		"model":  model,
		"input":  text,
		"voice":  voice,
		"speed":  speed,
	}


	if language != "" {
		reqBody["language"] = language
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/v1/audio/speech", strings.NewReader(string(bodyBytes)))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("API 错误 (HTTP %d): %s", resp.StatusCode, string(errBody))
	}

	// 读取音频数据
	audioData, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseSize))
	if err != nil {
		return nil, fmt.Errorf("读取音频数据失败: %w", err)
	}

	return audioData, nil
}

// saveAudio 将音频数据保存到文件。
func (t *TTSTool) saveAudio(path string, data []byte) error {
	// 确保父目录存在
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("写入文件失败: %w", err)
	}

	return nil
}

// formatFileSize 格式化字节大小。
func formatFileSize(bytes int) string {
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
	GetRegistry().Register(&TTSTool{})
}
