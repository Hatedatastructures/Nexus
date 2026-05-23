// Package tool 提供视频生成和语音录制/播放工具。
// video_generate 通过外部 API 根据文本生成视频；
// voice_record / voice_play 通过 ffmpeg 录音和播放音频。
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
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// VideoGenerateTool 通过外部 API 根据文本描述生成视频。
type VideoGenerateTool struct {
	client *http.Client
}

func (t *VideoGenerateTool) Name() string { return "video_generate" }

func (t *VideoGenerateTool) Description() string {
	return "根据文本描述生成视频。调用视频生成 API (Replicate/Runway 等)，将文本提示词转换为短视频。"
}

func (t *VideoGenerateTool) Toolset() string { return "media" }

func (t *VideoGenerateTool) Emoji() string { return "movie" }

func (t *VideoGenerateTool) IsAvailable() bool {
	return os.Getenv("VIDEO_GEN_API_KEY") != ""
}

func (t *VideoGenerateTool) MaxResultChars() int { return 10000 }

func (t *VideoGenerateTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "video_generate",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prompt": map[string]any{
					"type":        "string",
					"description": "视频内容描述，越详细越好",
				},
				"duration": map[string]any{
					"type":        "integer",
					"description": "视频时长 (秒)，默认 5",
				},
				"resolution": map[string]any{
					"type":        "string",
					"description": "分辨率: 720p, 1080p, 4K，默认 1080p",
				},
				"style": map[string]any{
					"type":        "string",
					"description": "视频风格，如 cinematic, anime, realistic 等 (可选)",
				},
			},
			"required": []string{"prompt"},
		},
	}
}

func (t *VideoGenerateTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	prompt, ok := args["prompt"].(string)
	if !ok || prompt == "" {
		return ToolError("参数 prompt 是必填项且必须为字符串"), nil
	}

	duration := 5
	if d, ok := args["duration"].(float64); ok && d > 0 {
		duration = int(d)
	}

	resolution := "1080p"
	if r, ok := args["resolution"].(string); ok && r != "" {
		resolution = r
	}

	style, _ := args["style"].(string)

	result, err := t.generate(ctx, prompt, duration, resolution, style)
	if err != nil {
		slog.Error("video generation failed", "prompt", prompt[:min(50, len(prompt))], "err", err)
		return ToolError(fmt.Sprintf("视频生成失败: %v", err)), nil
	}

	slog.Info("video generation succeeded", "prompt", prompt[:min(50, len(prompt))])
	return ToolResult(result), nil
}

// generate 调用视频生成 API。
func (t *VideoGenerateTool) generate(ctx context.Context, prompt string, duration int, resolution, style string) (map[string]any, error) {
	if t.client == nil {
		t.client = &http.Client{Timeout: 300 * time.Second}
	}

	apiKey := os.Getenv("VIDEO_GEN_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("未配置 VIDEO_GEN_API_KEY")
	}

	baseURL := os.Getenv("VIDEO_GEN_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.replicate.com/v1"
	}
	baseURL = strings.TrimRight(baseURL, "/")

	// 构建请求体 (Replicate predictions 风格)
	reqBody := map[string]any{
		"input": map[string]any{
			"prompt":     prompt,
			"duration":   duration,
			"resolution": resolution,
		},
	}
	if style != "" {
		reqBody["input"].(map[string]any)["style"] = style
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/predictions", bytes.NewReader(bodyBytes))
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

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("API 错误 (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var apiResp map[string]any
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	result := map[string]any{
		"output":     "视频生成请求已提交",
		"prompt":     prompt,
		"duration":   duration,
		"resolution": resolution,
	}
	if id, ok := apiResp["id"].(string); ok {
		result["prediction_id"] = id
		result["output"] = fmt.Sprintf("视频生成已提交 (ID: %s)", id)
	}
	if urls, ok := apiResp["urls"].(map[string]any); ok {
		if getURL, ok := urls["get"].(string); ok {
			result["status_url"] = getURL
		}
	}
	if output, ok := apiResp["output"]; ok {
		result["raw_output"] = output
	}

	return result, nil
}

// VoiceRecordTool 使用 ffmpeg 从默认麦克风录制音频。
type VoiceRecordTool struct{}

func (t *VoiceRecordTool) Name() string { return "voice_record" }

func (t *VoiceRecordTool) Description() string {
	return "从麦克风录制音频。使用 ffmpeg 捕获麦克风输入并保存为临时 WAV 文件。"
}

func (t *VoiceRecordTool) Toolset() string { return "media" }

func (t *VoiceRecordTool) Emoji() string { return "microphone" }

func (t *VoiceRecordTool) IsAvailable() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

func (t *VoiceRecordTool) MaxResultChars() int { return 5000 }

func (t *VoiceRecordTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "voice_record",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"duration": map[string]any{
					"type":        "integer",
					"description": "录音时长 (秒)，默认 5",
				},
			},
		},
	}
}

