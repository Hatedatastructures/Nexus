// Package skill 提供技能加载器，负责 SKILL.md 文件的解析与发现。
package skill

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

// ───────────────────────────── 排除目录 ─────────────────────────────

// excludedDirs 在遍历技能目录时排除的目录名
var excludedDirs = map[string]struct{}{
	".git":    {},
	".github": {},
	".hub":    {},
}

// ───────────────────────────── 平台映射 ─────────────────────────────

// platformMap 将技能中声明的人类可读平台名映射到 Go 的 runtime.GOOS 值
var platformMap = map[string]string{
	"macos":   "darwin",
	"linux":   "linux",
	"windows": "windows",
}

// ───────────────────────────── 前端元数据解析 ─────────────────────────────

// ParseSkillMarkdown 解析 SKILL.md 内容，提取 YAML frontmatter 和正文。
//
// SKILL.md 格式:
//
//	---
//	name: my-skill
//	description: 我的技能描述
//	version: 1.0.0
//	license: MIT
//	platforms: [macos, linux]
//	---
//
//	# 技能正文 (Markdown)
//
// 必填字段: name, description
// 可选字段: version, license, platforms, 以及其他自定义字段
func ParseSkillMarkdown(content []byte) (*Skill, error) {
	text := string(content)

	// 检查是否存在 frontmatter
	if !strings.HasPrefix(text, "---") {
		return nil, fmt.Errorf("SKILL.md 缺少 YAML frontmatter (必须以 '---' 开头)")
	}

	// 查找结束分隔符
	closeIdx := strings.Index(text[3:], "\n---\n")
	if closeIdx == -1 {
		return nil, fmt.Errorf("SKILL.md 缺少 YAML frontmatter 结束分隔符 '---'")
	}

	yamlContent := text[3 : closeIdx+3]
	body := text[closeIdx+7:] // 跳过 \n---\n

	// 解析 YAML
	var frontmatter map[string]any
	if err := yaml.Unmarshal([]byte(yamlContent), &frontmatter); err != nil {
		return nil, fmt.Errorf("解析 YAML frontmatter 失败: %w", err)
	}

	// 提取必填字段
	name, _ := frontmatter["name"].(string)
	if name == "" {
		return nil, fmt.Errorf("SKILL.md 缺少必填字段: name")
	}
	if len(name) > 64 {
		return nil, fmt.Errorf("技能名称过长 (最多 64 字符): %s", name)
	}

	description, _ := frontmatter["description"].(string)
	if description == "" {
		return nil, fmt.Errorf("SKILL.md 缺少必填字段: description")
	}
	if len(description) > 1024 {
		description = description[:1024]
	}

	// 提取可选字段
	version, _ := frontmatter["version"].(string)
	license_, _ := frontmatter["license"].(string)

	// 提取平台列表
	var platforms []string
	if raw, ok := frontmatter["platforms"]; ok {
		switch v := raw.(type) {
		case []any:
			for _, p := range v {
				platforms = append(platforms, fmt.Sprint(p))
			}
		case string:
			platforms = []string{v}
		}
	}

	// 移除 frontmatter 中已提取的字段，剩下的放入 Fields
	delete(frontmatter, "name")
	delete(frontmatter, "description")
	delete(frontmatter, "version")
	delete(frontmatter, "license")
	delete(frontmatter, "platforms")

	return &Skill{
		Name:        name,
		Description: description,
		Version:     version,
		License:     license_,
		Platforms:   platforms,
		Body:        strings.TrimSpace(body),
		Fields:      frontmatter,
	}, nil
}

// ───────────────────────────── 平台过滤 ─────────────────────────────

