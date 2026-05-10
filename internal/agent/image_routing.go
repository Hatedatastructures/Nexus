// Package agent 提供 AI 代理的核心实现。
// ImageRouter 根据消息中是否包含图像内容, 自动选择支持视觉的模型。
package agent

import (
	"encoding/json"
	"strings"

	"nexus-agent/internal/llm"
)

// ───────────────────────────── 默认视觉模型 ─────────────────────────────

// defaultVisionModels 是内置的支持视觉的模型集合。
var defaultVisionModels = map[string]bool{
	"claude-sonnet-4-20250514": true,
	"gpt-4o":                   true,
}

// defaultFallbackModel 是当前模型不支持视觉时的默认回退模型。
const defaultFallbackModel = "claude-sonnet-4-20250514"

// ───────────────────────────── ImageRouter ─────────────────────────────

// ImageRouter 根据消息内容选择合适的模型。
// 当消息中包含图像但当前模型不支持视觉时, 自动切换到 fallbackModel。
type ImageRouter struct {
	visionModels  map[string]bool // 支持 vision 的模型集合
	fallbackModel string          // vision 回退模型
}

// NewImageRouter 创建 ImageRouter 实例。
// visionModels 为额外支持视觉的模型列表 (会与默认视觉模型合并);
// fallbackModel 为空时使用默认回退模型 (claude-sonnet-4-20250514)。
func NewImageRouter(visionModels []string, fallbackModel string) *ImageRouter {
	merged := make(map[string]bool, len(defaultVisionModels)+len(visionModels))
	for k, v := range defaultVisionModels {
		merged[k] = v
	}
	for _, m := range visionModels {
		merged[m] = true
	}

	if fallbackModel == "" {
		fallbackModel = defaultFallbackModel
	}

	return &ImageRouter{
		visionModels:  merged,
		fallbackModel: fallbackModel,
	}
}

// RouteModel 检查消息中是否有图像, 返回应使用的模型。
// 规则:
//   - 消息中无图像 → 返回 currentModel (无需切换)
//   - 消息中有图像且当前模型支持 vision → 返回 currentModel
//   - 消息中有图像但当前模型不支持 vision → 返回 fallbackModel
func (r *ImageRouter) RouteModel(messages []llm.Message, currentModel string) string {
	if !HasImageContent(messages) {
		return currentModel
	}

	// 当前模型支持视觉, 无需切换
	if r.visionModels[currentModel] {
		return currentModel
	}

	// 切换到回退模型
	return r.fallbackModel
}

// HasVisionSupport 检查给定模型是否支持视觉。
func (r *ImageRouter) HasVisionSupport(model string) bool {
	return r.visionModels[model]
}

// FallbackModel 返回视觉回退模型名称。
func (r *ImageRouter) FallbackModel() string {
	return r.fallbackModel
}

// ───────────────────────────── 图像检测 ─────────────────────────────

// HasImageContent 检测消息列表中是否包含图像内容。
// 支持以下格式:
//   - base64 数据 URI: data:image/...;base64,...
//   - 图像 URL: http(s)://... 结尾为图片扩展名 (jpg/png/gif/webp/svg/bmp)
//   - OpenAI 多模态内容块: JSON 数组中包含 type:"image_url" 的对象
//   - Anthropic 图像块: JSON 数组中包含 type:"image" 的对象
func HasImageContent(messages []llm.Message) bool {
	for i := range messages {
		if messageHasImage(&messages[i]) {
			return true
		}
	}
	return false
}

// messageHasImage 检测单条消息是否包含图像。
func messageHasImage(msg *llm.Message) bool {
	content := msg.Content
	if content == "" {
		return false
	}

	// 快速检查: 常见图像标记子串 (避免不必要的 JSON 解析)
	lower := strings.ToLower(content)

	// 检查 base64 数据 URI
	if strings.Contains(lower, "data:image/") {
		return true
	}

	// 检查多模态内容块标记 (OpenAI image_url / Anthropic image)
	if strings.Contains(lower, `"image_url"`) || strings.Contains(lower, `"type":"image"`) {
		// 尝试解析为 JSON 数组以确认结构
		if isMultiModalContent(content) {
			return true
		}
	}

	// 检查裸图像 URL (以图片扩展名结尾)
	if containsImageURL(lower) {
		return true
	}

	return false
}

// ───────────────────────────── 多模态内容块解析 ─────────────────────────────

// contentPart 表示 OpenAI / Anthropic 多模态消息中的一个内容块。
// 仅用于图像检测, 仅提取 type 和必要字段。
type contentPart struct {
	Type     string `json:"type"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url,omitempty"`
	Source *struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
	} `json:"source,omitempty"`
}

// isMultiModalContent 尝试将内容解析为 JSON 数组, 检查是否包含图像块。
func isMultiModalContent(content string) bool {
	// 只处理以 [ 开头的 JSON 数组
	trimmed := strings.TrimSpace(content)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return false
	}

	var parts []contentPart
	if err := json.Unmarshal([]byte(content), &parts); err != nil {
		return false
	}

	for _, part := range parts {
		switch part.Type {
		case "image_url":
			// OpenAI 格式: {"type": "image_url", "image_url": {"url": "..."}}
			if part.ImageURL != nil && part.ImageURL.URL != "" {
				return true
			}
		case "image":
			// Anthropic 格式: {"type": "image", "source": {"type": "base64", ...}}
			if part.Source != nil {
				return true
			}
		}
	}

	return false
}

// ───────────────────────────── 图像 URL 检测 ─────────────────────────────

// imageExtensions 是常见的图像文件扩展名 (小写, 含前导点号)。
var imageExtensions = []string{
	".jpg", ".jpeg", ".png", ".gif", ".webp", ".svg", ".bmp", ".tiff", ".tif", ".ico",
}

// containsImageURL 检查文本中是否包含以图像扩展名结尾的 HTTP(S) URL。
func containsImageURL(lowerContent string) bool {
	// 查找 http:// 或 https:// 开头的 URL 片段
	idx := 0
	for {
		pos := strings.Index(lowerContent[idx:], "http")
		if pos < 0 {
			break
		}
		idx += pos

		// 确保是 http:// 或 https://
		rest := lowerContent[idx:]
		if !strings.HasPrefix(rest, "http://") && !strings.HasPrefix(rest, "https://") {
			idx += 4
			continue
		}

		// 提取 URL (到空格/引号/换行为止)
		urlEnd := len(rest)
		for _, sep := range []byte{' ', '\n', '\r', '\t', '"', '\'', ')', '>', '<'} {
			if p := strings.IndexByte(rest, sep); p >= 0 && p < urlEnd {
				urlEnd = p
			}
		}
		url := rest[:urlEnd]

		// 检查是否以图片扩展名结尾 (忽略查询参数)
		cleanURL := url
		if q := strings.IndexByte(cleanURL, '?'); q >= 0 {
			cleanURL = cleanURL[:q]
		}
		for _, ext := range imageExtensions {
			if strings.HasSuffix(cleanURL, ext) {
				return true
			}
		}

		idx += 4
	}

	return false
}
