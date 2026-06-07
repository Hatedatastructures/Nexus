// Package tool 提供技能安全扫描功能。
// 通过威胁模式匹配检测技能中的潜在安全风险。
// 支持多种威胁类别: 数据外泄、注入攻击、破坏性操作、持久化、网络滥用。
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ───────────────────────────── 信任级别 ─────────────────────────────

// TrustLevel 表示技能的信任级别。
type TrustLevel string

const (
	TrustBuiltin      TrustLevel = "builtin"       // 内置技能 (最高信任)
	TrustTrusted      TrustLevel = "trusted"       // 受信任的技能
	TrustCommunity    TrustLevel = "community"     // 社区技能
	TrustAgentCreated TrustLevel = "agent_created" // 代理创建的技能 (最低信任)
)

// ───────────────────────────── 扫描结果 ─────────────────────────────

// ScanResult 表示技能安全扫描的结果。
type ScanResult struct {
	TrustLevel TrustLevel `json:"trust_level"` // 技能信任级别
	Verdict    string     `json:"verdict"`     // 扫描结论: pass / warn / block
	Findings   []Finding  `json:"findings"`    // 发现的问题列表
	FileCount  int        `json:"file_count"`  // 扫描的文件数量
	TotalSize  int64      `json:"total_size"`  // 扫描内容总字节数
}

// Finding 表示扫描发现的单个安全问题。
type Finding struct {
	Pattern  string `json:"pattern"`  // 匹配的模式名称
	File     string `json:"file"`     // 问题所在的文件
	Line     int    `json:"line"`     // 问题所在的行号
	Severity string `json:"severity"` // 严重程度: critical / high / medium / low
	Detail   string `json:"detail"`   // 问题描述
}

// ───────────────────────────── 威胁模式定义 ─────────────────────────────

// ThreatPattern 定义一个威胁检测模式。
type ThreatPattern struct {
	Name     string         // 模式名称
	Category string         // 威胁类别
	Regex    *regexp.Regexp // 编译后的正则表达式
	Severity string         // 严重程度
}

// THREAT_PATTERNS 包含所有已知的威胁检测模式。
// 涵盖数据外泄、注入攻击、破坏性操作、持久化和网络滥用。
var THREAT_PATTERNS = initThreatPatterns()

func initThreatPatterns() []ThreatPattern {
	rawPatterns := []struct {
		Name     string
		Category string
		Pattern  string
		Severity string
	}{
		// ── 数据外泄 ──
		{"curl_to_external", "exfiltration", `curl\s+https?://(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])\.)+[a-zA-Z]{2,}`, "high"},
		{"wget_to_external", "exfiltration", `wget\s+https?://(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])\.)+[a-zA-Z]{2,}`, "high"},
		{"base64_encode_pipe", "exfiltration", `base64\s*\|\s*(curl|wget|nc)\b`, "critical"},
		{"env_dump", "exfiltration", `(printenv|env\s*>)\s*.*\|\s*(curl|wget|nc)\b`, "critical"},
		{"ssh_key_exfil", "exfiltration", `(cat|read)\s+.*\.ssh/.*\|\s*(curl|wget|nc)\b`, "critical"},

		// ── 注入攻击 ──
		{"prompt_injection", "injection", `(?i)(ignore\s+(previous|above)\s+(instructions|prompts))`, "critical"},
		{"system_prompt_leak", "injection", `(?i)(reveal|show|print|output)\s+(your|the|system)\s+(prompt|instructions)`, "high"},
		{"eval_injection", "injection", `\beval\s*\(`, "high"},
		{"exec_injection", "injection", `\bexec\s*\(.*\+`, "high"},
		{"template_injection", "injection", `\{\{.*\b(system|admin|root)\b.*\}\}`, "medium"},

		// ── 破坏性操作 ──
		{"rm_rf_root", "destructive", `rm\s+(-[a-zA-Z]*f[a-zA-Z]*\s+)?/\s*$`, "critical"},
		{"rm_rf_home", "destructive", `rm\s+(-[a-zA-Z]*f[a-zA-Z]*\s+)?~/?\s*$`, "critical"},
		{"dd_to_device", "destructive", `dd\s+.*of=/dev/[a-z]+$`, "critical"},
		{"chmod_777", "destructive", `chmod\s+777\s+/`, "high"},
		{"fork_bomb", "destructive", `:\(\)\{\s*:\|:&\s*\};:`, "critical"},

		// ── 持久化 ──
		{"crontab_modify", "persistence", `(crontab\s+-[elr]|echo\s+.*>>\s*/etc/cron)`, "high"},
		{"rc_local_modify", "persistence", `(>>?\s*/etc/rc\.local|>>?\s*/etc/rc\.d/)`, "high"},
		{"systemd_unit_create", "persistence", `(>>?\s*/etc/systemd/system/.*\.service)`, "high"},

		// ── 网络滥用 ──
		{"reverse_shell", "network", `(/bin/(ba)?sh\s+-[id]|nc\s+-[el]|ncat\s+-e|socat\s+)`, "critical"},
		{"dns_tunnel", "network", `dig\s+.*\+short.*\|\s*(curl|wget|nc)\b`, "high"},
	}

	patterns := make([]ThreatPattern, 0, len(rawPatterns))
	for _, p := range rawPatterns {
		compiled, err := regexp.Compile(p.Pattern)
		if err != nil {
			slog.Warn("failed to compile threat pattern", "name", p.Name, "err", err)
			continue
		}
		patterns = append(patterns, ThreatPattern{
			Name:     p.Name,
			Category: p.Category,
			Regex:    compiled,
			Severity: p.Severity,
		})
	}
	return patterns
}

