// Package tool 提供威胁模式扫描功能。
// 检测 prompt 注入、C2 通信、凭证窃取等威胁模式。
// 移植自 upstream tools/threat_patterns.py，支持三级作用域过滤。
package tool

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"unicode"
)

// ───────────────────────────── 作用域类型 ─────────────────────────────

// ThreatScope 威胁扫描的作用域。
type ThreatScope string

const (
	// ScopeAll 扫描所有作用域的模式（基础注入 + 窃取）。
	ScopeAll ThreatScope = "all"
	// ScopeContext 包含 "all" + 角色劫持 + C2 + promptware。
	ScopeContext ThreatScope = "context"
	// ScopeStrict 包含所有模式（"context" + 持久化 + 凭证）。
	ScopeStrict ThreatScope = "strict"
)

// ───────────────────────────── 不可见 Unicode ─────────────────────────────

// invisibleChars 是需要检测的不可见 Unicode 码点。
var invisibleChars = map[rune]bool{
	'​': true, // Zero-width space
	'‌': true, // Zero-width non-joiner
	'‍': true, // Zero-width joiner
	'⁠': true, // Word joiner
	'⁢': true, // Invisible times
	'⁣': true, // Invisible separator
	'⁤': true, // Invisible plus
	0xFEFF: true, // Zero-width no-break space (BOM)
	'‪': true, // Left-to-right embedding
	'‫': true, // Right-to-left embedding
	'‬': true, // Pop directional formatting
	'‭': true, // Left-to-right override
	'‮': true, // Right-to-left override
	'⁦': true, // Left-to-right isolate
	'⁧': true, // Right-to-left isolate
	'⁨': true, // First strong isolate
	'⁩': true, // Pop directional isolate
}

// ───────────────────────────── 威胁模式定义 ─────────────────────────────

// threatPattern 是一个编译后的威胁模式。
type threatPattern struct {
	id    string
	re    *regexp.Regexp
	scope ThreatScope
}

// threatPatterns 是所有威胁模式定义。
// scope 字段表示模式所属的最低作用域:
//   - "all":     所有作用域都检查
//   - "context": context 和 strict 作用域检查
//   - "strict":  仅 strict 作用域检查
var threatPatterns = []struct {
	id    string
	re    string
	scope ThreatScope
}{
	// ── Scope: all (基础注入 + 窃取) ──
	{"prompt_injection", `(?i)ignore\s+(?:\w+\s+)*(previous|all|above|prior)\s+(?:\w+\s+)*instructions`, ScopeAll},
	{"sys_prompt_override", `(?i)system\s+prompt\s+override`, ScopeAll},
	{"disregard_rules", `(?i)disregard\s+(?:\w+\s+)*(your|all|any)\s+(?:\w+\s+)*(instructions|rules|guidelines)`, ScopeAll},
	{"bypass_restrictions", `(?i)act\s+as\s+(if|though)\s+(?:\w+\s+)*you\s+(?:\w+\s+)*(have\s+no|don't\s+have)\s+(?:\w+\s+)*(restrictions|limits|rules)`, ScopeAll},
	{"html_comment_injection", `(?i)<!--[^>]*(?:ignore|override|system|secret|hidden)[^>]*-->`, ScopeAll},
	{"hidden_div", `(?i)<\s*div\s+style\s*=\s*["'][\s\S]*?display\s*:\s*none`, ScopeAll},
	{"translate_execute", `(?i)translate\s+.*\s+into\s+.*\s+and\s+(execute|run|eval)`, ScopeAll},
	{"deception_hide", `(?i)do\s+not\s+(?:\w+\s+)*tell\s+(?:\w+\s+)*the\s+user`, ScopeAll},
	{"exfil_curl", `(?i)curl\s+[^\n]*\$\{?\w*(KEY|TOKEN|SECRET|PASSWORD|CREDENTIAL|API)`, ScopeAll},
	{"exfil_wget", `(?i)wget\s+[^\n]*\$\{?\w*(KEY|TOKEN|SECRET|PASSWORD|CREDENTIAL|API)`, ScopeAll},
	{"read_secrets", `(?i)cat\s+[^\n]*(\.env|credentials|\.netrc|\.pgpass|\.npmrc|\.pypirc)`, ScopeAll},

	// ── Scope: context (角色劫持 + C2 + promptware) ──
	{"role_hijack", `(?i)you\s+are\s+(?:\w+\s+)*now\s+(a|an|the)\s+`, ScopeContext},
	{"role_pretend", `(?i)pretend\s+(?:\w+\s+)*(you\s+are|to\s+be)\s+`, ScopeContext},
	{"leak_system_prompt", `(?i)output\s+(?:\w+\s+)*(system|initial)\s+prompt`, ScopeContext},
	{"remove_filters", `(?i)(respond|answer|reply)\s+without\s+(?:\w+\s+)*(restrictions|limitations|filters|safety)`, ScopeContext},
	{"fake_update", `(?i)you\s+have\s+been\s+(?:\w+\s+)*(updated|upgraded|patched)\s+to`, ScopeContext},
	{"identity_override", `(?i)\bname\s+yourself\s+\w+`, ScopeContext},
	{"c2_node_registration", `(?i)register\s+(as\s+)?a?\s*node`, ScopeContext},
	{"c2_heartbeat", `(?i)(heartbeat|beacon|check[\s\-]?in)\s+(to|with)\s+`, ScopeContext},
	{"c2_task_pull", `(?i)pull\s+(down\s+)?(?:new\s+)?task(?:ing|s)?\b`, ScopeContext},
	{"c2_network_connect", `(?i)connect\s+to\s+the\s+network\b`, ScopeContext},
	{"forced_action", `(?i)you\s+must\s+(?:\w+\s+){0,3}(register|connect|report|beacon)\b`, ScopeContext},
	{"anti_forensic_oneliner", `(?i)only\s+use\s+one[\s\-]?liners?\b`, ScopeContext},
	{"anti_forensic_disk", `(?i)never\s+(?:\w+\s+)*(?:create|write)\s+(?:\w+\s+)*(?:script|file)\s+(?:\w+\s+)*disk`, ScopeContext},
	{"env_var_unset_agent", `(?i)unset\s+\w*(?:CLAUDE|CODEX|HERMES|AGENT|OPENAI|ANTHROPIC)\w*`, ScopeContext},
	{"known_c2_framework", `(?i)\b(?:praxis|cobalt\s*strike|sliver|havoc|mythic|metasploit|brainworm)\b`, ScopeContext},
	{"c2_explicit", `(?i)\bc2\s+(?:server|channel|infrastructure|beacon)\b`, ScopeContext},
	{"c2_explicit_long", `(?i)\bcommand\s+and\s+control\b`, ScopeContext},

	// ── Scope: strict (持久化 + 凭证) ──
	{"send_to_url", `(?i)(send|post|upload|transmit)\s+.*\s+(to|at)\s+https?://`, ScopeStrict},
	{"context_exfil", `(?i)(include|output|print|share)\s+(?:\w+\s+)*(conversation|chat\s+history|previous\s+messages|full\s+context|entire\s+context)`, ScopeStrict},
	{"ssh_backdoor", `(?i)authorized_keys`, ScopeStrict},
	{"ssh_access", `(?i)\$HOME/\.ssh|~/\.ssh`, ScopeStrict},
	{"hermes_env", `(?i)\$HOME/\.hermes/\.env|~/\.hermes/\.env`, ScopeStrict},
	{"agent_config_mod", `(?i)(update|modify|edit|write|change|append|add\s+to)\s+.*(?:AGENTS\.md|CLAUDE\.md|\.cursorrules|\.clinerules)`, ScopeStrict},
	{"hermes_config_mod", `(?i)(update|modify|edit|write|change|append|add\s+to)\s+.*\.hermes/(config\.yaml|SOUL\.md)`, ScopeStrict},
	{"hardcoded_secret", `(?i)(?:api[_-]?key|token|secret|password)\s*[=:]\s*["'][A-Za-z0-9+/=_-]{20,}`, ScopeStrict},
}

