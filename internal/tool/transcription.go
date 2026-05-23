// Package tool 提供语音转录工具。
// 通过 OpenAI Whisper API 将音频文件转录为文本。
package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ───────────────────────────── 语音转录工具 ─────────────────────────────

// TranscriptionTool 实现音频到文本的转录。
// 通过 OpenAI Whisper API 或兼容端点，将音频文件转录为文本。
type TranscriptionTool struct {
	client *http.Client
}

// Name 返回工具名称。
func (t *TranscriptionTool) Name() string { return "transcribe_audio" }

// Description 返回工具描述。
func (t *TranscriptionTool) Description() string {
	return "将音频文件转录为文本。支持多种音频格式 (MP3, WAV, M4A, FLAC 等)，支持多语言识别和翻译。"
}

// Toolset 返回工具所属工具集。
func (t *TranscriptionTool) Toolset() string { return "audio" }

// Emoji 返回工具图标。
func (t *TranscriptionTool) Emoji() string { return "🎙️" }

// IsAvailable 检查转录功能是否可用。
// 需要配置 OpenAI API Key。
func (t *TranscriptionTool) IsAvailable() bool {
	return os.Getenv("OPENAI_API_KEY") != "" || os.Getenv("HERMES_LLM_API_KEY") != ""
}

// MaxResultChars 返回结果最大字符数。
func (t *TranscriptionTool) MaxResultChars() int { return 200000 }

// Schema 返回工具的 JSON Schema。
func (t *TranscriptionTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "transcribe_audio",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"audio_path": map[string]any{
					"type":        "string",
					"description": "要转录的音频文件路径 (支持 MP3, WAV, M4A, FLAC, OGG 等)",
				},
				"language": map[string]any{
					"type":        "string",
					"description": "音频语言代码 (ISO-639-1)，如 zh, en, ja 等。不指定则自动检测",
				},
				"model": map[string]any{
					"type":        "string",
					"description": "使用的转录模型，如 whisper-1，默认 whisper-1",
				},
				"response_format": map[string]any{
					"type":        "string",
					"description": "响应格式: text, json, verbose_json, srt, vtt，默认 text",
				},
				"temperature": map[string]any{
					"type":        "number",
					"description": "采样温度 (0~1)，0 表示确定性最高",
				},
			},
			"required": []string{"audio_path"},
		},
	}
}

// Execute 执行音频转录。
// 读取音频文件 → 上传到 Whisper API → 返回转录文本。
func (t *TranscriptionTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	audioPath, ok := args["audio_path"].(string)
	if !ok || audioPath == "" {
		return ToolError("参数 audio_path 是必填项且必须为字符串"), nil
	}

	language, _ := args["language"].(string)

	model := "whisper-1"
	if m, ok := args["model"].(string); ok && m != "" {
		model = m
	}

	responseFormat := "text"
	if f, ok := args["response_format"].(string); ok && f != "" {
		responseFormat = f
	}

	temperature := 0.0
	if temp, ok := args["temperature"].(float64); ok {
		temperature = temp
	}

	// 安全敏感路径检查
	if isPathSensitive(audioPath) {
		slog.Warn("audio transcription blocked (sensitive path)", "path", audioPath)
		return ToolError(fmt.Sprintf("安全限制: 不允许访问敏感路径 %s", audioPath)), nil
	}

	// 检查文件是否存在和大小
	info, err := os.Stat(audioPath)
	if err != nil {
		slog.Warn("audio file not found", "path", audioPath, "err", err)
		return ToolError(fmt.Sprintf("无法访问音频文件: %v", err)), nil
	}
	// Whisper API 限制 25MB
	if info.Size() > 25*1024*1024 {
		return ToolError(fmt.Sprintf("音频文件过大 (最大 25MB): %s", formatAudioSize(info.Size()))), nil
	}

	// 调用 Whisper API 转录
	transcript, err := t.transcribe(ctx, model, audioPath, language, responseFormat, temperature)
	if err != nil {
		slog.Error("audio transcription failed", "path", audioPath, "err", err)
		return ToolError(fmt.Sprintf("音频转录失败: %v", err)), nil
	}

	slog.Info("audio transcription succeeded", "path", audioPath, "model", model, "chars", len(transcript))
	return ToolResult(map[string]any{
		"output":  transcript,
		"audio":   audioPath,
		"model":   model,
		"chars":   len(transcript),
		"language": language,
	}), nil
}

// transcribe 调用 Whisper API 转录音频。
func (t *TranscriptionTool) transcribe(ctx context.Context, model, audioPath, language, responseFormat string, temperature float64) (string, error) {
	if t.client == nil {
		t.client = &http.Client{Timeout: 300 * time.Second} // Whisper 转录可能需要较长时间
	}

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

	// 读取音频文件
	audioData, err := os.ReadFile(audioPath)
	if err != nil {
		return "", fmt.Errorf("读取音频文件失败: %w", err)
	}

	// 构建 multipart 表单
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// file 字段
	part, err := writer.CreateFormFile("file", filepath.Base(audioPath))
	if err != nil {
		return "", fmt.Errorf("创建表单文件失败: %w", err)
	}
	if _, err := part.Write(audioData); err != nil {
		return "", fmt.Errorf("写入表单数据失败: %w", err)
	}

	// model 字段
	_ = writer.WriteField("model", model)

	// response_format 字段
	_ = writer.WriteField("response_format", responseFormat)

	// language 字段 (可选)
	if language != "" {
		_ = writer.WriteField("language", language)
	}

	// temperature 字段
	_ = writer.WriteField("temperature", fmt.Sprintf("%.2f", temperature))

	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("关闭表单写入失败: %w", err)
	}

	// 发送请求
	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/v1/audio/transcriptions", &buf)
	if err != nil {
		return "", fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("API 错误 (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	// 解析响应 (text 格式直接返回文本，json 格式解析)
	if responseFormat == "text" || responseFormat == "srt" || responseFormat == "vtt" {
		return string(respBody), nil
	}

	// json 或 verbose_json 格式
	var apiResp struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		// 如果解析失败，返回原始响应
		return string(respBody), nil
	}
	return apiResp.Text, nil
}

// formatAudioSize 格式化音频文件大小。
func formatAudioSize(bytes int64) string {
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
	GetRegistry().Register(&TranscriptionTool{})
}
