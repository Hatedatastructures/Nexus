// Package tool 提供多文件补丁解析和应用功能。
// 支持 XML-like patch 格式的解析和模糊匹配应用。
package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"nexus-agent/internal/sandbox"
)

// ───────────────────────────── 路径验证 ─────────────────────────────

// validatePatchPath 验证补丁操作中的路径安全性。
// 拒绝包含 ".." 的路径和绝对路径。
func validatePatchPath(path string) error {
	if path == "" {
		return fmt.Errorf("路径不能为空")
	}
	if strings.Contains(path, "..") {
		return fmt.Errorf("路径不允许包含 \"..\": %s", path)
	}
	if filepath.IsAbs(path) {
		return fmt.Errorf("不允许使用绝对路径: %s", path)
	}
	return nil
}

// ───────────────────────────── 数据结构 ─────────────────────────────

// OperationType 补丁操作类型
type OperationType string

const (
	OpAdd    OperationType = "ADD"    // 新增文件
	OpUpdate OperationType = "UPDATE" // 更新文件内容
	OpDelete OperationType = "DELETE" // 删除文件
	OpMove   OperationType = "MOVE"   // 移动/重命名文件
)

// PatchOperation 表示单个补丁操作。
type PatchOperation struct {
	Type        OperationType // 操作类型
	FilePath    string        // 目标文件路径
	OldText     string        // 旧文本 (UPDATE 时用于定位)
	NewText     string        // 新文本 (ADD/UPDATE 时使用)
	TargetPath  string        // 目标路径 (MOVE 时使用)
	CreateDirs  bool          // 是否自动创建目录
	Overwrite   bool          // 是否覆盖已存在文件
	ExpectedReplacements int // 预期替换次数 (0 = 不限)
}

// ───────────────────────────── 解析函数 ─────────────────────────────

var (
	patchBlockRe  = regexp.MustCompile(`(?s)<patch>(.*?)</patch>`)
	filePathRe    = regexp.MustCompile(`(?i)<file_path>\s*(.*?)\s*</file_path>`)
	oldTextRe     = regexp.MustCompile(`(?s)(?i)<old_text>(.*?)</old_text>`)
	newTextRe     = regexp.MustCompile(`(?s)(?i)<new_text>(.*?)</new_text>`)
	operationRe   = regexp.MustCompile(`(?i)<operation>\s*(.*?)\s*</operation>`)
	targetPathRe  = regexp.MustCompile(`(?i)<target_path>\s*(.*?)\s*</target_path>`)
	createDirsRe  = regexp.MustCompile(`(?i)<create_dirs>\s*(.*?)\s*</create_dirs>`)
	overwriteRe   = regexp.MustCompile(`(?i)<overwrite>\s*(.*?)\s*</overwrite>`)
	expectedRe    = regexp.MustCompile(`(?i)<expected_replacements>\s*(.*?)\s*</expected_replacements>`)
)

// ParsePatchOperations 从 XML-like 格式的补丁内容中解析操作列表。
func ParsePatchOperations(content string) ([]PatchOperation, error) {
	blocks := patchBlockRe.FindAllStringSubmatch(content, -1)
	if len(blocks) == 0 {
		return nil, fmt.Errorf("未找到 <patch> 块")
	}

	var ops []PatchOperation
	for _, block := range blocks {
		op, err := parseSinglePatch(block[1])
		if err != nil {
			return nil, fmt.Errorf("解析 patch 块失败: %w", err)
		}
		ops = append(ops, op)
	}

	return ops, nil
}

func parseSinglePatch(content string) (PatchOperation, error) {
	op := PatchOperation{
		Type:      OpUpdate,
		Overwrite: true,
	}

	// 操作类型
	if m := operationRe.FindStringSubmatch(content); m != nil {
		op.Type = OperationType(strings.ToUpper(strings.TrimSpace(m[1])))
	}

	// 文件路径
	if m := filePathRe.FindStringSubmatch(content); m != nil {
		op.FilePath = strings.TrimSpace(m[1])
	} else {
		return op, fmt.Errorf("缺少 <file_path>")
	}

	// 旧文本
	if m := oldTextRe.FindStringSubmatch(content); m != nil {
		op.OldText = m[1]
	}

	// 新文本
	if m := newTextRe.FindStringSubmatch(content); m != nil {
		op.NewText = m[1]
	}

	// 目标路径 (MOVE)
	if m := targetPathRe.FindStringSubmatch(content); m != nil {
		op.TargetPath = strings.TrimSpace(m[1])
	}

	// 创建目录
	if m := createDirsRe.FindStringSubmatch(content); m != nil {
		op.CreateDirs = strings.TrimSpace(m[1]) == "true"
	}

	// 覆盖
	if m := overwriteRe.FindStringSubmatch(content); m != nil {
		op.Overwrite = strings.TrimSpace(m[1]) == "true"
	}

	// 预期替换次数
	if m := expectedRe.FindStringSubmatch(content); m != nil {
		fmt.Sscanf(strings.TrimSpace(m[1]), "%d", &op.ExpectedReplacements)
	}

	return op, nil
}

// ───────────────────────────── 沙箱路径解析 ─────────────────────────────

