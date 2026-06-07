package llm

import (
	"testing"
)

// ───────────────────────────── TestGetModel ─────────────────────────────

func TestGetModel(t *testing.T) {
	client := NewModelsDevClient()

	tests := []struct {
		name     string
		modelID  string
		wantNil  bool
		wantProv string
	}{
		{
			name:     "获取已知模型 claude-sonnet-4-20250514",
			modelID:  "claude-sonnet-4-20250514",
			wantNil:  false,
			wantProv: "anthropic",
		},
		{
			name:     "获取已知模型 gpt-4o",
			modelID:  "gpt-4o",
			wantNil:  false,
			wantProv: "openai",
		},
		{
			name:     "获取已知模型 gemini-2.5-pro",
			modelID:  "gemini-2.5-pro",
			wantNil:  false,
			wantProv: "google",
		},
		{
			name:     "获取已知模型 deepseek-chat",
			modelID:  "deepseek-chat",
			wantNil:  false,
			wantProv: "deepseek",
		},
		{
			name:     "获取已知模型 mistral-large-latest",
			modelID:  "mistral-large-latest",
			wantNil:  false,
			wantProv: "mistral",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := client.GetModel(tt.modelID)
			if tt.wantNil {
				if info != nil {
					t.Errorf("GetModel(%q) = %v, want nil", tt.modelID, info)
				}
				return
			}

			if info == nil {
				t.Fatalf("GetModel(%q) = nil, want non-nil", tt.modelID)
			}

			if info.Provider != tt.wantProv {
				t.Errorf("Provider = %q, want %q", info.Provider, tt.wantProv)
			}
			if info.ID != tt.modelID {
				t.Errorf("ID = %q, want %q", info.ID, tt.modelID)
			}
		})
	}
}

// ───────────────────────────── TestListModels ─────────────────────────────

func TestListModels(t *testing.T) {
	client := NewModelsDevClient()

	tests := []struct {
		name     string
		provider string
		minCount int
	}{
		{
			name:     "列出所有模型",
			provider: "",
			minCount: 10,
		},
		{
			name:     "按 provider 过滤 anthropic",
			provider: "anthropic",
			minCount: 3,
		},
		{
			name:     "按 provider 过滤 openai",
			provider: "openai",
			minCount: 3,
		},
		{
			name:     "按 provider 过滤 google",
			provider: "google",
			minCount: 2,
		},
		{
			name:     "按 provider 过滤 deepseek",
			provider: "deepseek",
			minCount: 1,
		},
		{
			name:     "不存在的 provider 返回空列表",
			provider: "nonexistent",
			minCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			models := client.ListModels(tt.provider)

			if len(models) < tt.minCount {
				t.Errorf("ListModels(%q) 返回 %d 个模型, 至少需要 %d",
					tt.provider, len(models), tt.minCount)
			}

			// 验证过滤结果中所有模型都属于指定 provider
			for _, m := range models {
				if tt.provider != "" && m.Provider != tt.provider {
					t.Errorf("模型 %s 的 provider = %q, want %q", m.ID, m.Provider, tt.provider)
				}
			}
		})
	}
}

// ───────────────────────────── TestIsVisionModel ─────────────────────────────

func TestIsVisionModel(t *testing.T) {
	client := NewModelsDevClient()

	tests := []struct {
		name    string
		modelID string
		want    bool
	}{
		{name: "claude-sonnet-4 支持 vision", modelID: "claude-sonnet-4-20250514", want: true},
		{name: "claude-opus-4 支持 vision", modelID: "claude-opus-4-20250514", want: true},
		{name: "gpt-4o 支持 vision", modelID: "gpt-4o", want: true},
		{name: "gemini-2.5-pro 支持 vision", modelID: "gemini-2.5-pro", want: true},
		{name: "deepseek-chat 不支持 vision", modelID: "deepseek-chat", want: false},
		{name: "o1-mini 不支持 vision", modelID: "o1-mini", want: false},
		{name: "mistral-large 不支持 vision", modelID: "mistral-large-latest", want: false},
		{name: "不存在的模型不支持 vision", modelID: "nonexistent-model", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := client.IsVisionModel(tt.modelID)
			if got != tt.want {
				t.Errorf("IsVisionModel(%q) = %v, want %v", tt.modelID, got, tt.want)
			}
		})
	}
}

// ───────────────────────────── TestGetModelUnknown ─────────────────────────────

func TestGetModelUnknown(t *testing.T) {
	client := NewModelsDevClient()

	tests := []struct {
		name    string
		modelID string
	}{
		{name: "完全未知的模型", modelID: "totally-unknown-model"},
		{name: "空字符串", modelID: ""},
		{name: "类似已知但不完全匹配", modelID: "claude-sonnet-4"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := client.GetModel(tt.modelID)
			if info != nil {
				t.Errorf("GetModel(%q) = %v, want nil", tt.modelID, info)
			}
		})
	}
}

// ───────────────────────────── TestModelDevInfoFields ─────────────────────────────

func TestModelDevInfoFields(t *testing.T) {
	client := NewModelsDevClient()

	info := client.GetModel("claude-sonnet-4-20250514")
	if info == nil {
		t.Fatal("GetModel() = nil")
	}

	// 验证内置模型的关键字段
	if info.ContextWindow != 200000 {
		t.Errorf("ContextWindow = %d, want 200000", info.ContextWindow)
	}
	if !info.Vision {
		t.Error("Vision 应为 true")
	}
	if !info.Reasoning {
		t.Error("Reasoning 应为 true")
	}
}

// ───────────────────────────── ListModels 返回副本 ─────────────────────────────

func TestListModels_ReturnsCopies(t *testing.T) {
	client := NewModelsDevClient()
	models := client.ListModels("anthropic")
	if len(models) == 0 {
		t.Fatal("expected at least one anthropic model")
	}
	original := client.GetModel("claude-sonnet-4-20250514")
	models[0].ID = "tampered"
	if original.ID == "tampered" {
		t.Error("ListModels should return copies, not references to cache")
	}
}