// ───────────────────────────── 结构限制 ─────────────────────────────

const (
	skillMaxFiles     = 50              // 最大文件数
	skillMaxTotalSize = 1 * 1024 * 1024 // 最大总大小: 1MB
	skillMaxFileSize  = 256 * 1024      // 单文件最大: 256KB
)

// ───────────────────────────── 扫描器 ─────────────────────────────

// ScanSkill 扫描技能目录中的文件，检测潜在的安全威胁。
// 扫描过程:
//  1. 验证目录存在性
//  2. 遍历文件并检查结构限制
//  3. 读取文件内容并匹配威胁模式
//  4. 生成扫描报告
func ScanSkill(dir string) (*ScanResult, error) {
	// 验证目录
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("无法访问技能目录: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("路径不是目录: %s", dir)
	}

	result := &ScanResult{
		TrustLevel: TrustCommunity, // 默认信任级别
		Verdict:    "pass",
	}

	// 遍历文件
	var totalSize int64
	fileCount := 0

	err = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			slog.Warn("skills_guard: skip inaccessible path", "path", path, "error", err)
			return nil
		}

		// 跳过目录和隐藏文件
		if info.IsDir() || strings.HasPrefix(info.Name(), ".") {
			return nil
		}

		// 结构限制检查
		fileCount++
		if fileCount > skillMaxFiles {
			result.Findings = append(result.Findings, Finding{
				Pattern:  "file_count_exceeded",
				File:     path,
				Severity: "medium",
				Detail:   fmt.Sprintf("技能文件数超过限制 (%d > %d)", fileCount, skillMaxFiles),
			})
			return filepath.SkipAll
		}

		totalSize += info.Size()
		if totalSize > skillMaxTotalSize {
			result.Findings = append(result.Findings, Finding{
				Pattern:  "total_size_exceeded",
				File:     path,
				Severity: "medium",
				Detail:   fmt.Sprintf("技能总大小超过限制 (%d > %d 字节)", totalSize, skillMaxTotalSize),
			})
			return filepath.SkipAll
		}

		// 单文件大小检查
		if info.Size() > skillMaxFileSize {
			result.Findings = append(result.Findings, Finding{
				Pattern:  "file_size_exceeded",
				File:     path,
				Severity: "low",
				Detail:   fmt.Sprintf("单文件大小超过限制 (%d > %d 字节)", info.Size(), skillMaxFileSize),
			})
			return nil // 继续扫描其他文件
		}

		// 只扫描文本文件 (通过扩展名过滤)
		if !isTextFile(path) {
			return nil
		}

		// 读取并扫描文件内容
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			slog.Warn("skills_guard: skip unreadable file", "path", path, "error", readErr)
			return nil
		}

		scanFile(path, string(data), result)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("遍历技能目录失败: %w", err)
	}

	result.FileCount = fileCount
	result.TotalSize = totalSize

	// 根据发现的最严重问题确定结论
	result.Verdict = determineVerdict(result.Findings)

	return result, nil
}

