// Package skill 提供技能索引构建与缓存功能。
// 技能索引是注入系统提示词的文本列表，帮助模型了解可用的技能。
package skill

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ───────────────────────────── 索引快照 ─────────────────────────────

// skillsIndexSnapshot 索引快照的结构 (用于磁盘缓存)
type skillsIndexSnapshot struct {
	Skills    []skillSummary `json:"skills"`
	Platform  string         `json:"platform"`
	UpdatedAt string         `json:"updated_at"`
}

// skillSummary 单个技能的摘要信息
type skillSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ───────────────────────────── 构建索引 ─────────────────────────────

// BuildSkillsIndex 构建用于系统提示词的技能列表文本。
//
// 格式: 每行一个技能，格式为 "- 技能名: 描述"
// 结果将被缓存到 .skills_prompt_snapshot.json 以加速后续启动。
//
// 参数:
//   - skills: 技能列表
//   - tools: 当前可用的工具名称列表
//   - platform: 当前平台标识
func BuildSkillsIndex(skills []*Skill, tools []string, platform string) string {
	if len(skills) == 0 {
		return ""
	}

	var lines []string
	lines = append(lines, "## 可用技能")
	lines = append(lines, "")

	for _, skill := range skills {
		desc := skill.Description
		if len(desc) > 100 {
			desc = desc[:97] + "..."
		}
		lines = append(lines, fmt.Sprintf("- **%s**: %s", skill.Name, desc))
	}

	return strings.Join(lines, "\n")
}

// BuildSkillsIndexWithCache 构建技能索引并尝试缓存到磁盘。
// cacheDir 通常为 ~/.nexus。
func BuildSkillsIndexWithCache(skills []*Skill, tools []string, platform string, cacheDir string) string {
	text := BuildSkillsIndex(skills, tools, platform)

	// 构建缓存快照
	summaries := make([]skillSummary, 0, len(skills))
	for _, s := range skills {
		summaries = append(summaries, skillSummary{
			Name:        s.Name,
			Description: s.Description,
		})
	}

	snapshot := skillsIndexSnapshot{
		Skills:    summaries,
		Platform:  platform,
		UpdatedAt: time.Now().Format(time.RFC3339),
	}

	if cacheDir != "" {
		if err := os.MkdirAll(cacheDir, 0700); err != nil {
			slog.Debug("skill: failed to create cache directory", "dir", cacheDir, "error", err)
		} else {
			cachePath := filepath.Join(cacheDir, ".skills_prompt_snapshot.json")
			data, err := json.MarshalIndent(snapshot, "", "  ")
			if err != nil {
				slog.Debug("skill: failed to serialize cache", "error", err)
			} else if err := os.WriteFile(cachePath, data, 0644); err != nil {
				slog.Debug("skill: failed to write cache", "path", cachePath, "error", err)
			}
		}
	}

	return text
}

// LoadSkillsIndexFromCache 从磁盘缓存加载技能索引。
// 返回缓存的文本，或空字符串表示无缓存。
func LoadSkillsIndexFromCache(cacheDir string) string {
	cachePath := filepath.Join(cacheDir, ".skills_prompt_snapshot.json")
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return ""
	}

	var snapshot skillsIndexSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		slog.Debug("skill: failed to parse cache", "path", cachePath, "error", err)
		return ""
	}

	if len(snapshot.Skills) == 0 {
		return ""
	}

	// 重建文本
	var lines []string
	lines = append(lines, "## 可用技能")
	lines = append(lines, "")

	for _, s := range snapshot.Skills {
		desc := s.Description
		if len(desc) > 100 {
			desc = desc[:97] + "..."
		}
		lines = append(lines, fmt.Sprintf("- **%s**: %s", s.Name, desc))
	}

	return strings.Join(lines, "\n")
}
