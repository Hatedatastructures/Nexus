package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// ───────────────────────────── patch 精确替换工具 ─────────────────────────────

// PatchTool 实现文件精确文本替换功能。
// 与 file_edit 不同，允许指定替换次数。
type PatchTool struct{}

// Name 返回工具名称。
func (t *PatchTool) Name() string { return "patch" }

// Description 返回工具描述。
func (t *PatchTool) Description() string {
	return "对文件进行精确的文本替换。支持指定替换次数，适用于需要批量替换的场景。"
}

// Toolset 返回工具所属工具集。
func (t *PatchTool) Toolset() string { return "file" }

// Emoji 返回工具图标。
func (t *PatchTool) Emoji() string { return "🔧" }

// IsAvailable patch 工具始终可用。
func (t *PatchTool) IsAvailable() bool { return true }

// MaxResultChars 返回结果最大字符数。
func (t *PatchTool) MaxResultChars() int { return 1000 }

// Schema 返回工具的 JSON Schema。
func (t *PatchTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "patch",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "要编辑的文件路径",
				},
				"old_text": map[string]any{
					"type":        "string",
					"description": "要被替换的原始文本",
				},
				"new_text": map[string]any{
					"type":        "string",
					"description": "替换后的新文本 (空字符串表示删除)",
				},
				"expected_replacements": map[string]any{
					"type":        "integer",
					"description": "期望的替换次数，默认为 1。如果实际匹配次数不等于此值则拒绝执行。",
				},
			},
			"required": []string{"path", "old_text"},
		},
	}
}

// Execute 执行文件 patch 替换。
// 读取文件 → 验证匹配次数 → 执行替换 → 写回文件。
func (t *PatchTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return ToolError("参数 path 是必填项且必须为字符串"), nil
	}
	oldText, ok := args["old_text"].(string)
	if !ok || oldText == "" {
		return ToolError("参数 old_text 是必填项且必须为字符串"), nil
	}
	newText, _ := args["new_text"].(string)

	// 期望替换次数，默认 1
	expectedReplacements := 1
	if v, ok := args["expected_replacements"].(float64); ok && v > 0 {
		expectedReplacements = int(v)
	}

	// 路径安全检查: 遍历组件拒绝 + 目录边界验证 (patch 不净化路径)
	if _, secErr := checkPathSecurity(path, false); secErr != nil {
		slog.Warn("file patch blocked (path security)", "path", path, "err", secErr)
		return ToolError(fmt.Sprintf("安全限制: %v", secErr)), nil
	}

	// 安全敏感路径检查
	if isPathSensitive(path) {
		slog.Warn("file patch blocked (sensitive path)", "path", path)
		return ToolError(fmt.Sprintf("安全限制: 不允许编辑敏感路径 %s", path)), nil
	}

	// 读取文件
	data, err := os.ReadFile(path)
	if err != nil {
		slog.Warn("file read failed", "path", path, "err", err)
		return ToolError(fmt.Sprintf("读取文件失败: %v", err)), nil
	}

	content := string(data)

	// 统计匹配次数
	count := strings.Count(content, oldText)
	if count == 0 {
		return ToolError(fmt.Sprintf("old_text 在文件中不存在: %s", path)), nil
	}
	if count != expectedReplacements {
		return ToolError(fmt.Sprintf("old_text 在文件中出现 %d 次，但期望替换次数为 %d。请调整 expected_replacements 参数或提供更多上下文。", count, expectedReplacements)), nil
	}

	// 执行替换
	newContent := strings.Replace(content, oldText, newText, expectedReplacements)

	// 写回文件
	if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
		slog.Error("file patch write-back failed", "path", path, "err", err)
		return ToolError(fmt.Sprintf("写回文件失败: %v", err)), nil
	}

	slog.Info("file patch succeeded", "path", path, "replacements", expectedReplacements)
	result, err := json.Marshal(map[string]any{
		"output":       fmt.Sprintf("文件 patch 成功: %s (替换了 %d 处)", path, expectedReplacements),
		"path":         path,
		"replacements": expectedReplacements,
	})
	if err != nil {
		return ToolError(fmt.Sprintf("序列化结果失败: %v", err)), nil
	}
	return string(result), nil
}

// ───────────────────────────── diff 生成 ─────────────────────────────

// generateUnifiedDiff 生成简易的 unified diff 格式输出。
func generateUnifiedDiff(old, newContent, path string) string {
	oldLines := strings.Split(old, "\n")
	newLines := strings.Split(newContent, "\n")

	var sb strings.Builder
	sb.WriteString("--- " + path + "\n")
	sb.WriteString("+++ " + path + "\n")

	// 找到不同的行范围
	maxLen := len(oldLines)
	if len(newLines) > maxLen {
		maxLen = len(newLines)
	}

	for i := 0; i < maxLen; i++ {
		var oldLine, newLine string
		if i < len(oldLines) {
			oldLine = oldLines[i]
		}
		if i < len(newLines) {
			newLine = newLines[i]
		}
		if oldLine != newLine {
			if oldLine != "" {
				sb.WriteString(fmt.Sprintf("-%d: %s\n", i+1, oldLine))
			}
			if newLine != "" {
				sb.WriteString(fmt.Sprintf("+%d: %s\n", i+1, newLine))
			}
		}
	}

	return sb.String()
}
