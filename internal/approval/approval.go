// Package approval 提供命令安全审批引擎。
// 检测危险命令模式 (如 rm -rf、git push --force)，
// 并根据配置的审批模式 (off / smart / always) 决定是否放行。
package approval

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
)

// ───────────────────────────── 审批结果 ─────────────────────────────

// Result 表示命令审批的结果
type Result int

const (
	Approved Result = iota // 通过
	Denied                 // 拒绝
	Pending                // 等待用户审批
)

// String 返回审批结果的可读字符串。
func (r Result) String() string {
	switch r {
	case Approved:
		return "Approved"
	case Denied:
		return "Denied"
	case Pending:
		return "Pending"
	default:
		return "Unknown"
	}
}

// ───────────────────────────── 审批检查器 ─────────────────────────────

// Checker 是命令安全审批检查器。
// 维护危险命令模式列表和审批状态。
type Checker struct {
	mode      string   // 审批模式: "off" / "smart" / "always"
	modeMu    sync.RWMutex // 保护 mode 字段的并发读写
	allowlist []string // 永久允许的模式
	blocklist []string // 永久禁止的模式
}

// NewChecker 创建审批检查器
func NewChecker(mode string, allowlist, blocklist []string) *Checker {
	if mode == "" {
		mode = "smart"
	}
	return &Checker{
		mode:      mode,
		allowlist: allowlist,
		blocklist: blocklist,
	}
}

// Mode 返回当前的审批模式。
func (c *Checker) Mode() string {
	c.modeMu.RLock()
	defer c.modeMu.RUnlock()
	return c.mode
}

// SetMode 设置审批模式。
func (c *Checker) SetMode(mode string) {
	c.modeMu.Lock()
	defer c.modeMu.Unlock()
	c.mode = mode
}

// ───────────────────────────── 硬封锁模式 (绝对禁止) ─────────────────────────────

// hardBlocked 包含绝对禁止的破坏性命令模式。
// 匹配这些模式的命令在任何审批模式下都会直接拒绝。
type hardBlockedPattern struct {
	pattern *regexp.Regexp
	desc    string
}

var hardBlocked = []hardBlockedPattern{
	// rm -rf / — 递归强制删除根目录
	{regexp.MustCompile(`\brm\s+.*(-rf|-fr)\s+(/\s*|/\*|~/\s*|~/\*)`), "递归删除根目录 (rm -rf /)"},
	{regexp.MustCompile(`\brm\s+(-rf|-fr)\s+/(\S*/?)`), "递归删除根目录下的系统路径"},

	// mkfs — 创建文件系统 (格式化磁盘)
	{regexp.MustCompile(`\bmkfs\b`), "创建文件系统/格式化 (mkfs)"},

	// dd 写入设备
	{regexp.MustCompile(`\bdd\s+.*of=/dev/sd[a-z]`), "dd 直接写入块设备"},
	{regexp.MustCompile(`\bdd\s+.*of=/dev/nvme`), "dd 写入 NVMe 设备"},
	{regexp.MustCompile(`\bdd\s+.*of=/dev/mmcblk`), "dd 写入 MMC 块设备"},

	// shutdown/reboot/poweroff
	{regexp.MustCompile(`\b(shutdown|reboot|poweroff|halt)\s`), "系统关机/重启"},

	// fork bomb
	{regexp.MustCompile(`:\s*\(\s*\)\s*{\s*:\s*\|\s*:\s*&\s*}\s*;`), "fork bomb (shell 函数递归)"},
	{regexp.MustCompile(`(?i)\bperl\s+-e\s+.*fork\s+while\b`), "Perl fork bomb"},

	// fdisk/parted 修改分区表
	{regexp.MustCompile(`\b(fdisk|parted)\s+/dev/`), "修改磁盘分区表"},

	// chmod 777 / 等危险权限修改
	{regexp.MustCompile(`\bchmod\s+.*777\s+/`), "危险权限修改 (chmod 777 系统路径)"},
}

// ───────────────────────────── 危险模式 (需要审批) ─────────────────────────────

// dangerousPatterns 包含需要用户审批的危险命令模式。
// 匹配这些模式的命令在 smart 和 always 模式下需要审批。
type dangerousPattern struct {
	pattern *regexp.Regexp
	desc    string
}

