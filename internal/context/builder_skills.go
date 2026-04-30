// Package context 提供技能索引注入功能。
// buildSkillsPrompt 构建活动技能列表，注入到系统提示词中。
package context

import (
	"fmt"
	"strings"
)

// buildSkillsPrompt 构建所有活动技能的索引文本。
//
// 技能从 skillManager 获取，格式化为模型可理解的列表。
// 每个技能包含名称、描述和版本信息。
//
// 返回格式:
//
//	## 可用技能
//
//	- `skill-name` (v1.0.0) — 技能描述
func (b *Builder) buildSkillsPrompt() string {
	if b.skillManager == nil {
		return ""
	}

	skills := b.skillManager.GetActiveSkills()
	if len(skills) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n\n## 可用技能\n\n")
	sb.WriteString("以下技能已加载并可在对话中使用:\n")

	for _, s := range skills {
		version := s.Version
		if version == "" {
			version = "未知版本"
		}
		desc := s.Description
		if desc == "" {
			desc = "无描述"
		}
		// 截断过长的描述
		if len(desc) > 200 {
			desc = desc[:200] + "..."
		}
		sb.WriteString(fmt.Sprintf("- `%s` (v%s) — %s\n", s.Name, version, desc))
	}

	// 追加技能使用指导
	sb.WriteString("\n通过 `skill_manage` 工具可管理技能。技能指令可通过 `/skill_name` 格式调用。\n")

	return sb.String()
}
