// Package i18n 提供 Nexus Agent 的国际化支持。
//
// 支持 YAML 格式的 locale 文件加载，点分键查找，以及带 fmt.Sprintf 参数的翻译。
// 回退链: zh-CN → zh → en-US → raw key。
package i18n

import (
	"fmt"
	"strings"
	"sync"
)

// Translator 翻译器接口，所有国际化操作通过此接口完成。
type Translator interface {
	// T 根据 key 查找翻译文本，并使用 args 进行格式化。
	// 若 key 不存在则返回 key 本身。
	T(key string, args ...any) string

	// Locale 返回当前 locale 标识符（如 "zh-CN"）。
	Locale() string

	// SetLocale 切换当前 locale。若 locale 未加载则返回错误。
	SetLocale(locale string) error
}

// translator 是 Translator 的默认实现。
type translator struct {
	mu      sync.RWMutex
	locale  string                       // 当前 locale 标识符
	locales map[string]map[string]string // locale -> flat key-value 映射
}

// 默认全局实例，供便捷函数使用。
var (
	defaultOnce sync.Once
	defaultInst Translator
)

// Init 使用指定 locale 初始化全局翻译器。
// 若传入空字符串则默认使用 "en"。该函数可安全地多次调用（仅首次生效）。
func Init(locale string) {
	defaultOnce.Do(func() {
		if locale == "" {
			locale = "en"
		}
		t := &translator{
			locale:  locale,
			locales: make(map[string]map[string]string),
		}
		// 加载所有内嵌 locale 文件
		locales, err := LoadAllEmbeddedLocales()
		if err == nil {
			for name, data := range locales {
				m, err := LoadLocale(data)
				if err == nil {
					t.locales[name] = m
				}
			}
		}
		defaultInst = t
	})
}

// New 创建一个新的翻译器实例（不使用全局状态）。
func New(locale string) (Translator, error) {
	if locale == "" {
		locale = "en"
	}
	t := &translator{
		locale:  locale,
		locales: make(map[string]map[string]string),
	}
	locales, err := LoadAllEmbeddedLocales()
	if err != nil {
		return nil, fmt.Errorf("加载内嵌 locale 失败: %w", err)
	}
	for name, data := range locales {
		m, err := LoadLocale(data)
		if err == nil {
			t.locales[name] = m
		}
	}
	if _, ok := t.locales[locale]; !ok {
		// 若请求的 locale 不在内嵌文件中，仍然允许创建，只是翻译会回退
	}
	return t, nil
}

// T 是全局便捷翻译函数。必须先调用 Init 初始化。
func T(key string, args ...any) string {
	if defaultInst == nil {
		// 未初始化时直接返回格式化后的 key
		if len(args) > 0 {
			return fmt.Sprintf(key, args...)
		}
		return key
	}
	return defaultInst.T(key, args...)
}

// T 实现 Translator 接口。
func (t *translator) T(key string, args ...any) string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// 回退链: 精确 locale → 语言前缀 → en → raw key
	result, found := t.lookup(t.locale, key)
	if !found {
		// 尝试语言前缀（如 zh-CN → zh）
		lang := t.languageOf(t.locale)
		if lang != t.locale {
			result, found = t.lookup(lang, key)
		}
	}
	if !found {
		// 尝试 en 回退
		if t.locale != "en" {
			result, found = t.lookup("en", key)
		}
	}
	if !found {
		// 最终回退：返回原始 key
		result = key
	}

	if len(args) > 0 {
		return fmt.Sprintf(result, args...)
	}
	return result
}

// Locale 返回当前 locale。
func (t *translator) Locale() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.locale
}

// SetLocale 切换当前 locale。
func (t *translator) SetLocale(locale string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	// 允许切换到任意 locale；若未加载翻译数据，翻译时会回退
	t.locale = locale
	return nil
}

// lookup 在指定 locale 的翻译表中查找 key。
func (t *translator) lookup(locale, key string) (string, bool) {
	m, ok := t.locales[locale]
	if !ok {
		return "", false
	}
	val, ok := m[key]
	return val, ok
}

// languageOf 提取 locale 的语言部分（如 "zh-CN" → "zh"）。
func (t *translator) languageOf(locale string) string {
	if i := strings.Index(locale, "-"); i >= 0 {
		return locale[:i]
	}
	return locale
}

// RegisterLocale 手动注册一个 locale 的翻译数据（用于测试或外部注入）。
func RegisterLocale(name string, data map[string]string) {
	if defaultInst == nil {
		return
	}
	if t, ok := defaultInst.(*translator); ok {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.locales[name] = data
	}
}
