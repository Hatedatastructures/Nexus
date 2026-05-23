// Package tool 提供技能系统管理工具。
package tool

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// ───────────────────────────── 技能列表工具 ─────────────────────────────

// SkillsListTool 列出所有可用技能。
type SkillsListTool struct{}

// Name 返回工具名称。
func (t *SkillsListTool) Name() string { return "skills_list" }

// Description 返回工具描述。
func (t *SkillsListTool) Description() string {
	return "列出所有已安装的可用技能及其描述。"
}

// Toolset 返回工具所属工具集。
func (t *SkillsListTool) Toolset() string { return "skills" }

// Emoji 返回工具图标。
func (t *SkillsListTool) Emoji() string { return "📚" }

// IsAvailable 检查是否可用。
func (t *SkillsListTool) IsAvailable() bool { return true }

// MaxResultChars 返回结果最大字符数。
func (t *SkillsListTool) MaxResultChars() int { return 20000 }

// Schema 返回工具的 JSON Schema。
func (t *SkillsListTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "skills_list",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{},
		},
	}
}

// Execute 列出所有技能。
func (t *SkillsListTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	skills := getSkillList()
	if len(skills) == 0 {
		return ToolResult(map[string]any{"output": "当前没有安装任何技能"}), nil
	}

	var buf strings.Builder
	buf.WriteString("已安装的技能:\n\n")
	for _, s := range skills {
		buf.WriteString(fmt.Sprintf("- **%s**: %s\n", s["name"], s["description"]))
	}

	return ToolResult(map[string]any{"output": buf.String()}), nil
}

// ───────────────────────────── 技能查看工具 ─────────────────────────────

// SkillViewTool 查看指定技能的详细内容。
type SkillViewTool struct{}

// Name 返回工具名称。
func (t *SkillViewTool) Name() string { return "skill_view" }

// Description 返回工具描述。
func (t *SkillViewTool) Description() string {
	return "查看指定技能的完整描述、使用方法和参数。"
}

// Toolset 返回工具所属工具集。
func (t *SkillViewTool) Toolset() string { return "skills" }

// Emoji 返回工具图标。
func (t *SkillViewTool) Emoji() string { return "📖" }

// IsAvailable 检查是否可用。
func (t *SkillViewTool) IsAvailable() bool { return true }

// MaxResultChars 返回结果最大字符数。
func (t *SkillViewTool) MaxResultChars() int { return 10000 }

// Schema 返回工具的 JSON Schema。
func (t *SkillViewTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "skill_view",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"skill": map[string]any{
					"type":        "string",
					"description": "要查看的技能名称",
				},
			},
			"required": []string{"skill"},
		},
	}
}

// Execute 查看技能详情。
func (t *SkillViewTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	skillName, ok := args["skill"].(string)
	if !ok || skillName == "" {
		return ToolError("参数 skill 是必填项"), nil
	}

	skill := getSkillDetail(skillName)
	if skill == "" {
		return ToolError(fmt.Sprintf("技能 %q 未找到", skillName)), nil
	}

	return ToolResult(map[string]any{"output": skill}), nil
}

// ───────────────────────────── 技能辅助函数 ─────────────────────────────

// getSkillList 获取技能列表摘要。
// 扫描 ~/.nexus/skills/ 下所有 SKILL.md 文件，提取 name 和 description。
func getSkillList() []map[string]string {
	home, err := os.UserHomeDir()
	if err != nil {
		slog.Debug("skill: failed to get home directory", "err", err)
		return nil
	}

	skillsDir := filepath.Join(home, ".nexus", "skills")
	var result []map[string]string

	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillPath := filepath.Join(skillsDir, entry.Name(), "SKILL.md")
		data, err := os.ReadFile(skillPath)
		if err != nil {
			continue
		}

		name, desc := parseSkillFrontmatter(data)
		if name == "" {
			name = entry.Name()
		}
		if desc == "" {
			desc = "(无描述)"
		}

		result = append(result, map[string]string{
			"name":        name,
			"description": desc,
		})
	}

	return result
}

// getSkillDetail 获取指定技能的详细内容。
// 查找 ~/.nexus/skills/<name>/SKILL.md 并返回完整内容。
func getSkillDetail(name string) string {
	// 防止路径遍历
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return ""
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	skillPath := filepath.Join(home, ".nexus", "skills", name, "SKILL.md")
	data, err := os.ReadFile(skillPath)
	if err == nil {
		return string(data)
	}

	// 遍历子目录查找（支持 category/name 结构）
	skillsDir := filepath.Join(home, ".nexus", "skills")
	var found []byte
	filepath.WalkDir(skillsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && d.Name() == name {
			candidate := filepath.Join(path, "SKILL.md")
			if content, e := os.ReadFile(candidate); e == nil {
				found = content
				return filepath.SkipAll
			}
		}
		return nil
	})

	if found != nil {
		return string(found)
	}
	return ""
}

// parseSkillFrontmatter 从 SKILL.md 内容中提取 name 和 description。
func parseSkillFrontmatter(content []byte) (name, description string) {
	text := string(content)
	if !strings.HasPrefix(text, "---") {
		return "", ""
	}
	closeIdx := strings.Index(text[3:], "\n---\n")
	if closeIdx == -1 {
		return "", ""
	}
	yamlContent := text[3 : closeIdx+3]

	for _, line := range strings.Split(yamlContent, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "name:") {
			name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
			name = strings.Trim(name, "\"'")
		} else if strings.HasPrefix(line, "description:") {
			description = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
			description = strings.Trim(description, "\"'")
		}
	}
	return name, description
}

// ───────────────────────────── init 注册 ─────────────────────────────

func init() {
	slog.Debug("registering skill management tool")
	GetRegistry().Register(&SkillsListTool{})
	GetRegistry().Register(&SkillViewTool{})
}