var dangerousPatterns = []dangerousPattern{
	// 递归删除 (非根目录)
	{regexp.MustCompile(`\brm\s+.*(-r|-rf|-fr|--recursive)\b`), "递归删除操作"},

	// SQL DROP / TRUNCATE
	{regexp.MustCompile(`(?i)\bdrop\s+(table|database|schema|index)\b`), "SQL DROP 操作"},
	{regexp.MustCompile(`(?i)\btruncate\s+(table\s+)?\w+`), "SQL TRUNCATE 操作"},
	{regexp.MustCompile(`(?i)\bdelete\s+from\s+\w+`), "SQL DELETE FROM 操作"},

	// git push --force
	{regexp.MustCompile(`\bgit\s+push\s+.*(--force|--force-with-lease|-f)\b`), "Git 强制推送"},

	// 输出重定向到块设备
	{regexp.MustCompile(`>\s*/dev/sd[a-z]`), "覆盖写入块设备"},
	{regexp.MustCompile(`>\s*/dev/nvme`), "覆盖写入 NVMe 设备"},

	// curl | sh / wget | sh — 未审查的脚本执行
	{regexp.MustCompile(`(curl|wget)\s+.*\|\s*(sh|bash|zsh)`), "管道执行远程脚本 (curl/wget | sh)"},

	// eval 危险使用
	{regexp.MustCompile(`\beval\s+`), "eval 命令执行"},

	// mv/cp 覆盖系统文件
	{regexp.MustCompile(`\b(mv|cp)\s+.*\s+(/etc/|/usr/|/bin/|/sbin/|/boot/)`), "移动/复制文件到系统目录"},

	// chown 系统目录
	{regexp.MustCompile(`\bchown\s+.*\s+(/etc/|/usr/|/bin/|/sbin/|/boot/)`), "修改系统路径所有权"},

	// docker privileged / mount host
	{regexp.MustCompile(`docker\s+(run|create)\s+.*(--privileged|--cap-add=SYS_ADMIN)`), "Docker 特权模式"},

	// 追加到系统配置文件
	{regexp.MustCompile(`>>\s*/etc/`), "追加重定向到系统配置"},
}

// ───────────────────────────── 安全模式 (自动通过) ─────────────────────────────

// safePatterns 包含明确安全的命令模式。
// 匹配这些模式的命令在任何模式下都自动通过。
var safePatterns = []*regexp.Regexp{
	regexp.MustCompile(`^\s*(ls|dir)\s`),             // 列出目录
	regexp.MustCompile(`^\s*(pwd|cwd)\s*$`),          // 显示当前目录
	regexp.MustCompile(`^\s*(cat|head|tail|less)\s`), // 查看文件内容
	regexp.MustCompile(`^\s*(echo|print|printf)\s`),  // 打印文本
	regexp.MustCompile(`^\s*(whoami|who|id)\s*$`),    // 用户信息
	regexp.MustCompile(`^\s*(date|time|uptime)\s*$`), // 系统时间信息
	regexp.MustCompile(`^\s*(uname|hostname)\s`),     // 系统信息
	regexp.MustCompile(`^\s*(env|printenv|set)\s`),   // 环境变量
	regexp.MustCompile(`^\s*(which|whereis|type)\s`), // 查找命令路径
	regexp.MustCompile(`^\s*(find|locate)\s`),        // 查找文件 (安全，无 -exec)
	regexp.MustCompile(`^\s*(grep|rg|ag)\s`),         // 文本搜索
	regexp.MustCompile(`^\s*(wc|sort|uniq)\s`),       // 文本处理
	regexp.MustCompile(`^\s*(df|du)\s`),              // 磁盘使用情况
	regexp.MustCompile(`^\s*(ps|top|htop)\s`),        // 进程查看
	regexp.MustCompile(`^\s*(curl|wget)\s+-[oO]`),    // 下载到文件
	regexp.MustCompile(`^\s*(git\s+status|git\s+log|git\s+diff|git\s+branch|git\s+show)`), // Git 只读操作
	regexp.MustCompile(`^\s*(docker\s+ps|docker\s+images|docker\s+inspect|docker\s+logs|docker\s+stats)`), // Docker 只读操作
}

// ───────────────────────────── 检查逻辑 ─────────────────────────────

// Check 检查命令是否安全。
// command 是完整的 shell 命令字符串。
// 返回审批结果和原因描述。
func (c *Checker) Check(ctx context.Context, command string) (Result, string) {
	command = strings.TrimSpace(command)

	if command == "" {
		return Approved, "空命令"
	}

	c.modeMu.RLock()
	mode := c.mode
	c.modeMu.RUnlock()

	// off 模式: 始终通过
	if mode == "off" {
		return Approved, "审批模式为 off"
	}

	// 检查自定义黑名单
	for _, blocked := range c.blocklist {
		if strings.Contains(command, blocked) {
			slog.Warn("command matched custom blocklist, denying execution",
				"command", truncateForLog(command, 200),
				"blocked_pattern", blocked,
			)
			return Denied, fmt.Sprintf("自定义黑名单: 匹配 '%s'", blocked)
		}
	}

	// 检查自定义白名单
	for _, allowed := range c.allowlist {
		if strings.Contains(command, allowed) {
			// 白名单不能绕过硬阻止检查
			if result, reason := c.checkHardBlocked(command); result == Denied {
				slog.Warn("allowlist bypass blocked by hard-blocked check",
					"command", truncateForLog(command, 200),
					"allowlist_pattern", allowed,
					"hard_blocked_reason", reason,
				)
				return Denied, reason
			}
			return Approved, fmt.Sprintf("自定义白名单: 匹配 '%s'", allowed)
		}
	}

	// 硬封锁检查 (任何模式下都拒绝)
	if result, reason := c.checkHardBlocked(command); result == Denied {
		slog.Warn("command matched hard-block pattern",
			"command", truncateForLog(command, 200),
			"reason", reason,
		)
		return Denied, reason
	}

	// always 模式: 始终需要审批 (除非安全模式)
	if mode == "always" {
		if c.isSafe(command) {
			return Approved, "安全命令 (always 模式自动放行)"
		}
		return Pending, "always 模式需要审批"
	}

	// smart 模式: 仅检查危险命令
	if mode == "smart" {
		// 安全命令自动通过
		if c.isSafe(command) {
			return Approved, "安全命令自动通过"
		}

		// 检查危险模式
		if result, reason := c.checkDangerous(command); result != Approved {
			slog.Info("command requires user approval",
				"command", truncateForLog(command, 200),
				"reason", reason,
			)
			return result, reason
		}

		return Approved, "smart 模式: 未检测到危险模式"
	}

	// 默认通过 (未知模式)
	return Approved, fmt.Sprintf("未知审批模式: %s", mode)
}

