package i18n

import (
	"embed"
	"fmt"
	"path/filepath"
	"strings"
)

//go:embed locales/*.yaml
var localeFS embed.FS

// LoadAllEmbeddedLocales 加载所有内嵌的 locale 文件。
// 返回 locale 名称到文件内容的映射（如 "en" -> []byte, "zh" -> []byte）。
func LoadAllEmbeddedLocales() (map[string][]byte, error) {
	entries, err := localeFS.ReadDir("locales")
	if err != nil {
		return nil, fmt.Errorf("读取内嵌 locale 目录失败: %w", err)
	}

	result := make(map[string][]byte, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		// 仅处理 .yaml 文件
		if filepath.Ext(name) != ".yaml" {
			continue
		}
		// 从文件名提取 locale 标识符（如 en.yaml → en）
		localeName := strings.TrimSuffix(name, ".yaml")

		data, err := localeFS.ReadFile("locales/" + name)
		if err != nil {
			return nil, fmt.Errorf("读取 locale 文件 %s 失败: %w", name, err)
		}
		result[localeName] = data
	}

	return result, nil
}

// LoadEmbeddedLocale 加载指定名称的内嵌 locale 文件。
func LoadEmbeddedLocale(name string) (map[string]string, error) {
	data, err := localeFS.ReadFile("locales/" + name + ".yaml")
	if err != nil {
		return nil, fmt.Errorf("读取 locale %s 失败: %w", name, err)
	}
	return LoadLocale(data)
}
