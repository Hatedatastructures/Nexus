// Package memory 提供内置记忆工具调用的处理和辅助函数。
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"nexus-agent/internal/llm"

	pkgerrors "nexus-agent/internal/errors"
)

// ───────────────────────────── 工具调用处理 ─────────────────────────────

// GetToolSchemas 返回记忆工具的 JSON Schema 列表。
func (p *BuiltinProvider) GetToolSchemas() []llm.ToolSchema {
	return []llm.ToolSchema{
		{
			Name: "memory",
			Description: `保存持久信息到跨会话的记忆存储中。
记忆会被注入到未来的对话回合中，请保持内容精简，聚焦于长期有用的信息。

主动保存时机:
- 用户纠正你或说"记住这个"/"不要再这样做"
- 用户分享偏好、习惯或个人信息 (姓名、角色、时区、编码风格)
- 你发现了环境信息 (操作系统、已安装工具、项目结构)
- 你学到了特定于此用户设置的约定、API 特性或工作流程
- 你发现了一个在将来的会话中也会有用的稳定事实

两种目标:
- 'memory': 你的个人笔记 — 环境事实、项目约定、工具特性、经验教训
- 'user': 用户画像 — 姓名、角色、偏好、沟通风格

操作: add (新增条目), replace (更新 — 用 old_text 定位), remove (删除 — 用 old_text 定位)`,
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{
						"type":        "string",
						"enum":        []string{"add", "replace", "remove"},
						"description": "要执行的操作类型",
					},
					"target": map[string]any{
						"type":        "string",
						"enum":        []string{"memory", "user"},
						"description": "目标存储: 'memory' 为个人笔记, 'user' 为用户画像",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "条目内容。'add' 和 'replace' 操作必需。",
					},
					"old_text": map[string]any{
						"type":        "string",
						"description": "用于定位要替换或删除的条目的短唯一子字符串",
					},
				},
				"required": []string{"action", "target"},
			},
		},
	}
}

// HandleToolCall 处理模型发起的记忆工具调用。
// 支持 read/add/replace/remove 操作。
func (p *BuiltinProvider) HandleToolCall(ctx context.Context, toolName string, args map[string]any) (string, error) {
	if toolName != "memory" {
		return "", pkgerrors.New(pkgerrors.MemoryProvider, fmt.Sprintf("内置记忆: 未知工具 '%s'", toolName))
	}

	action, _ := args["action"].(string)
	target, _ := args["target"].(string)
	content, _ := args["content"].(string)
	oldText, _ := args["old_text"].(string)

	if target != "memory" && target != "user" {
		return toolError("无效目标 '" + target + "'。请使用 'memory' 或 'user'。"), nil
	}

	switch action {
	case "add":
		if content == "" {
			return toolError("'add' 操作需要 content 参数。"), nil
		}
		return p.add(ctx, target, content)

	case "replace":
		if oldText == "" {
			return toolError("'replace' 操作需要 old_text 参数。"), nil
		}
		if content == "" {
			return toolError("'replace' 操作需要 content 参数。"), nil
		}
		return p.replace(ctx, target, oldText, content)

	case "remove":
		if oldText == "" {
			return toolError("'remove' 操作需要 old_text 参数。"), nil
		}
		return p.remove(ctx, target, oldText)

	default:
		return toolError("未知操作 '" + action + "'。可用操作: add, replace, remove。"), nil
	}
}

// ───────────────────────────── CRUD 操作 ─────────────────────────────

// add 向指定目标追加新条目。
func (p *BuiltinProvider) add(ctx context.Context, target, content string) (string, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return toolError("内容不能为空。"), nil
	}

	// 威胁模式扫描: 阻止注入和窃取载荷
	if msg := scanMemoryThreat(content); msg != "" {
		return toolError(msg), nil
	}

	path := p.filePath(target)
	lockPath := p.lockPath(target)

	// 获取文件锁
	unlock, err := p.acquireLock(lockPath)
	if err != nil {
		return toolError("文件锁获取失败: " + err.Error()), nil
	}
	defer unlock()

	// 在锁内重新读取以确保获取最新状态
	entries := p.readEntries(filepath.Base(path))

	// 拒绝精确重复
	for _, e := range entries {
		if e == content {
			return toolSuccess(target, entries, "条目已存在 (未添加重复项)。"), nil
		}
	}

	// 检查字符限制
	limit := p.charLimit(target)
	newEntries := append(entries, content)
	newTotal := len(strings.Join(newEntries, entryDelimiter))

	if newTotal > limit {
		current := 0
		if len(entries) > 0 {
			current = len(strings.Join(entries, entryDelimiter))
		}
		pct := min(100, current*100/limit)
		return jsonMarshal(map[string]any{
			"success": false,
			"error": fmt.Sprintf(
				"记忆空间不足: %d/%d 字符 (%d%%)。添加此条目 (%d 字符) 将超出限制。请先替换或删除现有条目。",
				current, limit, pct, len(content),
			),
			"current_entries": entries,
			"usage":           fmt.Sprintf("%d/%d", current, limit),
		}), nil
	}

	entries = append(entries, content)
	if err := p.writeEntries(path, entries); err != nil {
		return toolError("写入文件失败: " + err.Error()), nil
	}

	// 更新内存缓存
	p.mu.Lock()
	p.setEntries(target, entries)
	p.mu.Unlock()

	slog.Info("builtin memory: entry added", "target", target, "entries", len(entries))
	return toolSuccess(target, entries, "条目已添加。"), nil
}

