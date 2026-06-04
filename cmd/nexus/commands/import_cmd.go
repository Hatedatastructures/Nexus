package commands

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"nexus-agent/internal/tool"
)

// ImportCommand 实现 nexus import 命令。
type ImportCommand struct{}

func (c *ImportCommand) Name() string    { return "import" }
func (c *ImportCommand) Synopsis() string { return "导入资源 (skills)" }

func (c *ImportCommand) Run(args []string) {
	if len(args) == 0 {
		fmt.Println("用法: nexus import <subcommand>")
		fmt.Println("可用子命令:")
		fmt.Println("  skills <url>  - 从 URL 安装技能")
		return
	}

	switch args[0] {
	case "skills":
		if len(args) < 2 {
			PrintError("用法: nexus import skills <url>")
		}
		c.importSkills(args[1])
	default:
		PrintError("未知子命令: %s", args[0])
	}
}

func (c *ImportCommand) importSkills(url string) {
	fmt.Printf("正在从 URL 安装技能: %s\n", url)

	// 确定目标目录
	nexusHome := GetNexusHome()
	skillsDir := nexusHome + "/skills"

	// 确保技能目录存在
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		PrintError("创建技能目录失败: %v", err)
	}

	// 从 URL 提取技能名称
	skillName := extractSkillNameFromURL(url)
	if skillName == "" {
		skillName = fmt.Sprintf("skill_%d", time.Now().Unix())
	}

	// 防止路径遍历
	if err := validateSkillName(skillName); err != nil {
		PrintError("无效的技能名称: %v", err)
	}

	targetDir := skillsDir + "/" + skillName

	// 检查是否已存在
	if _, err := os.Stat(targetDir); err == nil {
		PrintError("技能 %q 已存在，请先删除后重试", skillName)
	}

	fmt.Printf("  技能名称: %s\n", skillName)
	fmt.Printf("  目标目录: %s\n", targetDir)
	fmt.Println()

	// 尝试使用 git clone（如果 URL 是 git 仓库）
	if isGitURLFromImport(url) {
		if err := cloneGitRepoFromImport(url, targetDir); err != nil {
			PrintError("克隆仓库失败: %v", err)
		}
		PrintSuccess("技能安装成功")
		fmt.Printf("  提示: 在 config.yaml 中将 %q 添加到 skills.external_dirs 以启用\n", targetDir)
		return
	}

	// 否则尝试 HTTP 下载（假设是 SKILL.md 文件的直接链接）
	if err := downloadSkillFileFromImport(url, targetDir); err != nil {
		PrintError("下载技能文件失败: %v", err)
	}

	PrintSuccess("技能文件下载成功")
	fmt.Printf("  提示: 在 config.yaml 中将 %q 添加到 skills.external_dirs 以启用\n", targetDir)
}

func extractSkillNameFromURL(url string) string {
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

func isGitURLFromImport(rawURL string) bool {
	if strings.HasPrefix(rawURL, "git@") {
		rest := rawURL[4:]
		colonIdx := strings.Index(rest, ":")
		if colonIdx < 0 {
			return false
		}
		return tool.IsAllowedGitHost(rest[:colonIdx])
	}
	if strings.HasPrefix(rawURL, "https://") {
		u, err := url.Parse(rawURL)
		if err != nil {
			return false
		}
		return tool.IsAllowedGitHost(u.Hostname())
	}
	return false
}

func cloneGitRepoFromImport(rawURL, targetDir string) error {
	// SSRF 防御: 验证 URL 安全性
	if safe, reason := tool.CheckURLSafety(rawURL); !safe {
		return fmt.Errorf("URL 安全检查失败: %s", reason)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 检查 git 是否在 PATH 中
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("系统未安装 git，无法克隆仓库")
	}

	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", rawURL, targetDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone 失败: %v\n%s", err, string(output))
	}

	return nil
}

func downloadSkillFileFromImport(rawURL, targetDir string) error {
	// 协议白名单: 仅允许 HTTPS
	if !strings.HasPrefix(rawURL, "https://") {
		return fmt.Errorf("仅允许 HTTPS 协议下载技能文件")
	}

	// SSRF 防御
	if safe, reason := tool.CheckURLSafety(rawURL); !safe {
		return fmt.Errorf("URL 安全检查失败: %s", reason)
	}

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("创建目录失败: %v", err)
	}

	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return fmt.Errorf("创建请求失败: %v", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("下载失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP 错误: %s", resp.Status)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return fmt.Errorf("读取响应失败: %v", err)
	}

	targetPath := targetDir + "/SKILL.md"
	if err := os.WriteFile(targetPath, data, 0644); err != nil {
		return fmt.Errorf("写入文件失败: %v", err)
	}

	return nil
}

func init() {
	Register(&ImportCommand{})
}