// resolvePatchPath 当 env 非 nil 时，将相对路径约束到沙箱工作目录。
func resolvePatchPath(path string, env sandbox.Environment) string {
	if env != nil && env.CWD() != "" {
		return filepath.Join(env.CWD(), path)
	}
	return path
}

// ───────────────────────────── 应用函数 ─────────────────────────────

// ApplyOperations 将补丁操作应用到文件系统。
// env 非 nil 时通过沙箱工作目录约束路径，否则直接操作本地文件系统。
func ApplyOperations(ops []PatchOperation, env sandbox.Environment) error {
	for _, op := range ops {
		// 验证路径安全性
		if err := validatePatchPath(op.FilePath); err != nil {
			return fmt.Errorf("路径验证失败: %w", err)
		}
		if op.Type == OpMove {
			if err := validatePatchPath(op.TargetPath); err != nil {
				return fmt.Errorf("目标路径验证失败: %w", err)
			}
		}
		if err := applyOperation(op, env); err != nil {
			return fmt.Errorf("应用操作 %s %s 失败: %w", op.Type, op.FilePath, err)
		}
	}
	return nil
}

func applyOperation(op PatchOperation, env sandbox.Environment) error {
	switch op.Type {
	case OpAdd:
		return applyAdd(op, env)
	case OpUpdate:
		return applyUpdate(op, env)
	case OpDelete:
		return applyDelete(op, env)
	case OpMove:
		return applyMove(op, env)
	default:
		return fmt.Errorf("未知操作类型: %s", op.Type)
	}
}

func applyAdd(op PatchOperation, env sandbox.Environment) error {
	fp := resolvePatchPath(op.FilePath, env)
	if op.CreateDirs {
		dir := filepath.Dir(fp)
		if dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return fmt.Errorf("创建目录失败: %w", err)
			}
		}
	}

	if !op.Overwrite {
		if _, err := os.Stat(fp); err == nil {
			return fmt.Errorf("文件已存在且未设置 overwrite")
		}
	}

	return os.WriteFile(fp, []byte(op.NewText), 0644)
}

func applyUpdate(op PatchOperation, env sandbox.Environment) error {
	fp := resolvePatchPath(op.FilePath, env)
	data, err := os.ReadFile(fp)
	if err != nil {
		return fmt.Errorf("读取文件失败: %w", err)
	}

	content := string(data)

	if op.OldText == "" {
		// 无旧文本: 替换整个文件
		return os.WriteFile(fp, []byte(op.NewText), 0644)
	}

	// 模糊匹配替换
	replaced := fuzzyReplace(content, op.OldText, op.NewText, op.ExpectedReplacements)
	if replaced == content {
		return fmt.Errorf("未找到匹配的旧文本")
	}

	return os.WriteFile(fp, []byte(replaced), 0644)
}

func applyDelete(op PatchOperation, env sandbox.Environment) error {
	return os.Remove(resolvePatchPath(op.FilePath, env))
}

func applyMove(op PatchOperation, env sandbox.Environment) error {
	if op.TargetPath == "" {
		return fmt.Errorf("MOVE 操作缺少 target_path")
	}
	return os.Rename(resolvePatchPath(op.FilePath, env), resolvePatchPath(op.TargetPath, env))
}

// ───────────────────────────── 模糊匹配 ─────────────────────────────

// fuzzyReplace 执行模糊匹配替换。
// 先尝试精确匹配，失败后尝试忽略首尾空白的匹配。
func fuzzyReplace(content, oldText, newText string, maxReplacements int) string {
	// 1. 精确匹配
	if strings.Contains(content, oldText) {
		if maxReplacements > 0 {
			count := 0
			result := content
			for {
				idx := strings.Index(result, oldText)
				if idx == -1 || count >= maxReplacements {
					break
				}
				result = result[:idx] + newText + result[idx+len(oldText):]
				count++
			}
			return result
		}
		return strings.ReplaceAll(content, oldText, newText)
	}

	// 2. 忽略首尾空白匹配
	trimmedOld := strings.TrimSpace(oldText)
	if trimmedOld != "" && strings.Contains(content, trimmedOld) {
		if maxReplacements > 0 {
			count := 0
			result := content
			for {
				idx := strings.Index(result, trimmedOld)
				if idx == -1 || count >= maxReplacements {
					break
				}
				result = result[:idx] + newText + result[idx+len(trimmedOld):]
				count++
			}
			return result
		}
		return strings.ReplaceAll(content, trimmedOld, newText)
	}

	// 3. 逐行模糊匹配
	lines := strings.Split(content, "\n")
	oldLines := strings.Split(oldText, "\n")

	if len(oldLines) > 1 {
		// 多行匹配: 查找连续行序列
		for i := 0; i <= len(lines)-len(oldLines); i++ {
			match := true
			for j, oldLine := range oldLines {
				if strings.TrimSpace(lines[i+j]) != strings.TrimSpace(oldLine) {
					match = false
					break
				}
			}
			if match {
				newLines := strings.Split(newText, "\n")
				result := make([]string, 0, len(lines)-len(oldLines)+len(newLines))
				result = append(result, lines[:i]...)
				result = append(result, newLines...)
				result = append(result, lines[i+len(oldLines):]...)
				return strings.Join(result, "\n")
			}
		}
	}

	return content // 未匹配，返回原内容
}