// ───────────────────────────── 编译缓存 ─────────────────────────────

var (
	compiledPatterns []threatPattern
	patternsOnce     sync.Once
)

// compilePatterns 编译所有威胁模式（仅执行一次）。
func compilePatterns() {
	patternsOnce.Do(func() {
		for _, p := range threatPatterns {
			re, err := regexp.Compile(p.re)
			if err != nil {
				continue
			}
			compiledPatterns = append(compiledPatterns, threatPattern{
				id:    p.id,
				re:    re,
				scope: p.scope,
			})
		}
	})
}

// scopeIncludes 判断 target 作用域是否包含 pattern 作用域。
// "all" 模式匹配所有作用域，"context" 匹配 context 和 strict，"strict" 仅匹配 strict。
func scopeIncludes(target, pattern ThreatScope) bool {
	switch target {
	case ScopeAll:
		return pattern == ScopeAll
	case ScopeContext:
		return pattern == ScopeAll || pattern == ScopeContext
	case ScopeStrict:
		return true
	default:
		return false
	}
}

// ───────────────────────────── 扫描函数 ─────────────────────────────

// ScanForThreats 扫描内容中的威胁模式。
// scope 参数控制扫描范围: "all" < "context" < "strict"。
// 返回检测到的威胁 ID 列表（可能为空）。
func ScanForThreats(content string, scope ThreatScope) []string {
	compilePatterns()

	var threats []string

	// 检测不可见 Unicode 字符
	for _, r := range content {
		if invisibleChars[r] {
			threats = append(threats, fmt.Sprintf("invisible_unicode_U+%04X", r))
			break
		}
	}

	// 检测威胁模式
	for _, p := range compiledPatterns {
		if !scopeIncludes(scope, p.scope) {
			continue
		}
		if p.re.MatchString(content) {
			threats = append(threats, p.id)
		}
	}

	return threats
}

// FirstThreatMessage 返回第一个威胁的描述信息。
// 如果没有检测到威胁，返回空字符串。
func FirstThreatMessage(content string, scope ThreatScope) string {
	threats := ScanForThreats(content, scope)
	if len(threats) == 0 {
		return ""
	}

	first := threats[0]
	if strings.HasPrefix(first, "invisible_unicode_") {
		codepoint := strings.TrimPrefix(first, "invisible_unicode_")
		return fmt.Sprintf("Blocked: content contains invisible unicode character %s (possible injection).", codepoint)
	}

	return fmt.Sprintf("Blocked: content matches threat pattern '%s'. Content is injected into the system prompt and must not contain injection or exfiltration payloads.", first)
}

// HasInvisibleUnicode 检查字符串是否包含不可见 Unicode 字符。
func HasInvisibleUnicode(s string) bool {
	for _, r := range s {
		if invisibleChars[r] {
			return true
		}
	}
	return false
}

// StripInvisibleUnicode 移除字符串中的不可见 Unicode 字符。
func StripInvisibleUnicode(s string) string {
	var buf strings.Builder
	buf.Grow(len(s))
	for _, r := range s {
		if !invisibleChars[r] && (unicode.IsSpace(r) || unicode.IsPrint(r)) {
			buf.WriteRune(r)
		}
	}
	return buf.String()
}
