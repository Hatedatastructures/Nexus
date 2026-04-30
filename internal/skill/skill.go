// Package skill 提供可复用技能的管理。
// 技能是包含 YAML frontmatter 和 Markdown 指令的声明式文件。
// 支持创建/安装/更新/删除操作，以及 agentskills.io 中心集成。
package skill

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// ───────────────────────────── 技能数据模型 ─────────────────────────────

// Skill 表示一个技能的完整信息
type Skill struct {
	Name        string         // 技能名称 (max 64 chars)
	Description string         // 技能描述 (max 1024 chars)
	Version     string         // 语义版本
	License     string         // SPDX 标识
	Platforms   []string       // 兼容平台 (空 = 全平台)
	Body        string         // SKILL.md 正文 Markdown
	Category    string         // 分类目录
	Path        string         // 磁盘路径
	Fields      map[string]any // 其他 frontmatter 字段
}

// ───────────────────────────── 技能管理器 ─────────────────────────────

// Manager 管理技能的全生命周期。
// 支持加载、查询、创建、更新、删除、安装和启用/禁用操作。
type Manager struct {
	mu         sync.RWMutex
	skillsDir  string             // 技能根目录 (~/.nexus/skills)
	skills     map[string]*Skill  // 已加载的技能
	disabled   []string           // 禁用的技能名称列表
	loader     *SkillLoader       // 技能加载器
	hub        *SkillsHub         // 技能中心客户端
}

// NewManager 创建技能管理器
func NewManager(skillsDir string, disabled []string) *Manager {
	return &Manager{
		skillsDir: skillsDir,
		skills:    make(map[string]*Skill),
		disabled:  disabled,
		loader:    NewSkillLoader(skillsDir, nil),
		hub:       NewSkillsHub(skillsDir),
	}
}

// GetActiveSkills 返回当前激活的技能列表
func (m *Manager) GetActiveSkills() []*Skill {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var active []*Skill
	for name, skill := range m.skills {
		if !m.isDisabled(name) {
			active = append(active, skill)
		}
	}
	return active
}

// GetActiveSkillsIndex 返回活动技能的索引文本 (用于系统提示词)
func (m *Manager) GetActiveSkillsIndex() string {
	active := m.GetActiveSkills()
	if len(active) == 0 {
		return ""
	}
	return BuildSkillsIndexWithCache(active, nil, "", filepath.Dir(m.skillsDir))
}

// isDisabled 检查技能是否被禁用
func (m *Manager) isDisabled(name string) bool {
	for _, d := range m.disabled {
		if d == name {
			return true
		}
	}
	return false
}

// ───────────────────────────── 加载与查询 ─────────────────────────────