func (t *VoiceRecordTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	duration := 5
	if d, ok := args["duration"].(float64); ok && d > 0 {
		duration = int(d)
	}

	// 创建临时输出文件
	tmpDir := os.TempDir()
	outPath := filepath.Join(tmpDir, fmt.Sprintf("recording_%d.wav", time.Now().UnixMilli()))

	// 构建 ffmpeg 命令
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "ffmpeg", "-y",
			"-f", "dshow", "-i", "audio=麦克风",
			"-t", fmt.Sprintf("%d", duration),
			"-acodec", "pcm_s16le", "-ar", "44100", "-ac", "1",
			outPath,
		)
	} else if runtime.GOOS == "darwin" {
		cmd = exec.CommandContext(ctx, "ffmpeg", "-y",
			"-f", "avfoundation", "-i", ":0",
			"-t", fmt.Sprintf("%d", duration),
			"-acodec", "pcm_s16le", "-ar", "44100", "-ac", "1",
			outPath,
		)
	} else {
		cmd = exec.CommandContext(ctx, "ffmpeg", "-y",
			"-f", "alsa", "-i", "default",
			"-t", fmt.Sprintf("%d", duration),
			"-acodec", "pcm_s16le", "-ar", "44100", "-ac", "1",
			outPath,
		)
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return ToolError(fmt.Sprintf("录音失败: %v (%s)", err, stderr.String())), nil
	}

	// 检查文件
	info, err := os.Stat(outPath)
	if err != nil {
		return ToolError(fmt.Sprintf("录音文件不存在: %v", err)), nil
	}

	slog.Info("recording completed", "path", outPath, "duration", duration, "size", info.Size())
	return ToolResult(map[string]any{
		"output":    fmt.Sprintf("录音完成: %s (%s)", outPath, formatFileSize(int(info.Size()))),
		"path":      outPath,
		"duration":  duration,
		"size_bytes": info.Size(),
	}), nil
}

// VoicePlayTool 使用系统播放器播放音频文件。
type VoicePlayTool struct{}

func (t *VoicePlayTool) Name() string { return "voice_play" }

func (t *VoicePlayTool) Description() string {
	return "播放音频文件。使用 ffmpeg 或系统播放器播放指定路径的音频。"
}

func (t *VoicePlayTool) Toolset() string { return "media" }

func (t *VoicePlayTool) Emoji() string { return "speaker" }

func (t *VoicePlayTool) IsAvailable() bool {
	_, err := exec.LookPath("ffplay")
	if err != nil {
		_, err = exec.LookPath("ffmpeg")
	}
	return err == nil
}

func (t *VoicePlayTool) MaxResultChars() int { return 5000 }

func (t *VoicePlayTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "voice_play",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{
					"type":        "string",
					"description": "要播放的音频文件路径",
				},
			},
			"required": []string{"file_path"},
		},
	}
}

func (t *VoicePlayTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	filePath, ok := args["file_path"].(string)
	if !ok || filePath == "" {
		return ToolError("参数 file_path 是必填项且必须为字符串"), nil
	}

	// 安全敏感路径检查
	if isPathSensitive(filePath) {
		slog.Warn("audio playback blocked (sensitive path)", "path", filePath)
		return ToolError(fmt.Sprintf("安全限制: 不允许访问敏感路径 %s", filePath)), nil
	}

	if _, err := os.Stat(filePath); err != nil {
		return ToolError(fmt.Sprintf("文件不存在: %v", err)), nil
	}

	// 使用 ffplay 播放 (静默模式，播放完自动退出)
	cmd := exec.CommandContext(ctx, "ffplay",
		"-nodisp", "-autoexit", "-loglevel", "quiet", filePath)

	if err := cmd.Run(); err != nil {
		return ToolError(fmt.Sprintf("播放失败: %v", err)), nil
	}

	slog.Info("audio playback completed", "path", filePath)
	return ToolResult(map[string]any{
		"output":   fmt.Sprintf("播放完成: %s", filePath),
		"file_path": filePath,
	}), nil
}
// ───────────────────────────── init 注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&VideoGenerateTool{})
	GetRegistry().Register(&VoiceRecordTool{})
	GetRegistry().Register(&VoicePlayTool{})
}
