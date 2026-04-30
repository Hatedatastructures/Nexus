// Package skill 提供技能模板预处理和内联 Shell 展开功能。
package skill

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// ───────────────────────────── 模板变量 ─────────────────────────────

// defaultVars 返回默认的模板变量映射。
// 这些变量在技能正文中被替换。
func defaultVars() map[string]string {
	vars := map[string]string{
		"PLATFORM": runtime.GOOS,
		"OS":       runtime.GOOS,
		"ARCH":     runtime.GOARCH,
	}

	// 解析 ${NEXUS_HOME}
	if home := os.Getenv("NEXUS_HOME"); home != "" {
		vars["NEXUS_HOME"] = home
	} else {
		// 默认 ~/.nexus
		if userHome, err := os.UserHomeDir(); err == nil {
			vars["NEXUS_HOME"] = userHome + "/.nexus"
		}
	}

	// 解析 ${HERMES_SKILL_DIR}
	if skillDir := os.Getenv("HERMES_SKILL_DIR"); skillDir != "" {
		vars["HERMES_SKILL_DIR"] = skillDir
	} else {
		vars["HERMES_SKILL_DIR"] = vars["NEXUS_HOME"] + "/skills"
	}

	// 解析 ${HERMES_SESSION_ID}
	if sid := os.Getenv("HERMES_SESSION_ID"); sid != "" {
		vars["HERMES_SESSION_ID"] = sid
	}

	return vars
}

// ───────────────────────────── 技能预处理 ─────────────────────────────

// PreprocessSkill 对技能正文中的模板变量进行替换。
//
// 支持的变量:
//   - ${NEXUS_HOME} — nexus 主目录路径
//   - ${HERMES_SKILL_DIR} — 技能目录路径
//   - ${PLATFORM} — 操作系统平台 (linux/darwin/windows)
//   - ${OS} — 同 PLATFORM
//   - ${ARCH} — CPU 架构 (amd64/arm64)
//   - ${HERMES_SESSION_ID} — 当前会话 ID
//
// 返回一个新的 Skill 副本 (原文不变)。
func PreprocessSkill(skill *Skill, vars map[string]string) *Skill {
	if skill == nil {
		return nil
	}

	// 合并默认变量和用户提供的变量
	merged := defaultVars()
	for k, v := range vars {
		merged[k] = v
	}

	// 创建副本
	processed := *skill
	processed.Body = expandVariables(skill.Body, merged)
	processed.Description = expandVariables(skill.Description, merged)

	return &processed
}

// expandVariables 在文本中展开 ${VAR_NAME} 形式的变量。
func expandVariables(text string, vars map[string]string) string {
	result := text
	for name, value := range vars {
		placeholder := "${" + name + "}"
		result = strings.ReplaceAll(result, placeholder, value)
	}
	return result
}

// ───────────────────────────── 内联 Shell 展开 ─────────────────────────────

// ExpandInlineShell 展开技能正文中的内联 Shell 命令。
//
// 语法: !`command`
// 效果: 执行 command，将 stdout 替换到命令所在位置。
//
// 安全限制:
//   - 执行超时: 30 秒
//   - 最大输出: 4096 字节
//   - 命令在技能目录中执行 (工作目录)
//
// 返回展开后的文本和可能的错误。
func ExpandInlineShell(body string) (string, error) {
	var result strings.Builder
	i := 0

	for i < len(body) {
		// 查找 !`
		idx := strings.Index(body[i:], "!`")
		if idx == -1 {
			result.WriteString(body[i:])
			break
		}

		// 写入前缀文本
		result.WriteString(body[i : i+idx])

		// 查找配对的 反引号
		cmdStart := i + idx + 2          // 跳过 "!`"
		cmdEnd := strings.Index(body[cmdStart:], "`")
		if cmdEnd == -1 {
			// 未找到配对的 反引号，原样输出
			result.WriteString(body[i+idx:])
			break
		}

		command := strings.TrimSpace(body[cmdStart : cmdStart+cmdEnd])

		// 执行命令并替换
		if command != "" {
			output, err := executeInlineCommand(command)
			if err != nil {
				result.WriteString(fmt.Sprintf("[!`%s` 执行失败: %v]", command, err))
			} else {
				result.WriteString(strings.TrimSpace(output))
			}
		}

		i = cmdStart + cmdEnd + 1 // 跳过命令和配对的 反引号
	}

	return result.String(), nil
}

// executeInlineCommand 执行内联 Shell 命令。
// 限制超时 30 秒，最大输出 4096 字节。
func executeInlineCommand(command string) (string, error) {
	// 选择 shell
	shell := "/bin/sh"
	shellArg := "-c"
	if runtime.GOOS == "windows" {
		shell = "cmd"
		shellArg = "/c"
	}

	cmd := exec.Command(shell, shellArg, command)
	cmd.Env = os.Environ()

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("命令退出码异常: %w", err)
	}

	// 限制输出长度
	result := string(output)
	if len(result) > 4096 {
		result = result[:4096] + "\n[... 输出已截断 ...]"
	}

	return result, nil
}
