// Package tool 提供技能管理工具（供 Agent 调用）。
package tool

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SkillManageTool 技能管理工具。
type SkillManageTool struct{}

func (t *SkillManageTool) Name() string { return "skill_manage" }
func (t *SkillManageTool) Description() string {
	return "管理技能。支持安装、卸载、更新、搜索技能。"
}
func (t *SkillManageTool) Toolset() string     { return "skills" }
func (t *SkillManageTool) IsAvailable() bool   { return true }
func (t *SkillManageTool) Emoji() string       { return "📦" }
func (t *SkillManageTool) MaxResultChars() int { return 10000 }

func (t *SkillManageTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "操作类型",
					"enum":        []string{"install", "uninstall", "list", "search"},
				},
				"identifier": map[string]any{
					"type":        "string",
					"description": "技能标识符 (install 操作时必填)。可以是 git URL 或技能名称",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "技能名称 (uninstall 操作时必填)",
				},
				"query": map[string]any{
					"type":        "string",
					"description": "搜索关键词 (search 操作时必填)",
				},
			},
			"required": []string{"action"},
		},
	}
}

func (t *SkillManageTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	action := getStringFromArgs(args, "action")
	if action == "" {
		return ToolError("action 参数是必填项"), nil
	}

	switch action {
	case "install":
		identifier := getStringFromArgs(args, "identifier")
		if identifier == "" {
			return ToolError("identifier 参数是必填项"), nil
		}
		return t.installSkill(ctx, identifier)
	case "uninstall":
		name := getStringFromArgs(args, "name")
		if name == "" {
			return ToolError("name 参数是必填项"), nil
		}
		return t.uninstallSkill(name)
	case "list":
		return t.listSkills()
	case "search":
		query := getStringFromArgs(args, "query")
		if query == "" {
			return ToolError("query 参数是必填项"), nil
		}
		return t.searchSkills(query)
	default:
		return ToolError(fmt.Sprintf("未知操作: %s", action)), nil
	}
}

func (t *SkillManageTool) installSkill(ctx context.Context, identifier string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return ToolError(fmt.Sprintf("获取主目录失败: %v", err)), nil
	}

	skillsDir := home + "/.nexus/skills"

	// 确保技能目录存在
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		return ToolError(fmt.Sprintf("创建技能目录失败: %v", err)), nil
	}

	// 从标识符提取技能名称
	skillName := extractSkillNameFromIdentifier(identifier)
	if skillName == "" {
		return ToolError("无法从标识符提取技能名称"), nil
	}
	if skillName == "." || skillName == ".." || strings.ContainsAny(skillName, "/\\") {
		return ToolError("无效的技能名称"), nil
	}

	targetDir := filepath.Join(skillsDir, skillName)

	// 检查是否已存在
	if _, err := os.Stat(targetDir); err == nil {
		return ToolError(fmt.Sprintf("技能 %q 已存在", skillName)), nil
	}

	// 尝试使用 git clone
	if isGitIdentifier(identifier) {
		if _, err := exec.LookPath("git"); err != nil {
			return ToolError("系统未安装 git"), nil
		}

		cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", identifier, targetDir)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return ToolError(fmt.Sprintf("克隆失败: %v\n%s", err, string(output))), nil
		}

		return ToolResult(map[string]any{
			"success": true,
			"message": fmt.Sprintf("技能已安装: %s", skillName),
			"name":    skillName,
			"path":    targetDir,
		}), nil
	}

	return ToolError("不支持的标识符格式，请使用 git URL"), nil
}

func (t *SkillManageTool) uninstallSkill(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return ToolError(fmt.Sprintf("获取主目录失败: %v", err)), nil
	}

	skillPath := filepath.Join(home, ".nexus", "skills", name)

	// 路径遍历防护: 确保解析后的路径仍在 skills 目录内
	absPath, err := filepath.Abs(skillPath)
	if err != nil {
		return ToolError(fmt.Sprintf("解析路径失败: %v", err)), nil
	}
	resolved, err := filepath.EvalSymlinks(absPath)
	if err != nil && !os.IsNotExist(err) {
		return ToolError(fmt.Sprintf("解析路径失败: %v", err)), nil
	}
	if resolved == "" {
		resolved = absPath
	}

	skillsDir := filepath.Join(home, ".nexus", "skills")
	absSkillsDir, _ := filepath.Abs(skillsDir)
	if absSkillsDir != "" {
		rel, err := filepath.Rel(absSkillsDir, resolved)
		if err != nil || strings.HasPrefix(rel, "..") {
			return ToolError("路径遍历攻击被阻止"), nil
		}
	}

	// 检查是否存在
	if _, err := os.Stat(skillPath); os.IsNotExist(err) {
		return ToolError(fmt.Sprintf("技能 %q 不存在", name)), nil
	}

	// 删除技能目录
	if err := os.RemoveAll(skillPath); err != nil {
		return ToolError(fmt.Sprintf("删除技能失败: %v", err)), nil
	}

	return ToolResult(map[string]any{
		"success": true,
		"message": fmt.Sprintf("技能已卸载: %s", name),
	}), nil
}