// LoadAll 扫描技能目录并加载所有有效技能。
func (m *Manager) LoadAll(ctx context.Context) error {
	skills, err := m.loader.DiscoverAll()
	if err != nil {
		return fmt.Errorf("加载技能失败: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.skills = make(map[string]*Skill, len(skills))
	for _, skill := range skills {
		m.skills[skill.Name] = skill
	}

	slog.Info("技能: 加载完成", "count", len(m.skills))
	return nil
}

// Get 根据名称获取已加载的技能。
// 返回 nil 表示未找到。
func (m *Manager) Get(name string) (*Skill, error) {
	m.mu.RLock()
	skill, ok := m.skills[name]
	m.mu.RUnlock()

	if ok {
		return skill, nil
	}

	// 缓存中未找到，尝试用加载器加载
	loaded, err := m.loader.Load(name)
	if err != nil {
		return nil, fmt.Errorf("技能 '%s' 未找到", name)
	}

	// 加载成功后缓存到内存
	m.mu.Lock()
	m.skills[name] = loaded
	m.mu.Unlock()

	return loaded, nil
}

// ───────────────────────────── 创建与写入 ─────────────────────────────

// Create 创建新技能并写入磁盘。
// skill.Path 和 skill.Category 会被自动设置。
func (m *Manager) Create(skill *Skill) error {
	if skill == nil {
		return fmt.Errorf("技能不能为 nil")
	}
	if skill.Name == "" {
		return fmt.Errorf("技能名称为空")
	}
	if skill.Description == "" {
		return fmt.Errorf("技能描述为空")
	}

	skillDir := filepath.Join(m.skillsDir, skill.Name)
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return fmt.Errorf("创建技能目录失败: %w", err)
	}

	// 构建 SKILL.md 内容
	skill.Path = filepath.Join(skillDir, "SKILL.md")
	if err := m.writeSkillToDisk(skill); err != nil {
		return fmt.Errorf("写入 SKILL.md 失败: %w", err)
	}

	m.mu.Lock()
	m.skills[skill.Name] = skill
	m.mu.Unlock()

	slog.Info("技能: 创建完成", "name", skill.Name, "path", skill.Path)
	return nil
}

// Update 更新指定技能的元数据和正文。
// 需要提供完整的 skill 对象。
func (m *Manager) Update(name string, skill *Skill) error {
	if skill == nil {
		return fmt.Errorf("技能不能为 nil")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.skills[name]
	if !ok {
		return fmt.Errorf("技能 '%s' 不存在", name)
	}

	// 保留原有路径
	skill.Path = existing.Path
	if skill.Path == "" {
		skill.Path = filepath.Join(m.skillsDir, name, "SKILL.md")
	}

	if err := m.writeSkillToDisk(skill); err != nil {
		return fmt.Errorf("写入 SKILL.md 失败: %w", err)
	}

	m.skills[name] = skill
	slog.Info("技能: 更新完成", "name", name)
	return nil
}

// Delete 删除指定技能 (从内存和磁盘移除)。
func (m *Manager) Delete(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	skill, ok := m.skills[name]
	if !ok {
		return fmt.Errorf("技能 '%s' 不存在", name)
	}

	// 删除磁盘文件
	skillDir := filepath.Dir(skill.Path)
	if err := os.RemoveAll(skillDir); err != nil {
		slog.Warn("技能: 删除目录失败", "dir", skillDir, "error", err)
	}

	delete(m.skills, name)

	// 从禁用列表中移除
	var newDisabled []string
	for _, d := range m.disabled {
		if d != name {
			newDisabled = append(newDisabled, d)
		}
	}
	m.disabled = newDisabled

	slog.Info("技能: 删除完成", "name", name)
	return nil
}

// ───────────────────────────── 安装 ─────────────────────────────

// Install 从技能中心安装技能到本地。
// source 可以是 "github" 或 "url"。
// identifier 是安装标识符 (如 github:owner/repo 或 https://...)。
func (m *Manager) Install(ctx context.Context, source, identifier string) error {
	skill, err := m.hub.Install(ctx, identifier)
	if err != nil {
		return fmt.Errorf("安装技能失败: %w", err)
	}

	m.mu.Lock()
	m.skills[skill.Name] = skill
	m.mu.Unlock()

	slog.Info("技能: 安装完成", "name", skill.Name, "source", source)
	return nil
}

// ───────────────────────────── 启用/禁用 ─────────────────────────────

// Disable 禁用指定名称的技能 (在后续回合中不加载)。
func (m *Manager) Disable(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 避免重复
	for _, d := range m.disabled {
		if d == name {
			return
		}
	}
	m.disabled = append(m.disabled, name)
	slog.Info("技能: 已禁用", "name", name)
}

// Enable 重新启用之前被禁用的技能。
func (m *Manager) Enable(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var newDisabled []string
	for _, d := range m.disabled {
		if d != name {
			newDisabled = append(newDisabled, d)
		}
	}
	m.disabled = newDisabled
	slog.Info("技能: 已启用", "name", name)
}

// ───────────────────────────── 内部方法 ─────────────────────────────

// writeSkillToDisk 将技能序列化为 SKILL.md 格式并写入磁盘。
func (m *Manager) writeSkillToDisk(skill *Skill) error {
	// 构建 frontmatter
	fm := make(map[string]any)
	fm["name"] = skill.Name
	fm["description"] = skill.Description
	if skill.Version != "" {
		fm["version"] = skill.Version
	}
	if skill.License != "" {
		fm["license"] = skill.License
	}
	if len(skill.Platforms) > 0 {
		fm["platforms"] = skill.Platforms
	}
	// 合并额外字段
	for k, v := range skill.Fields {
		if _, reserved := fm[k]; !reserved {
			fm[k] = v
		}
	}

	// 序列化为 YAML
	fmBytes, err := yaml.Marshal(fm)
	if err != nil {
		return fmt.Errorf("序列化 frontmatter 失败: %w", err)
	}

	// 组装完整的 SKILL.md
	content := "---\n"
	content += string(fmBytes)
	content += "---\n\n"
	if skill.Body != "" {
		content += skill.Body
		content += "\n"
	}

	// 确保目录存在
	dir := filepath.Dir(skill.Path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	// 原子写入 (临时文件 + 重命名)
	tmpFile, err := os.CreateTemp(dir, ".skill_*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.WriteString(content); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("写入临时文件失败: %w", err)
	}
	tmpFile.Close()

	if err := os.Rename(tmpPath, skill.Path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("原子重命名失败: %w", err)
	}

	return nil
}

