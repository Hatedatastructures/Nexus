// Package commands 提供 Nexus CLI 的模块化命令实现。
package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ───────────────────────────── 样式定义 ─────────────────────────────

var (
	TitleStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#6EE7B7")).Bold(true)
	DimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#6B7280"))
	UserStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#FBBF24")).Bold(true)
	ErrorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#F87171")).Bold(true)
	GreenBold    = lipgloss.NewStyle().Foreground(lipgloss.Color("#34D399")).Bold(true)
	ReasoningLbl = lipgloss.NewStyle().Foreground(lipgloss.Color("#A78BFA")).Bold(true)
)

// ───────────────────────────── 命令接口 ─────────────────────────────

// Command 定义 CLI 命令接口。
type Command interface {
	// Name 返回命令名称（用于路由）。
	Name() string
	// Synopsis 返回一行简短描述。
	Synopsis() string
	// Run 执行命令，args 为子命令后的参数。
	Run(args []string)
}

// ───────────────────────────── 命令注册表 ─────────────────────────────

var commandRegistry = make(map[string]Command)

// Register 注册一个命令到全局注册表。
func Register(cmd Command) {
	commandRegistry[cmd.Name()] = cmd
}

// GetCommand 根据名称获取命令。
func GetCommand(name string) (Command, bool) {
	cmd, ok := commandRegistry[name]
	return cmd, ok
}

// ListCommands 返回所有已注册的命令名称（按注册顺序）。
func ListCommands() []string {
	names := make([]string, 0, len(commandRegistry))
	for name := range commandRegistry {
		names = append(names, name)
	}
	return names
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

// PrintTitle 打印标题。
func PrintTitle(text string) {
	fmt.Println(TitleStyle.Render(text))
	fmt.Println(strings.Repeat("━", 60))
}

// PrintSection 打印段落标题。
func PrintSection(text string) {
	fmt.Println(GreenBold.Render("  [" + text + "]"))
}

// PrintError 打印错误信息并退出。
func PrintError(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s\n", ErrorStyle.Render(fmt.Sprintf(format, args...)))
	os.Exit(1)
}

// PrintWarning 打印警告信息。
func PrintWarning(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s\n", DimStyle.Render(fmt.Sprintf(format, args...)))
}

// PrintSuccess 打印成功信息。
func PrintSuccess(text string) {
	fmt.Println(GreenBold.Render("  ✓ " + text))
}

// MaskAPIKey 将 API Key 脱敏显示（仅显示前后各 4 个字符）。
func MaskAPIKey(key string) string {
	if key == "" {
		return "(未设置)"
	}
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "..." + key[len(key)-4:]
}

// GetNexusHome 返回 ~/.nexus 目录路径。
func GetNexusHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".nexus"
	}
	return home + "/.nexus"
}

// GetDBPath 返回状态数据库路径。
func GetDBPath() string {
	return GetNexusHome() + "/nexus.db"
}

// GetConfigPath 返回配置文件路径。
func GetConfigPath() string {
	return GetNexusHome() + "/config.yaml"
}

// GetLogsDir 返回日志目录路径。
func GetLogsDir() string {
	return GetNexusHome() + "/logs"
}

// FileExists 检查文件是否存在。
func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// validateSkillName 验证技能名称不包含路径遍历成分。
// 拒绝 ".."、"/"、"\"、空字节，以及空名称。
func validateSkillName(name string) error {
	if name == "" {
		return fmt.Errorf("技能名称为空")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("技能名称不能为 %q", name)
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("技能名称不能包含 '..'")
	}
	if strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("技能名称不能包含路径分隔符")
	}
	if strings.ContainsRune(name, 0) {
		return fmt.Errorf("技能名称不能包含空字节")
	}
	return nil
}