// skillMatchesPlatform 判断技能是否与当前平台兼容。
// 如果 platform 列表为空，则兼容所有平台。
func skillMatchesPlatform(skill *Skill) bool {
	if len(skill.Platforms) == 0 {
		return true
	}

	current := runtime.GOOS
	for _, p := range skill.Platforms {
		normalized := strings.ToLower(strings.TrimSpace(p))
		if mapped, ok := platformMap[normalized]; ok {
			normalized = mapped
		}
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

// ───────────────────────────── 技能加载器 ─────────────────────────────

// SkillLoader 负责从多个目录发现和加载技能。
//
// 扫描策略:
//   - 遍历 skillsDir 和 externalDirs
//   - 查找 */SKILL.md 文件
//   - 按平台过滤
//   - 去重 (本地目录优先于外部目录)
type SkillLoader struct {
	skillsDir    string   // 技能根目录 (~/.nexus/skills)
	externalDirs []string // 外部技能目录列表
}

// NewSkillLoader 创建技能加载器。
// skillsDir 是本地技能目录，externalDirs 是额外的外部搜索目录。
func NewSkillLoader(skillsDir string, externalDirs []string) *SkillLoader {
	return &SkillLoader{
		skillsDir:    skillsDir,
		externalDirs: externalDirs,
	}
}

// DiscoverAll 扫描所有技能目录，返回发现的所有有效技能。
// 按平台过滤，同名技能以本地目录为准 (本地优先于外部)。
func (l *SkillLoader) DiscoverAll() ([]*Skill, error) {
	// 收集所有搜索目录 (本地优先)
	var dirs []string
	if l.skillsDir != "" {
		dirs = append(dirs, l.skillsDir)
	}
	dirs = append(dirs, l.externalDirs...)

	seen := make(map[string]*Skill) // name -> skill
	var ordered []string             // 保持发现顺序

	for _, dir := range dirs {
		if dir == "" {
			continue
		}

		skills, err := l.discoverInDir(dir)
		if err != nil {
			slog.Warn("技能: 扫描目录失败",
				"dir", dir,
				"error", err,
			)
			continue
		}

		for _, skill := range skills {
			// 按平台过滤
			if !skillMatchesPlatform(skill) {
				slog.Debug("技能: 跳过不兼容平台的技能",
					"name", skill.Name,
					"platforms", skill.Platforms,
				)
				continue
			}

			// 去重 (先发现的优先 — 本地 dirs 先被扫描)
			if _, exists := seen[skill.Name]; !exists {
				seen[skill.Name] = skill
				ordered = append(ordered, skill.Name)
			}
		}
	}

	result := make([]*Skill, 0, len(ordered))
	for _, name := range ordered {
		result = append(result, seen[name])
	}

	slog.Info("技能: 发现完毕", "count", len(result))
	return result, nil
}

// Load 按名称加载单个技能。
// 在 skillsDir 和 externalDirs 中搜索名为 name 的 SKILL.md。
func (l *SkillLoader) Load(name string) (*Skill, error) {
	dirs := []string{}
	if l.skillsDir != "" {
		dirs = append(dirs, l.skillsDir)
	}
	dirs = append(dirs, l.externalDirs...)

	for _, dir := range dirs {
		if dir == "" {
			continue
		}

		skillPath := filepath.Join(dir, name, "SKILL.md")
		data, err := os.ReadFile(skillPath)
		if err != nil {
			if !os.IsNotExist(err) {
				slog.Debug("技能: 读取文件失败", "path", skillPath, "error", err)
			}
			continue
		}

		skill, err := ParseSkillMarkdown(data)
		if err != nil {
			return nil, fmt.Errorf("解析技能 '%s' 失败: %w", name, err)
		}

		skill.Path = skillPath

		// 提取分类目录 (技能目录的父目录名)
		parentDir := filepath.Base(filepath.Dir(skillPath))
		if parentDir != name {
			skill.Category = parentDir
		}

		if !skillMatchesPlatform(skill) {
			return nil, fmt.Errorf("技能 '%s' 与当前平台不兼容", name)
		}

		return skill, nil
	}

	return nil, fmt.Errorf("技能 '%s' 未找到", name)
}

// ───────────────────────────── 内部方法 ─────────────────────────────

// discoverInDir 在单个目录中递归扫描 SKILL.md 文件。
func (l *SkillLoader) discoverInDir(dir string) ([]*Skill, error) {
	var skills []*Skill

	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // 跳过无法访问的路径
		}

		// 跳过排除的目录
		if d.IsDir() {
			if _, excluded := excludedDirs[filepath.Base(path)]; excluded {
				return filepath.SkipDir
			}
			return nil
		}

		// 只处理 SKILL.md 文件
		if filepath.Base(path) != "SKILL.md" {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			slog.Debug("技能: 读取文件失败", "path", path, "error", err)
			return nil
		}

		skill, err := ParseSkillMarkdown(data)
		if err != nil {
			slog.Debug("技能: 解析失败", "path", path, "error", err)
			return nil
		}

		skill.Path = path

		// 提取分类目录
		parentDir := filepath.Base(filepath.Dir(path))
		if parentDir != skill.Name {
			skill.Category = parentDir
		}

		skills = append(skills, skill)
		return nil
	})

	return skills, err
}