// replace 替换匹配的旧文本所在条目为新内容。
func (p *BuiltinProvider) replace(ctx context.Context, target, oldText, newContent string) (string, error) {
	oldText = strings.TrimSpace(oldText)
	newContent = strings.TrimSpace(newContent)

	if oldText == "" {
		return toolError("old_text 不能为空。"), nil
	}
	if newContent == "" {
		return toolError("new_content 不能为空。请使用 'remove' 操作来删除条目。"), nil
	}

	// 威胁模式扫描: 阻止注入和窃取载荷
	if msg := scanMemoryThreat(newContent); msg != "" {
		return toolError(msg), nil
	}

	path := p.filePath(target)
	lockPath := p.lockPath(target)

	unlock, err := p.acquireLock(lockPath)
	if err != nil {
		return toolError("文件锁获取失败: " + err.Error()), nil
	}
	defer unlock()

	entries := p.readEntries(filepath.Base(path))

	// 查找匹配项
	matches := p.findEntries(entries, oldText)
	if len(matches) == 0 {
		return toolError(fmt.Sprintf("未找到匹配 '%s' 的条目。", oldText)), nil
	}

	if len(matches) > 1 {
		// 检查是否所有匹配都是完全相同的重复条目
		uniqueTexts := make(map[string]struct{})
		for _, idx := range matches {
			uniqueTexts[entries[idx]] = struct{}{}
		}
		if len(uniqueTexts) > 1 {
			var previews []string
			for _, idx := range matches {
				e := entries[idx]
				if len(e) > 80 {
					previews = append(previews, e[:80]+"...")
				} else {
					previews = append(previews, e)
				}
			}
			return jsonMarshal(map[string]any{
				"success": false,
				"error":   fmt.Sprintf("有多个条目匹配 '%s'。请提供更具体的匹配文本。", oldText),
				"matches": previews,
			}), nil
		}
	}

	idx := matches[0]
	limit := p.charLimit(target)

	// 检查替换后是否超出限制
	testEntries := make([]string, len(entries))
	copy(testEntries, entries)
	testEntries[idx] = newContent
	newTotal := len(strings.Join(testEntries, entryDelimiter))
	if newTotal > limit {
		return toolError(fmt.Sprintf(
			"替换后将超出 %d 字符限制。请缩短新内容或先删除其他条目。",
			limit,
		)), nil
	}

	entries[idx] = newContent
	if err := p.writeEntries(path, entries); err != nil {
		return toolError("写入文件失败: " + err.Error()), nil
	}

	p.mu.Lock()
	p.setEntries(target, entries)
	p.mu.Unlock()

	slog.Info("builtin memory: entry replaced", "target", target)
	return toolSuccess(target, entries, "条目已替换。"), nil
}

// remove 删除匹配的条目。
func (p *BuiltinProvider) remove(ctx context.Context, target, oldText string) (string, error) {
	oldText = strings.TrimSpace(oldText)
	if oldText == "" {
		return toolError("old_text 不能为空。"), nil
	}

	path := p.filePath(target)
	lockPath := p.lockPath(target)

	unlock, err := p.acquireLock(lockPath)
	if err != nil {
		return toolError("文件锁获取失败: " + err.Error()), nil
	}
	defer unlock()

	entries := p.readEntries(filepath.Base(path))

	matches := p.findEntries(entries, oldText)
	if len(matches) == 0 {
		return toolError(fmt.Sprintf("未找到匹配 '%s' 的条目。", oldText)), nil
	}

	if len(matches) > 1 {
		uniqueTexts := make(map[string]struct{})
		for _, idx := range matches {
			uniqueTexts[entries[idx]] = struct{}{}
		}
		if len(uniqueTexts) > 1 {
			var previews []string
			for _, idx := range matches {
				e := entries[idx]
				if len(e) > 80 {
					previews = append(previews, e[:80]+"...")
				} else {
					previews = append(previews, e)
				}
			}
			return jsonMarshal(map[string]any{
				"success": false,
				"error":   fmt.Sprintf("有多个条目匹配 '%s'。请提供更具体的匹配文本。", oldText),
				"matches": previews,
			}), nil
		}
	}

	idx := matches[0]
	entries = append(entries[:idx], entries[idx+1:]...)
	if err := p.writeEntries(path, entries); err != nil {
		return toolError("写入文件失败: " + err.Error()), nil
	}

	p.mu.Lock()
	p.setEntries(target, entries)
	p.mu.Unlock()

	slog.Info("builtin memory: entry removed", "target", target)
	return toolSuccess(target, entries, "条目已删除。"), nil
}

// ───────────────────────────── 工具辅助函数 ─────────────────────────────

// findEntries 返回包含子字符串的所有条目索引。
func (p *BuiltinProvider) findEntries(entries []string, sub string) []int {
	var indices []int
	for i, e := range entries {
		if strings.Contains(e, sub) {
			indices = append(indices, i)
		}
	}
	return indices
}

// toolError 构造 JSON 格式的工具错误响应。
func toolError(message string) string {
	return jsonMarshal(map[string]any{
		"success": false,
		"error":   message,
	})
}

// toolSuccess 构造 JSON 格式的工具成功响应。
func toolSuccess(target string, entries []string, message string) string {
	current := 0
	if len(entries) > 0 {
		current = len(strings.Join(entries, entryDelimiter))
	}
	limit := memoryCharLimit
	if target == "user" {
		limit = userCharLimit
	}
	pct := min(100, current*100/limit)

	resp := map[string]any{
		"success":     true,
		"target":      target,
		"entries":     entries,
		"usage":       fmt.Sprintf("%d%% — %d/%d 字符", pct, current, limit),
		"entry_count": len(entries),
	}
	if message != "" {
		resp["message"] = message
	}
	return jsonMarshal(resp)
}

// jsonMarshal 将值序列化为 JSON 字符串 (紧凑格式)。
func jsonMarshal(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf(`{"success":false,"error":"JSON 序列化失败: %v"}`, err)
	}
	return string(data)
}