func (t *SkillManageTool) listSkills() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return ToolError(fmt.Sprintf("获取主目录失败: %v", err)), nil
	}

	skillsDir := filepath.Join(home, ".nexus", "skills")

	// 读取技能目录
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return ToolResult(map[string]any{
				"success": true,
				"message": "无已安装的技能",
				"count":   0,
			}), nil
		}
		return ToolError(fmt.Sprintf("读取技能目录失败: %v", err)), nil
	}

	// 过滤出目录
	skills := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			skills = append(skills, entry.Name())
		}
	}

	if len(skills) == 0 {
		return ToolResult(map[string]any{
			"success": true,
			"message": "无已安装的技能",
			"count":   0,
		}), nil
	}

	return ToolResult(map[string]any{
		"success": true,
		"count":   len(skills),
		"skills":  skills,
	}), nil
}

func (t *SkillManageTool) searchSkills(query string) (string, error) {
	// 简单的本地搜索
	home, err := os.UserHomeDir()
	if err != nil {
		return ToolError(fmt.Sprintf("获取主目录失败: %v", err)), nil
	}

	skillsDir := filepath.Join(home, ".nexus", "skills")
	query = strings.ToLower(query)

	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return ToolResult(map[string]any{
			"success": true,
			"message": "无匹配的技能",
			"count":   0,
		}), nil
	}

	matches := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() && strings.Contains(strings.ToLower(entry.Name()), query) {
			matches = append(matches, entry.Name())
		}
	}

	if len(matches) == 0 {
		return ToolResult(map[string]any{
			"success": true,
			"message": "无匹配的技能",
			"count":   0,
		}), nil
	}

	return ToolResult(map[string]any{
		"success": true,
		"count":   len(matches),
		"skills":  matches,
	}), nil
}

func extractSkillNameFromIdentifier(identifier string) string {
	// 处理 git URL: git@github.com:user/repo.git
	if strings.HasPrefix(identifier, "git@") {
		parts := strings.Split(identifier, "/")
		if len(parts) >= 2 {
			name := parts[len(parts)-1]
			name = strings.TrimSuffix(name, ".git")
			return name
		}
	}

	// 处理 HTTPS URL
	if strings.HasPrefix(identifier, "http://") || strings.HasPrefix(identifier, "https://") {
		identifier = strings.Split(identifier, "?")[0]
		identifier = strings.TrimRight(identifier, "/")

		parts := strings.Split(identifier, "/")
		if len(parts) >= 2 {
			last := parts[len(parts)-1]
			if last == "" && len(parts) >= 3 {
				last = parts[len(parts)-2]
			}
			last = strings.TrimSuffix(last, ".git")
			return last
		}
	}

	// 直接使用名称
	return identifier
}

func isGitIdentifier(identifier string) bool {
	if strings.HasPrefix(identifier, "git@") {
		// 提取 git@host:... 中的 host
		rest := identifier[4:]
		colonIdx := strings.Index(rest, ":")
		if colonIdx < 0 {
			return false
		}
		host := rest[:colonIdx]
		return IsAllowedGitHost(host)
	}
	// HTTPS URL: must be from a known host
	if strings.HasPrefix(identifier, "https://") {
		u, err := url.Parse(identifier)
		if err != nil {
			return false
		}
		return IsAllowedGitHost(u.Hostname())
	}
	return false
}

// IsAllowedGitHost 检查 git 主机是否在白名单中。
func IsAllowedGitHost(host string) bool {
	switch host {
	case "github.com", "gitlab.com", "bitbucket.org":
		return true
	default:
		return false
	}
}