// CheckTool 检查工具调用是否安全。
// toolName 是工具名称，args 是工具参数。
func (c *Checker) CheckTool(ctx context.Context, toolName string, args map[string]any) (Result, string) {
	// 非终端工具默认安全
	if toolName != "terminal" {
		return Approved, ""
	}
	cmd, _ := args["command"].(string)
	return c.Check(ctx, cmd)
}

// ───────────────────────────── 内部检查方法 ─────────────────────────────

// checkHardBlocked 检查命令是否匹配硬封锁模式。
func (c *Checker) checkHardBlocked(command string) (Result, string) {
	for _, hp := range hardBlocked {
		if hp.pattern.MatchString(command) {
			return Denied, fmt.Sprintf("硬封锁: %s", hp.desc)
		}
	}

	// 特殊检查: find ... -exec 危险
	if findExecDangerous := regexp.MustCompile(`\bfind\s+.*\s+-exec\s+`); findExecDangerous.MatchString(command) {
		// find -exec 可能执行危险命令，但 find -exec rm -rf / 应被硬封锁拦截
		if strings.Contains(command, "rm -rf /") || strings.Contains(command, "rm -fr /") {
			return Denied, "硬封锁: find -exec 配合递归删除根目录"
		}
	}

	return Approved, ""
}

// checkDangerous 检查命令是否匹配危险模式 (需要审批)。
func (c *Checker) checkDangerous(command string) (Result, string) {
	for _, dp := range dangerousPatterns {
		if dp.pattern.MatchString(command) {
			// 对于递归删除，如果只删除当前目录下的内容，降低风险等级
			if strings.Contains(dp.desc, "递归删除") {
				if c.isSafeDelete(command) {
					continue
				}
			}
			return Pending, fmt.Sprintf("危险命令: %s", dp.desc)
		}
	}
	return Approved, ""
}

// isSafe 检查命令是否为明确安全的操作。
// 即使匹配安全模式，也拒绝包含 shell 链式元字符的命令。
func (c *Checker) isSafe(command string) bool {
	for _, sp := range safePatterns {
		if sp.MatchString(command) {
			// 匹配安全模式后，检查是否包含 shell 链式元字符
			// 如 "ls; rm -rf /" 匹配 "^\s*ls\s" 但实际执行危险操作
			if containsShellMetacharacters(command) {
				return false
			}
			return true
		}
	}
	return false
}

// containsShellMetacharacters 检查命令是否包含 shell 链式元字符。
// 这些字符可用于在看似安全的命令后追加危险操作。
func containsShellMetacharacters(cmd string) bool {
	return strings.ContainsAny(cmd, ";|`$") ||
		strings.Contains(cmd, "&&") ||
		strings.Contains(cmd, "||") ||
		strings.Contains(cmd, "$(")
}

// isSafeDelete 检查递归删除是否在安全的子目录中执行。
// 仅在不以 /、~、.. 开头的相对路径中删除时返回 true。
func (c *Checker) isSafeDelete(command string) bool {
	// 提取删除目标路径
	re := regexp.MustCompile(`\brm\s+(-rf|-fr|-r)\s+([^\s;|&]+)`)
	matches := re.FindStringSubmatch(command)
	if len(matches) < 3 {
		return false
	}
	target := matches[2]

	// 安全目标: 不以 / 或 ~ 开头的非特殊路径
	if strings.HasPrefix(target, "/") {
		return false
	}
	if strings.HasPrefix(target, "~") {
		return false
	}
	if strings.HasPrefix(target, "..") {
		return false
	}
	if target == "." {
		return false
	}

	return true
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

// truncateForLog 截断字符串用于日志输出。
func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
