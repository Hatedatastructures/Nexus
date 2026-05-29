// Package tool 提供敏感路径防护功能。
// 阻止读写敏感系统文件和目录，防止凭证泄露和系统破坏。
// 基于 upstream file_safety.py 的安全策略进行 Go 移植。
package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ───────────────────────────── 敏感路径常量 ─────────────────────────────

// blockedWritePaths 是禁止写入的精确路径（相对于用户 home 或 nexus home）。
var blockedWritePaths = []string{
	".ssh/authorized_keys",
	".ssh/id_rsa",
	".ssh/id_ed25519",
	".ssh/config",
	".bashrc",
	".zshrc",
	".profile",
	".bash_profile",
	".zprofile",
	".netrc",
	".pgpass",
	".npmrc",
	".pypirc",
	".git-credentials",
}

// blockedWritePrefixes 是禁止写入的目录前缀（相对于用户 home）。
var blockedWritePrefixes = []string{
	".ssh" + string(os.PathSeparator),
	".aws" + string(os.PathSeparator),
	".gnupg" + string(os.PathSeparator),
	".kube" + string(os.PathSeparator),
	".docker" + string(os.PathSeparator),
	".azure" + string(os.PathSeparator),
	filepath.Join(".config", "gh") + string(os.PathSeparator),
	filepath.Join(".config", "gcloud") + string(os.PathSeparator),
}

// blockedProjectEnvBasenames 是项目目录中禁止读取的 env 文件名。
var blockedProjectEnvBasenames = map[string]bool{
	".env":            true,
	".env.local":      true,
	".env.development": true,
	".env.production": true,
	".env.test":       true,
	".env.staging":    true,
	".envrc":          true,
}

// nexusControlFiles 是 Nexus 控制平面文件（禁止工具写入）。
var nexusControlFiles = []string{
	"auth.json",
	"config.yaml",
	"webhook_subscriptions.json",
}

// nexusControlDirs 是 Nexus 控制平面目录前缀（禁止工具写入）。
var nexusControlDirs = []string{
	"mcp-tokens",
	"pairing",
}

// ───────────────────────────── 路径检查函数 ─────────────────────────────

// getHomeDir 获取用户 home 目录。
func getHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// getNexusHome 获取 Nexus home 目录。
func getNexusHome() string {
	home := getHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".nexus")
}

// IsBlockedWritePath 检查路径是否在写入黑名单中。
// 解析 symlink 后检查是否匹配敏感文件或目录。
func IsBlockedWritePath(path string) error {
	resolved, err := filepath.Abs(path)
	if err != nil {
		return nil
	}

	// 解析 symlink
	realPath, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil
		}
		// 文件不存在时解析父目录
		parent := filepath.Dir(resolved)
		realParent, evalErr := filepath.EvalSymlinks(parent)
		if evalErr != nil {
			return nil
		}
		realPath = filepath.Join(realParent, filepath.Base(resolved))
	}

	home := getHomeDir()
	nexusHome := getNexusHome()

	// 检查绝对系统路径（仅 Unix）
	if runtime.GOOS != "windows" {
		systemBlocked := []string{"/etc/sudoers", "/etc/passwd", "/etc/shadow"}
		for _, bp := range systemBlocked {
			if realPath == bp {
				return fmt.Errorf("禁止写入系统文件: %s", path)
			}
		}
	}

	// 检查 home 目录下的敏感文件
	if home != "" {
		for _, bp := range blockedWritePaths {
			blocked := filepath.Join(home, bp)
			if realPath == blocked {
				return fmt.Errorf("禁止写入敏感文件: %s", path)
			}
		}

		// 检查 home 目录下的敏感目录前缀
		for _, prefix := range blockedWritePrefixes {
			prefixPath := filepath.Join(home, prefix)
			if strings.HasPrefix(realPath+string(os.PathSeparator), prefixPath) || realPath == filepath.Join(home, strings.TrimSuffix(prefix, string(os.PathSeparator))) {
				return fmt.Errorf("禁止写入敏感目录: %s", path)
			}
		}
	}

	// 检查 Nexus 控制平面文件
	for _, base := range []string{nexusHome} {
		if base == "" {
			continue
		}
		for _, cf := range nexusControlFiles {
			if realPath == filepath.Join(base, cf) {
				return fmt.Errorf("禁止写入 Nexus 控制文件: %s", path)
			}
		}
		for _, cd := range nexusControlDirs {
			dirPath := filepath.Join(base, cd)
			if strings.HasPrefix(realPath+string(os.PathSeparator), dirPath+string(os.PathSeparator)) {
				return fmt.Errorf("禁止写入 Nexus 控制目录: %s", path)
			}
		}
	}

	return nil
}

// IsBlockedReadPath 检查路径是否在读取黑名单中。
// 阻止读取 /proc/*/environ、项目 .env 文件等敏感文件。
func IsBlockedReadPath(path string) error {
	resolved, err := filepath.Abs(path)
	if err != nil {
		return nil
	}

	// 解析 symlink
	realPath, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil
		}
		parent := filepath.Dir(resolved)
		realParent, evalErr := filepath.EvalSymlinks(parent)
		if evalErr != nil {
			return nil
		}
		realPath = filepath.Join(realParent, filepath.Base(resolved))
	}

	// 阻止 /proc/*/environ（仅 Linux）
	if runtime.GOOS == "linux" {
		if strings.HasPrefix(realPath, "/proc/") && strings.HasSuffix(realPath, "/environ") {
			return fmt.Errorf("禁止读取进程环境变量: %s", path)
		}
	}

	// 阻止项目目录中的 .env 文件
	base := filepath.Base(realPath)
	if blockedProjectEnvBasenames[base] {
		// 仅当不在 Nexus home 目录内时阻止（Nexus 自身的 .env 允许读取）
		nexusHome := getNexusHome()
		if nexusHome != "" && !strings.HasPrefix(realPath, nexusHome+string(os.PathSeparator)) {
			return fmt.Errorf("禁止读取项目环境文件: %s", path)
		}
	}

	// 阻止读取 Nexus 凭证文件
	nexusHome := getNexusHome()
	if nexusHome != "" {
		credFiles := []string{
			"auth.json",
			"auth.lock",
			".env",
			"webhook_subscriptions.json",
			filepath.Join("auth", "google_oauth.json"),
		}
		for _, cf := range credFiles {
			if realPath == filepath.Join(nexusHome, cf) {
				return fmt.Errorf("禁止读取凭证文件: %s", path)
			}
		}
		// 阻止 mcp-tokens 目录
		mcpDir := filepath.Join(nexusHome, "mcp-tokens")
		if strings.HasPrefix(realPath+string(os.PathSeparator), mcpDir+string(os.PathSeparator)) {
			return fmt.Errorf("禁止读取 MCP 令牌目录: %s", path)
		}
	}

	return nil
}
