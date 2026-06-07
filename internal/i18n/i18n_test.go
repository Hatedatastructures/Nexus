package i18n

import (
	"testing"
)

// ───────────────────────────── 辅助函数 ─────────────────────────────

// newTestTranslator 创建用于测试的翻译器，手动注入 locale 数据。
func newTestTranslator(locale string) Translator {
	t := &translator{
		locale:  locale,
		locales: make(map[string]map[string]string),
	}

	// 注入英文翻译
	t.locales["en"] = map[string]string{
		"agent.welcome":    "Welcome to Nexus Agent",
		"agent.goodbye":    "Goodbye!",
		"error.rate_limit": "Rate limit reached, retrying in %d seconds",
		"error.network":    "Network error: %s",
	}

	// 注入中文翻译
	t.locales["zh"] = map[string]string{
		"agent.welcome":    "欢迎使用 Nexus Agent",
		"agent.goodbye":    "再见！",
		"error.rate_limit": "已达到速率限制，%d 秒后重试",
		"error.network":    "网络错误：%s",
	}

	return t
}

// ───────────────────────────── TestTranslatorT ─────────────────────────────

func TestTranslatorT(t *testing.T) {
	tests := []struct {
		name   string
		locale string
		key    string
		args   []any
		want   string
	}{
		{
			name:   "英文翻译",
			locale: "en",
			key:    "agent.welcome",
			want:   "Welcome to Nexus Agent",
		},
		{
			name:   "中文翻译",
			locale: "zh",
			key:    "agent.welcome",
			want:   "欢迎使用 Nexus Agent",
		},
		{
			name:   "英文 goodbye",
			locale: "en",
			key:    "agent.goodbye",
			want:   "Goodbye!",
		},
		{
			name:   "中文 goodbye",
			locale: "zh",
			key:    "agent.goodbye",
			want:   "再见！",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := newTestTranslator(tt.locale)
			got := tr.T(tt.key, tt.args...)
			if got != tt.want {
				t.Errorf("T(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

// ───────────────────────────── TestTranslatorFallback ─────────────────────────────

func TestTranslatorFallback(t *testing.T) {
	tests := []struct {
		name   string
		locale string
		key    string
		want   string
	}{
		{
			name:   "zh-CN 回退到 zh",
			locale: "zh-CN",
			key:    "agent.welcome",
			want:   "欢迎使用 Nexus Agent",
		},
		{
			name:   "en-US 回退到 en",
			locale: "en-US",
			key:    "agent.welcome",
			want:   "Welcome to Nexus Agent",
		},
		{
			name:   "zh-TW 回退到 zh",
			locale: "zh-TW",
			key:    "agent.goodbye",
			want:   "再见！",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := newTestTranslator(tt.locale)
			got := tr.T(tt.key)
			if got != tt.want {
				t.Errorf("T(%q) with locale %q = %q, want %q", tt.key, tt.locale, got, tt.want)
			}
		})
	}

	// 测试 en 回退: zh locale 请求 zh 中不存在但 en 中存在的 key
	t.Run("zh 回退到 en", func(t *testing.T) {
		// 创建只有 en 的翻译器
		tr := &translator{
			locale: "zh",
			locales: map[string]map[string]string{
				"en": {"only.en.key": "English only"},
			},
		}
		got := tr.T("only.en.key")
		if got != "English only" {
			t.Errorf("T(%q) = %q, want %q", "only.en.key", got, "English only")
		}
	})
}

// ───────────────────────────── TestTranslatorMissingKey ─────────────────────────────

func TestTranslatorMissingKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want string
	}{
		{
			name: "未知键返回 raw key",
			key:  "unknown.key.path",
			want: "unknown.key.path",
		},
		{
			name: "空键返回空字符串",
			key:  "",
			want: "",
		},
		{
			name: "深层未知键",
			key:  "very.deep.nested.unknown.key",
			want: "very.deep.nested.unknown.key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := newTestTranslator("en")
			got := tr.T(tt.key)
			if got != tt.want {
				t.Errorf("T(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

// ───────────────────────────── TestTranslatorFormatArgs ─────────────────────────────

func TestTranslatorFormatArgs(t *testing.T) {
	tests := []struct {
		name   string
		locale string
		key    string
		args   []any
		want   string
	}{
		{
			name:   "英文 %d 参数替换",
			locale: "en",
			key:    "error.rate_limit",
			args:   []any{30},
			want:   "Rate limit reached, retrying in 30 seconds",
		},
		{
			name:   "英文 %s 参数替换",
			locale: "en",
			key:    "error.network",
			args:   []any{"connection refused"},
			want:   "Network error: connection refused",
		},
		{
			name:   "中文 %d 参数替换",
			locale: "zh",
			key:    "error.rate_limit",
			args:   []any{60},
			want:   "已达到速率限制，60 秒后重试",
		},
		{
			name:   "中文 %s 参数替换",
			locale: "zh",
			key:    "error.network",
			args:   []any{"连接被拒绝"},
			want:   "网络错误：连接被拒绝",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := newTestTranslator(tt.locale)
			got := tr.T(tt.key, tt.args...)
			if got != tt.want {
				t.Errorf("T(%q, %v) = %q, want %q", tt.key, tt.args, got, tt.want)
			}
		})
	}
}

// ───────────────────────────── TestTranslatorLocale ─────────────────────────────

func TestTranslatorLocale(t *testing.T) {
	tr := newTestTranslator("zh")
	if tr.Locale() != "zh" {
		t.Errorf("Locale() = %q, want %q", tr.Locale(), "zh")
	}

	if err := tr.SetLocale("en"); err != nil {
		t.Fatalf("SetLocale() error: %v", err)
	}

	if tr.Locale() != "en" {
		t.Errorf("切换后 Locale() = %q, want %q", tr.Locale(), "en")
	}

	// 切换后应返回英文翻译
	got := tr.T("agent.welcome")
	if got != "Welcome to Nexus Agent" {
		t.Errorf("切换到 en 后 T() = %q, want %q", got, "Welcome to Nexus Agent")
	}
}
