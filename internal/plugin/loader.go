// Package plugin 提供插件发现和加载功能。
// Loader 负责从多个目录扫描 plugin.yaml 文件，解析清单并验证依赖。
package plugin

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ───────────────────────────── 排除目录 ─────────────────────────────

// excludedPluginDirs 在遍历插件目录时排除的目录名
var excludedPluginDirs = map[string]struct{}{
	".git":    {},
	".github": {},
	".cache":  {},
}

// ───────────────────────────── 插件加载器 ─────────────────────────────

// Loader 负责从多个目录发现和加载插件清单。
//
// 扫描策略:
//   - 遍历 pluginDirs 列表
//   - 在每个目录中查找 */plugin.yaml 文件
//   - 解析清单并验证依赖
//   - 按平台过滤
//   - 去重 (先发现的优先)
type Loader struct {
	pluginDirs []string // 插件目录列表
}

// NewLoader 创建插件加载器。
// pluginDirs 为插件目录列表，按优先级排序 (本地目录优先)。
func NewLoader(pluginDirs []string) *Loader {
	return &Loader{
		pluginDirs: pluginDirs,
	}
}

// ───────────────────────────── 发现 ─────────────────────────────

// Discover 扫描所有插件目录，返回发现的所有有效插件清单。
// 按平台过滤，同名插件以先发现的为准 (目录列表中靠前的优先)。
func (l *Loader) Discover() ([]*Manifest, error) {
	seen := make(map[string]*Manifest) // name → manifest
	var ordered []string               // 保持发现顺序

	for _, dir := range l.pluginDirs {
		if dir == "" {
			continue
		}

		manifests, err := l.discoverInDir(dir)
		if err != nil {
			slog.Warn("插件: 扫描目录失败",
				"dir", dir,
				"error", err,
			)
			continue
		}

		for _, m := range manifests {
			// 按平台过滤
			if !manifestMatchesPlatform(m) {
				slog.Debug("插件: 跳过不兼容平台的插件",
					"name", m.Name,
					"platforms", m.Platforms,
				)
				continue
			}

			// 去重 (先发现的优先)
			if _, exists := seen[m.Name]; !exists {
				seen[m.Name] = m
				ordered = append(ordered, m.Name)
			} else {
				slog.Debug("插件: 跳过重复插件",
					"name", m.Name,
					"dir", dir,
				)
			}
		}
	}

	result := make([]*Manifest, 0, len(ordered))
	for _, name := range ordered {
		result = append(result, seen[name])
	}

	slog.Info("插件: 发现完毕", "count", len(result))
	return result, nil
}

// ───────────────────────────── 验证 ─────────────────────────────

// Validate 验证插件清单的依赖完整性。
//
// 检查项:
//   - 必需的环境变量是否存在
//   - 外部依赖的二进制是否在 PATH 中
//   - 清单字段格式是否正确
func (l *Loader) Validate(m *Manifest) error {
	if m == nil {
		return fmt.Errorf("清单为 nil")
	}

	// 基本字段验证
	if err := ValidateManifest(m); err != nil {
		return fmt.Errorf("清单验证失败: %w", err)
	}

	// 检查必需的环境变量
	for _, envName := range m.RequiresEnv {
		if os.Getenv(envName) == "" {
			slog.Warn("插件: 缺少必需的环境变量",
				"plugin", m.Name,
				"env", envName,
			)
			return fmt.Errorf("插件 %s 缺少必需的环境变量: %s", m.Name, envName)
		}
	}

	// 检查外部依赖
	for _, dep := range m.ExternalDeps {
		if !isInPATH(dep) {
			slog.Warn("插件: 外部依赖不可用",
				"plugin", m.Name,
				"dep", dep,
			)
			return fmt.Errorf("插件 %s 的外部依赖不可用: %s", m.Name, dep)
		}
	}

	return nil
}

// ───────────────────────────── 内部方法 ─────────────────────────────

// discoverInDir 在单个目录中递归扫描 plugin.yaml 文件。
func (l *Loader) discoverInDir(dir string) ([]*Manifest, error) {
	var manifests []*Manifest

	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // 跳过无法访问的路径
		}

		// 跳过排除的目录
		if d.IsDir() {
			if _, excluded := excludedPluginDirs[filepath.Base(path)]; excluded {
				return filepath.SkipDir
			}
			return nil
		}

		// 只处理 plugin.yaml 文件
		base := filepath.Base(path)
		if base != "plugin.yaml" && base != "plugin.yml" {
			return nil
		}

		m, err := ParseManifest(path)
		if err != nil {
			slog.Debug("插件: 解析清单失败",
				"path", path,
				"error", err,
			)
			return nil // 继续扫描其他插件
		}

		manifests = append(manifests, m)
		return nil
	})

	return manifests, err
}

// manifestMatchesPlatform 判断插件清单是否与当前平台兼容。
// 如果 platforms 列表为空，则兼容所有平台。
func manifestMatchesPlatform(m *Manifest) bool {
	if len(m.Platforms) == 0 {
		return true
	}

	current := runtime.GOOS
	for _, p := range m.Platforms {
		normalized := strings.ToLower(strings.TrimSpace(p))
		if current == normalized {
			return true
		}
		// darwin 兼容 macos
		if current == "darwin" && normalized == "macos" {
			return true
		}
	}

	return false
}

// isInPATH 检查指定的可执行文件是否在 PATH 中。
func isInPATH(name string) bool {
	// 如果是完整路径，直接检查文件是否存在
	if filepath.IsAbs(name) {
		_, err := os.Stat(name)
		return err == nil
	}

	// 在 PATH 中搜索
	pathEnv := os.Getenv("PATH")
	if pathEnv == "" {
		return false
	}

	separator := ":"
	if runtime.GOOS == "windows" {
		separator = ";"
		name = name + ".exe"
	}

	for _, dir := range strings.Split(pathEnv, separator) {
		if dir == "" {
			continue
		}
		fullPath := filepath.Join(dir, name)
		if _, err := os.Stat(fullPath); err == nil {
			return true
		}
	}

	return false
}