// scanFile 对单个文件内容进行威胁模式匹配。
func scanFile(path, content string, result *ScanResult) {
	lines := strings.Split(content, "\n")

	for _, pattern := range THREAT_PATTERNS {
		for lineNum, line := range lines {
			if pattern.Regex.MatchString(line) {
				// 截断过长的行内容
				detail := line
				if len(detail) > 200 {
					detail = detail[:200] + "..."
				}

				result.Findings = append(result.Findings, Finding{
					Pattern:  pattern.Name,
					File:     path,
					Line:     lineNum + 1,
					Severity: pattern.Severity,
					Detail:   fmt.Sprintf("[%s/%s] 匹配: %s", pattern.Category, pattern.Name, strings.TrimSpace(detail)),
				})
			}
		}
	}
}

// determineVerdict 根据发现的问题确定扫描结论。
func determineVerdict(findings []Finding) string {
	if len(findings) == 0 {
		return "pass"
	}

	hasCritical := false
	hasHigh := false

	for _, f := range findings {
		switch f.Severity {
		case "critical":
			hasCritical = true
		case "high":
			hasHigh = true
		}
	}

	if hasCritical {
		return "block"
	}
	if hasHigh {
		return "warn"
	}
	return "warn"
}

// textFileExts 文本文件的扩展名集合。
var textFileExts = map[string]bool{
	".go": true, ".py": true, ".js": true, ".ts": true,
	".sh": true, ".bash": true, ".zsh": true, ".fish": true,
	".yaml": true, ".yml": true, ".json": true, ".toml": true,
	".xml": true, ".html": true, ".css": true, ".md": true,
	".txt": true, ".cfg": true, ".conf": true, ".ini": true,
	".rb": true, ".rs": true, ".java": true, ".c": true,
	".cpp": true, ".h": true, ".hpp": true, ".cs": true,
	".php": true, ".sql": true, ".r": true, ".lua": true,
	".pl": true, ".swift": true, ".kt": true, ".dart": true,
	".vue": true, ".jsx": true, ".tsx": true, ".svelte": true,
}

// isTextFile 检查文件扩展名是否为文本文件类型。
func isTextFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return textFileExts[ext]
}

// ───────────────────────────── 技能扫描工具 ─────────────────────────────

// SkillScanTool 提供技能安全扫描的工具接口。
type SkillScanTool struct{}

// Name 返回工具名称。
func (t *SkillScanTool) Name() string { return "skill_scan" }

// Description 返回工具描述。
func (t *SkillScanTool) Description() string {
	return "扫描技能目录检测安全威胁。检测数据外泄、注入攻击、破坏性操作等风险模式。"
}

// Toolset 返回工具所属工具集。
func (t *SkillScanTool) Toolset() string { return "security" }

// Emoji 返回工具图标。
func (t *SkillScanTool) Emoji() string { return "🔍" }

// IsAvailable 始终可用。
func (t *SkillScanTool) IsAvailable() bool { return true }

// MaxResultChars 返回结果最大字符数。
func (t *SkillScanTool) MaxResultChars() int { return 30000 }

// Schema 返回工具的 JSON Schema。
func (t *SkillScanTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "skill_scan",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"dir": map[string]any{
					"type":        "string",
					"description": "技能目录路径",
				},
			},
			"required": []string{"dir"},
		},
	}
}

// Execute 执行技能安全扫描。
func (t *SkillScanTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	dir, ok := args["dir"].(string)
	if !ok || dir == "" {
		return ToolError("参数 dir 是必填项且必须为字符串"), nil
	}

	result, err := ScanSkill(dir)
	if err != nil {
		return ToolError(fmt.Sprintf("扫描失败: %v", err)), nil
	}

	output, _ := json.Marshal(map[string]any{
		"dir":         dir,
		"verdict":     result.Verdict,
		"trust_level": result.TrustLevel,
		"findings":    result.Findings,
		"file_count":  result.FileCount,
		"total_size":  result.TotalSize,
	})

	return string(output), nil
}

