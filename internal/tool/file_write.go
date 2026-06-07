package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// ───────────────────────────── 文件写入工具 ─────────────────────────────

// FileWriteTool 实现文件写入功能。
type FileWriteTool struct{}

// Name 返回工具名称。
func (t *FileWriteTool) Name() string { return "file_write" }

// Description 返回工具描述。
func (t *FileWriteTool) Description() string {
	return "将内容写入文件。如果文件已存在则覆盖。"
}

// Toolset 返回工具所属工具集。
func (t *FileWriteTool) Toolset() string { return "file" }

// Emoji 返回工具图标。
func (t *FileWriteTool) Emoji() string { return "✏️" }

// IsAvailable 文件写入始终可用。
func (t *FileWriteTool) IsAvailable() bool { return true }

// MaxResultChars 返回结果最大字符数。
func (t *FileWriteTool) MaxResultChars() int { return 1000 }

// Schema 返回工具的 JSON Schema。
func (t *FileWriteTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "file_write",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "目标文件路径 (绝对路径或相对路径)",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "要写入的文件内容",
				},
			},
			"required": []string{"path", "content"},
		},
	}
}

// Execute 执行文件写入。
// 检查敏感路径 → 确保目录存在 → 写入文件。
func (t *FileWriteTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return ToolError("参数 path 是必填项且必须为字符串"), nil
	}
	content, ok := args["content"].(string)
	if !ok {
		return ToolError("参数 content 是必填项且必须为字符串"), nil
	}

	// 路径安全检查: 遍历组件拒绝 + 目录边界验证 (写入不净化路径)
	if _, secErr := checkPathSecurity(path, false); secErr != nil {
		slog.Warn("file write blocked (path security)", "path", path, "err", secErr)
		return ToolError(fmt.Sprintf("安全限制: %v", secErr)), nil
	}

	// 安全敏感路径检查
	if isPathSensitive(path) {
		slog.Warn("file write blocked (sensitive path)", "path", path)
		return ToolError(fmt.Sprintf("安全限制: 不允许写入敏感路径 %s", path)), nil
	}

	// 确保父目录存在
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		slog.Error("failed to create directory", "dir", dir, "err", err)
		return ToolError(fmt.Sprintf("创建目录失败: %v", err)), nil
	}

	// 写入文件
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		slog.Error("file write failed", "path", path, "err", err)
		return ToolError(fmt.Sprintf("写入文件失败: %v", err)), nil
	}

	slog.Info("file written successfully", "path", path, "size", len(content))
	result, err := json.Marshal(map[string]any{
		"output":  fmt.Sprintf("文件写入成功: %s (%d 字节)", path, len(content)),
		"path":    path,
		"size":    len(content),
	})
	if err != nil {
		return ToolError(fmt.Sprintf("序列化结果失败: %v", err)), nil
	}
	return string(result), nil
}

// ───────────────────────────── 文件编辑工具 ─────────────────────────────

// FileEditTool 实现文件编辑功能 (基于文本替换)。
type FileEditTool struct{}

// Name 返回工具名称。
func (t *FileEditTool) Name() string { return "file_edit" }

// Description 返回工具描述。
func (t *FileEditTool) Description() string {
	return "替换文件中的指定文本。old_text 必须在文件中唯一匹配。"
}

// Toolset 返回工具所属工具集。
func (t *FileEditTool) Toolset() string { return "file" }

// Emoji 返回工具图标。
func (t *FileEditTool) Emoji() string { return "📝" }

// IsAvailable 文件编辑始终可用。
func (t *FileEditTool) IsAvailable() bool { return true }

// MaxResultChars 返回结果最大字符数。
func (t *FileEditTool) MaxResultChars() int { return 1000 }

// Schema 返回工具的 JSON Schema。
func (t *FileEditTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "file_edit",
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
					"description": "要被替换的原始文本 (必须在文件中唯一存在)",
				},
				"new_text": map[string]any{
					"type":        "string",
					"description": "替换后的新文本 (空字符串表示删除)",
				},
			},
			"required": []string{"path", "old_text"},
		},
	}
}

// Execute 执行文件编辑。
// 读取文件 → 查找替换 → 写回文件。
func (t *FileEditTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return ToolError("参数 path 是必填项且必须为字符串"), nil
	}
	oldText, ok := args["old_text"].(string)
	if !ok || oldText == "" {
		return ToolError("参数 old_text 是必填项且必须为字符串"), nil
	}
	newText, _ := args["new_text"].(string)

	// 路径安全检查: 遍历组件拒绝 + 目录边界验证 (编辑不净化路径)
	if _, secErr := checkPathSecurity(path, false); secErr != nil {
		slog.Warn("file edit blocked (path security)", "path", path, "err", secErr)
		return ToolError(fmt.Sprintf("安全限制: %v", secErr)), nil
	}

	// 安全敏感路径检查
	if isPathSensitive(path) {
		slog.Warn("file edit blocked (sensitive path)", "path", path)
		return ToolError(fmt.Sprintf("安全限制: 不允许编辑敏感路径 %s", path)), nil
	}

	// 读取文件
	data, err := os.ReadFile(path)
	if err != nil {
		slog.Warn("file read failed", "path", path, "err", err)
		return ToolError(fmt.Sprintf("读取文件失败: %v", err)), nil
	}

	content := string(data)

	// 查找并替换
	count := strings.Count(content, oldText)
	if count == 0 {
		return ToolError(fmt.Sprintf("old_text 在文件中不存在: %s", path)), nil
	}
	if count > 1 {
		return ToolError(fmt.Sprintf("old_text 在文件中出现 %d 次，无法唯一匹配。请提供更多上下文以确保唯一性。", count)), nil
	}

	newContent := strings.Replace(content, oldText, newText, 1)

	// 写回文件
	if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
		slog.Error("file edit write-back failed", "path", path, "err", err)
		return ToolError(fmt.Sprintf("写回文件失败: %v", err)), nil
	}

	slog.Info("file edit succeeded", "path", path)
	result, err := json.Marshal(map[string]any{
		"output": fmt.Sprintf("文件编辑成功: %s (替换了 %d 处)", path, 1),
		"path":   path,
		"diff":   generateUnifiedDiff(content, newContent, path),
	})
	if err != nil {
		return ToolError(fmt.Sprintf("序列化结果失败: %v", err)), nil
	}
	return string(result), nil
}
