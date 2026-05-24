// Package tool 提供 Jupyter Notebook 编辑工具。
// 支持对 .ipynb 文件的单元格进行替换、插入和删除操作。
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// ───────────────────────────── Notebook 数据结构 ─────────────────────────────

// notebookFile 表示 .ipynb 文件的顶层 JSON 结构。
type notebookFile struct {
	Cells          []notebookCell `json:"cells"`
	Metadata       map[string]any `json:"metadata"`
	NBFormat       int            `json:"nbformat"`
	NBFormatMinor  int            `json:"nbformat_minor"`
}

// notebookCell 表示 .ipynb 文件中的一个单元格。
type notebookCell struct {
	CellType  string         `json:"cell_type"`
	ID        string         `json:"id,omitempty"`
	Source    []string       `json:"source"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	Outputs   []any          `json:"outputs,omitempty"`
	ExecutionOrder *int      `json:"execution_count,omitempty"`
}

// ───────────────────────────── Notebook 编辑工具 ─────────────────────────────

// NotebookEditTool 实现对 Jupyter Notebook (.ipynb) 文件单元格的编辑功能。
// 支持三种模式: replace (替换)、insert (插入)、delete (删除)。
type NotebookEditTool struct{}

// Name 返回工具名称。
func (t *NotebookEditTool) Name() string { return "notebook_edit" }

// Description 返回工具描述。
func (t *NotebookEditTool) Description() string {
	return "编辑 Jupyter Notebook (.ipynb) 文件的单元格。支持替换、插入和删除操作。"
}

// Toolset 返回工具所属工具集。
func (t *NotebookEditTool) Toolset() string { return "file" }

// Emoji 返回工具图标。
func (t *NotebookEditTool) Emoji() string { return "\U0001f4d3" }

// IsAvailable Notebook 编辑始终可用。
func (t *NotebookEditTool) IsAvailable() bool { return true }

// MaxResultChars 返回结果最大字符数。
func (t *NotebookEditTool) MaxResultChars() int { return 50000 }

// Schema 返回工具的 JSON Schema。
func (t *NotebookEditTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "notebook_edit",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"notebook_path": map[string]any{
					"type":        "string",
					"description": "Jupyter Notebook 文件的绝对路径 (.ipynb)",
				},
				"cell_id": map[string]any{
					"type":        "string",
					"description": "要编辑的单元格 ID。对于 insert 模式，指定在哪个单元格之后插入。",
				},
				"cell_type": map[string]any{
					"type":        "string",
					"description": "单元格类型: code 或 markdown，默认 code。用于 insert 模式。",
					"enum":        []string{"code", "markdown"},
				},
				"edit_mode": map[string]any{
					"type":        "string",
					"description": "编辑模式: replace (默认)、insert 或 delete",
					"enum":        []string{"replace", "insert", "delete"},
				},
				"new_source": map[string]any{
					"type":        "string",
					"description": "单元格的新内容 (用于 replace 或 insert 模式)",
				},
			},
			"required": []string{"notebook_path"},
		},
	}
}

// Execute 执行 Notebook 编辑操作。
// 读取 .ipynb 文件 → 解析 JSON → 根据编辑模式操作单元格 → 写回文件。
func (t *NotebookEditTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	// 参数提取
	notebookPath, ok := args["notebook_path"].(string)
	if !ok || notebookPath == "" {
		return ToolError("参数 notebook_path 是必填项且必须为字符串"), nil
	}

	if _, err := checkPathSecurity(notebookPath, false); err != nil {
		return ToolError(fmt.Sprintf("路径不安全: %v", err)), nil
	}

	cellID, _ := args["cell_id"].(string)
	cellType := "code"
	if v, ok := args["cell_type"].(string); ok && (v == "code" || v == "markdown") {
		cellType = v
	}
	editMode := "replace"
	if v, ok := args["edit_mode"].(string); ok && (v == "replace" || v == "insert" || v == "delete") {
		editMode = v
	}
	newSource, _ := args["new_source"].(string)

	// 读取 notebook 文件
	data, err := os.ReadFile(notebookPath)
	if err != nil {
		return ToolError(fmt.Sprintf("读取 notebook 文件失败: %v", err)), nil
	}

	// 解析 JSON
	var nb notebookFile
	if err := json.Unmarshal(data, &nb); err != nil {
		return ToolError(fmt.Sprintf("解析 notebook JSON 失败: %v", err)), nil
	}

	// 根据编辑模式执行操作
	switch editMode {
	case "replace":
		if cellID == "" {
			return ToolError("replace 模式需要指定 cell_id"), nil
		}
		if newSource == "" {
			return ToolError("replace 模式需要指定 new_source"), nil
		}
		idx := findCellByID(nb.Cells, cellID)
		if idx < 0 {
			return ToolError(fmt.Sprintf("未找到 ID 为 %s 的单元格", cellID)), nil
		}
		nb.Cells[idx].Source = sourceToLines(newSource)

	case "insert":
		if newSource == "" {
			return ToolError("insert 模式需要指定 new_source"), nil
		}
		newCell := notebookCell{
			CellType: cellType,
			ID:       fmt.Sprintf("cell-%d", time.Now().UnixNano()),
			Source:   sourceToLines(newSource),
		}
		if cellID == "" {
			// 插入到开头
			nb.Cells = append([]notebookCell{newCell}, nb.Cells...)
		} else {
			idx := findCellByID(nb.Cells, cellID)
			if idx < 0 {
				return ToolError(fmt.Sprintf("未找到 ID 为 %s 的单元格", cellID)), nil
			}
			// 插入到指定单元格之后
			tail := append([]notebookCell{newCell}, nb.Cells[idx+1:]...)
			nb.Cells = append(nb.Cells[:idx+1], tail...)
		}

	case "delete":
		if cellID == "" {
			return ToolError("delete 模式需要指定 cell_id"), nil
		}
		idx := findCellByID(nb.Cells, cellID)
		if idx < 0 {
			return ToolError(fmt.Sprintf("未找到 ID 为 %s 的单元格", cellID)), nil
		}
		nb.Cells = append(nb.Cells[:idx], nb.Cells[idx+1:]...)
	}

	// 序列化并写回文件
	outData, err := json.MarshalIndent(nb, "", " ")
	if err != nil {
		return ToolError(fmt.Sprintf("序列化 notebook 失败: %v", err)), nil
	}
	if err := os.WriteFile(notebookPath, outData, 0644); err != nil {
		return ToolError(fmt.Sprintf("写回 notebook 文件失败: %v", err)), nil
	}

	result, _ := json.Marshal(map[string]any{
		"output":       fmt.Sprintf("notebook 编辑成功: %s (模式: %s)", notebookPath, editMode),
		"notebook_path": notebookPath,
		"edit_mode":    editMode,
		"cell_count":   len(nb.Cells),
	})
	return string(result), nil
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

// findCellByID 在单元格列表中查找指定 ID 的单元格，返回其索引。
// 未找到返回 -1。
func findCellByID(cells []notebookCell, id string) int {
	for i, cell := range cells {
		if cell.ID == id {
			return i
		}
	}
	return -1
}

// sourceToLines 将字符串源内容转换为 .ipynb 格式的行数组。
// 每行以 \n 结尾（最后一行除外，如果原始内容不以 \n 结尾）。
func sourceToLines(source string) []string {
	if source == "" {
		return []string{}
	}
	lines := strings.Split(source, "\n")
	result := make([]string, len(lines))
	for i, line := range lines {
		if i < len(lines)-1 {
			result[i] = line + "\n"
		} else {
			// 最后一行: 保留原样（如果为空行则跳过）
			if line == "" {
				return result[:i]
			}
			result[i] = line
		}
	}
	return result
}

// ───────────────────────────── init 注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&NotebookEditTool{})
}
