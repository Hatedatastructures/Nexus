package agent

import (
	"testing"

	"nexus-agent/internal/llm"
)

// ───────────────────────────── TestHasImageContent ─────────────────────────────

func TestHasImageContent(t *testing.T) {
	tests := []struct {
		name     string
		messages []llm.Message
		want     bool
	}{
		{
			name: "纯文本消息不包含图像",
			messages: []llm.Message{
				{Role: llm.RoleUser, Content: "hello world"},
			},
			want: false,
		},
		{
			name: "包含 base64 数据 URI",
			messages: []llm.Message{
				{Role: llm.RoleUser, Content: "data:image/png;base64,iVBORw0KGgo="},
			},
			want: true,
		},
		{
			name: "包含 OpenAI image_url 多模态块",
			messages: []llm.Message{
				{Role: llm.RoleUser, Content: `[{"type":"text","text":"describe this"},{"type":"image_url","image_url":{"url":"https://example.com/cat.jpg"}}]`},
			},
			want: true,
		},
		{
			name: "包含 Anthropic image 多模态块",
			messages: []llm.Message{
				{Role: llm.RoleUser, Content: `[{"type":"image","source":{"type":"base64","media_type":"image/png"}},{"type":"text","text":"what is this"}]`},
			},
			want: true,
		},
		{
			name: "包含裸图像 URL (.jpg)",
			messages: []llm.Message{
				{Role: llm.RoleUser, Content: "Check this image: https://example.com/photo.jpg"},
			},
			want: true,
		},
		{
			name: "包含裸图像 URL (.png)",
			messages: []llm.Message{
				{Role: llm.RoleUser, Content: "https://example.com/image.png"},
			},
			want: true,
		},
		{
			name: "包含带查询参数的图像 URL",
			messages: []llm.Message{
				{Role: llm.RoleUser, Content: "https://example.com/image.jpg?width=100"},
			},
			want: true,
		},
		{
			name:     "空消息列表",
			messages: []llm.Message{},
			want:     false,
		},
		{
			name: "多条消息中只有第二条包含图像",
			messages: []llm.Message{
				{Role: llm.RoleUser, Content: "hello"},
				{Role: llm.RoleUser, Content: "data:image/jpeg;base64,/9j/4AAQ"},
			},
			want: true,
		},
		{
			name: "空 Content 消息",
			messages: []llm.Message{
				{Role: llm.RoleAssistant, Content: ""},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HasImageContent(tt.messages)
			if got != tt.want {
				t.Errorf("HasImageContent() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ───────────────────────────── TestRouteModel ─────────────────────────────

func TestRouteModel(t *testing.T) {
	router := NewImageRouter(nil, "")

	tests := []struct {
		name         string
		messages     []llm.Message
		currentModel string
		wantModel    string
	}{
		{
			name:         "无图像消息 → 返回当前模型",
			messages:     []llm.Message{{Role: llm.RoleUser, Content: "hello"}},
			currentModel: "deepseek-chat",
			wantModel:    "deepseek-chat",
		},
		{
			name:         "有图像且当前模型支持 vision → 返回当前模型",
			messages:     []llm.Message{{Role: llm.RoleUser, Content: "data:image/png;base64,abc"}},
			currentModel: "claude-sonnet-4-20250514",
			wantModel:    "claude-sonnet-4-20250514",
		},
		{
			name:         "有图像且当前模型支持 vision (gpt-4o) → 返回当前模型",
			messages:     []llm.Message{{Role: llm.RoleUser, Content: "data:image/png;base64,abc"}},
			currentModel: "gpt-4o",
			wantModel:    "gpt-4o",
		},
		{
			name:         "有图像但当前模型不支持 vision → 切换到 fallback",
			messages:     []llm.Message{{Role: llm.RoleUser, Content: "data:image/png;base64,abc"}},
			currentModel: "deepseek-chat",
			wantModel:    defaultFallbackModel,
		},
		{
			name:         "有图像但 o1-mini 不支持 vision → 切换到 fallback",
			messages:     []llm.Message{{Role: llm.RoleUser, Content: "data:image/jpeg;base64,abc"}},
			currentModel: "o1-mini",
			wantModel:    defaultFallbackModel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := router.RouteModel(tt.messages, tt.currentModel)
			if got != tt.wantModel {
				t.Errorf("RouteModel() = %q, want %q", got, tt.wantModel)
			}
		})
	}

	// 测试自定义 fallback 模型
	t.Run("自定义 fallback 模型", func(t *testing.T) {
		customRouter := NewImageRouter(nil, "custom-fallback-model")
		msgs := []llm.Message{{Role: llm.RoleUser, Content: "data:image/png;base64,abc"}}
		got := customRouter.RouteModel(msgs, "non-vision-model")
		if got != "custom-fallback-model" {
			t.Errorf("RouteModel() = %q, want %q", got, "custom-fallback-model")
		}
	})

	// 测试自定义 vision 模型列表
	t.Run("自定义 vision 模型列表", func(t *testing.T) {
		customRouter := NewImageRouter([]string{"my-custom-vision-model"}, "")
		msgs := []llm.Message{{Role: llm.RoleUser, Content: "data:image/png;base64,abc"}}
		got := customRouter.RouteModel(msgs, "my-custom-vision-model")
		if got != "my-custom-vision-model" {
			t.Errorf("RouteModel() = %q, want %q", got, "my-custom-vision-model")
		}
	})
}

// ───────────────────────────── TestHasVisionSupport ─────────────────────────────

func TestHasVisionSupport(t *testing.T) {
	router := NewImageRouter([]string{"custom-model"}, "")

	tests := []struct {
		name  string
		model string
		want  bool
	}{
		{name: "claude-sonnet-4 支持", model: "claude-sonnet-4-20250514", want: true},
		{name: "gpt-4o 支持", model: "gpt-4o", want: true},
		{name: "自定义模型支持", model: "custom-model", want: true},
		{name: "deepseek-chat 不支持", model: "deepseek-chat", want: false},
		{name: "未知模型不支持", model: "unknown-model", want: false},
		{name: "空字符串不支持", model: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := router.HasVisionSupport(tt.model)
			if got != tt.want {
				t.Errorf("HasVisionSupport(%q) = %v, want %v", tt.model, got, tt.want)
			}
		})
	}
}

// ───────────────────────────── TestFallbackModel ─────────────────────────────

func TestFallbackModel(t *testing.T) {
	t.Run("默认 fallback 模型", func(t *testing.T) {
		router := NewImageRouter(nil, "")
		if router.FallbackModel() != defaultFallbackModel {
			t.Errorf("FallbackModel() = %q, want %q", router.FallbackModel(), defaultFallbackModel)
		}
	})

	t.Run("自定义 fallback 模型", func(t *testing.T) {
		router := NewImageRouter(nil, "my-fallback")
		if router.FallbackModel() != "my-fallback" {
			t.Errorf("FallbackModel() = %q, want %q", router.FallbackModel(), "my-fallback")
		}
	})
}
