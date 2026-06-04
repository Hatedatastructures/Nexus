package commands

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"nexus-agent/internal/tool"
)

// SkillCommand 实现 nexus skill 命令。
type SkillCommand struct{}

func (c *SkillCommand) Name() string    { return "skill" }
func (c *SkillCommand) Synopsis() string { return "技能管理 (list/search/install)" }

func (c *SkillCommand) Run(args []string) {
	if len(args) == 0 {
		c.listSkills()
		return
	}

	switch args[0] {
	case "list", "ls":
		c.listSkills()
	case "search":
		if len(args) < 2 {
			PrintError("用法: nexus skill search <query>")
		}
		c.searchSkills(strings.Join(args[1:], " "))
	case "install":
		if len(args) < 2 {
			PrintError("用法: nexus skill install <url>")
		}
		c.installSkill(args[1])
	default:
		PrintError("未知子命令: %s", args[0])
	}
}

func (c *SkillCommand) listSkills() {
	tool.DiscoverBuiltin()

	PrintTitle("已注册的可用工具")
	fmt.Println(strings.Repeat("━", 60))

	defs := tool.GetRegistry().GetDefinitions(nil)
	for i, d := range defs {
		fmt.Printf("  %2d. %-25s  %s\n", i+1, d.Name, d.Description)
	}
	fmt.Printf("\n共 %d 个工具\n", len(defs))
}

func (c *SkillCommand) searchSkills(query string) {
	fmt.Println(DimStyle.Render("  技能搜索功能开发中..."))
	fmt.Printf("  查询: %s\n", query)
}

func (c *SkillCommand) installSkill(url string) {
	fmt.Printf("正在从 URL 安装技能: %s\n", url)

	// 确定目标目录
	nexusHome := GetNexusHome()
	skillsDir := nexusHome + "/skills"

	// 确保技能目录存在
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		PrintError("创建技能目录失败: %v", err)
	}

	// 从 URL 提取技能名称
	skillName := extractSkillName(url)
	if skillName == "" {
		skillName = fmt.Sprintf("skill_%d", time.Now().Unix())
	}

	// 防止路径遍历
	if err := validateSkillName(skillName); err != nil {
		PrintError("无效的技能名称: %v", err)
	}

	targetDir := filepath.Join(skillsDir, skillName)

	// 检查是否已存在
	if _, err := os.Stat(targetDir); err == nil {
		PrintError("技能 %q 已存在，请先删除后重试", skillName)
	}

	fmt.Printf("  技能名称: %s\n", skillName)
	fmt.Printf("  目标目录: %s\n", targetDir)
	fmt.Println()

	// 尝试使用 git clone
	if isGitURL(url) {
		if err := cloneGitRepo(url, targetDir); err != nil {
			PrintError("克隆仓库失败: %v", err)
		}
		PrintSuccess("技能安装成功")
		fmt.Printf("  提示: 在 config.yaml 中将 %q 添加到 skills.external_dirs 以启用\n", targetDir)
		return
	}

	PrintError("不支持的 URL 格式，请使用 git 仓库 URL")
}

func extractSkillName(url string) string {
	// 处理 git URL: git@github.com:user/repo.git
	if strings.HasPrefix(url, "git@") {
		parts := strings.Split(url, "/")
		if len(parts) >= 2 {
			name := parts[len(parts)-1]
			name = strings.TrimSuffix(name, ".git")
			return name
		}
	}

	// 处理 HTTPS URL
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		url = strings.Split(url, "?")[0]
		url = strings.TrimRight(url, "/")

		parts := strings.Split(url, "/")
		if len(parts) >= 2 {
			last := parts[len(parts)-1]
			if last == "" && len(parts) >= 3 {
				last = parts[len(parts)-2]
			}
			last = strings.TrimSuffix(last, ".git")
			last = strings.TrimSuffix(last, ".md")
			return last
		}
	}

	return ""
}

func isGitURL(url string) bool {
	return strings.HasPrefix(url, "git@") ||
		strings.HasSuffix(url, ".git") ||
		(strings.HasPrefix(url, "https://github.com/") && !strings.HasSuffix(url, ".md"))
}

func cloneGitRepo(repoURL, targetDir string) error {
	// 检查 git 是否在 PATH 中
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("系统未安装 git，无法克隆仓库")
	}

	// 协议白名单
	if !strings.HasPrefix(repoURL, "https://") && !strings.HasPrefix(repoURL, "git@") {
		return fmt.Errorf("仅支持 https:// 和 git@ 协议的仓库地址")
	}

	// 主机白名单
	u, err := url.Parse(repoURL)
	if err != nil {
		return fmt.Errorf("无效的仓库地址: %w", err)
	}
	if !tool.IsAllowedGitHost(u.Hostname()) {
		return fmt.Errorf("不允许的主机: %s", u.Hostname())
	}

	// SSRF 防御
	if safe, reason := tool.CheckURLSafety(repoURL); !safe {
		return fmt.Errorf("URL 安全检查失败: %s", reason)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", repoURL, targetDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone 失败: %v\n%s", err, string(output))
	}

	return nil
}

func init() {
	Register(&SkillCommand{})
}
