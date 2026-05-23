// Package tool 提供影子 Git 检查点管理功能。
// 在隔离的影子仓库中维护操作检查点，支持创建、列表、差异比较和恢复。
// 使用 Git 环境隔离确保不影响用户全局 Git 配置。
package tool

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// ───────────────────────────── 检查点管理器 ─────────────────────────────

// CheckpointManager 管理影子 Git 检查点仓库。
// 每个受管理的目录在 ~/.nexus/checkpoints/ 下有对应的影子仓库。
type CheckpointManager struct {
	BaseDir string // 影子仓库根目录 (默认 ~/.nexus/checkpoints)
}

// NewCheckpointManager 创建检查点管理器。
// 如果 baseDir 为空，使用默认路径 ~/.nexus/checkpoints。
func NewCheckpointManager(baseDir string) *CheckpointManager {
	if baseDir == "" {
		home, _ := os.UserHomeDir()
		if home != "" {
			baseDir = filepath.Join(home, ".nexus", "checkpoints")
		}
	}
	return &CheckpointManager{BaseDir: baseDir}
}

// ───────────────────────────── Git 环境隔离 ─────────────────────────────

// gitEnv 返回隔离的 Git 环境变量。
// 设置 GIT_CONFIG_GLOBAL=/dev/null 防止读取用户全局配置。
// 设置 GIT_CONFIG_NOSYSTEM=1 防止读取系统级配置。
func gitEnv() []string {
	env := os.Environ()
	env = append(env,
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
	)
	return env
}

// ───────────────────────────── 提交哈希验证 ─────────────────────────────

// validCommitHash 匹配合法的 Git 提交哈希 (短格式或长格式)。
var validCommitHash = regexp.MustCompile(`^[0-9a-fA-F]{4,40}$`)

// validateCommitHash 验证提交哈希是否合法，防止命令注入。
func validateCommitHash(hash string) error {
	if hash == "" {
		return fmt.Errorf("提交哈希不能为空")
	}
	if !validCommitHash.MatchString(hash) {
		return fmt.Errorf("提交哈希包含非法字符: %q", hash)
	}
	return nil
}

// ───────────────────────────── 影子仓库路径 ─────────────────────────────

// shadowRepoPath 计算目录对应的影子仓库路径。
// 将源路径转换为安全的目录名 (使用路径哈希避免冲突)。
func (m *CheckpointManager) shadowRepoPath(dir string) string {
	// 规范化路径
	absDir, err := filepath.Abs(dir)
	if err != nil {
		absDir = dir
	}

	// 使用路径的 Base 名 + 路径哈希作为影子仓库名
	// 这样可以区分同名但不同位置的目录
	safeName := sanitizeRepoName(absDir)
	return filepath.Join(m.BaseDir, safeName)
}

// sanitizeRepoName 将路径转换为安全的仓库目录名。
func sanitizeRepoName(path string) string {
	// 替换路径分隔符和其他特殊字符
	replacer := strings.NewReplacer(
		":", "_",
		"/", "_",
		`\`, "_",
		" ", "_",
		"..", "_",
	)
	name := replacer.Replace(path)

	// 限制长度
	if len(name) > 100 {
		name = name[:100]
	}

	return name
}

// ───────────────────────────── 检查点操作 ─────────────────────────────

// EnsureCheckpoint 确保目录有对应的影子 Git 仓库，并创建一个新的检查点。
// 如果影子仓库不存在，会自动初始化。然后暂存所有变更并提交。
func (m *CheckpointManager) EnsureCheckpoint(dir string) error {
	// 验证源目录存在
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("源目录不存在: %w", err)
	}

	// 确保基础目录存在
	if err := os.MkdirAll(m.BaseDir, 0755); err != nil {
		return fmt.Errorf("创建检查点基础目录失败: %w", err)
	}

	shadowDir := m.shadowRepoPath(dir)

	// 如果影子仓库不存在，初始化
	if _, err := os.Stat(filepath.Join(shadowDir, ".git")); os.IsNotExist(err) {
		if err := m.initShadowRepo(dir, shadowDir); err != nil {
			return fmt.Errorf("初始化影子仓库失败: %w", err)
		}
	}

	// 同步文件到影子仓库
	if err := m.syncToShadow(dir, shadowDir); err != nil {
		return fmt.Errorf("同步文件失败: %w", err)
	}

	// 检查是否有变更
	hasChanges, err := m.hasChanges(shadowDir)
	if err != nil {
		return fmt.Errorf("检查变更失败: %w", err)
	}

	if !hasChanges {
		slog.Debug("checkpoint: no changes, skipping commit", "dir", dir)
		return nil
	}

	// 暂存并提交
	if err := m.commitCheckpoint(shadowDir); err != nil {
		return fmt.Errorf("提交检查点失败: %w", err)
	}

	slog.Info("checkpoint created", "dir", dir)
	return nil
}

// initShadowRepo 初始化影子 Git 仓库。
func (m *CheckpointManager) initShadowRepo(srcDir, shadowDir string) error {
	// 清理可能存在的旧目录
	os.RemoveAll(shadowDir)

	// 创建影子仓库目录
	if err := os.MkdirAll(shadowDir, 0755); err != nil {
		return err
	}

	// git init
	cmd := exec.Command("git", "init", shadowDir)
	cmd.Env = gitEnv()
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git init 失败: %s (%w)", string(output), err)
	}

	// 初始提交 (空仓库需要至少一个提交)
	cmd = exec.Command("git", "-C", shadowDir, "commit", "--allow-empty", "-m", "初始检查点")
	cmd.Env = gitEnv()
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("初始提交失败: %s (%w)", string(output), err)
	}

	return nil
}

// syncToShadow 将源目录的文件同步到影子仓库。
func (m *CheckpointManager) syncToShadow(srcDir, shadowDir string) error {
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // 跳过无法访问的文件
		}

		// 跳过隐藏目录
		if info.IsDir() && strings.HasPrefix(info.Name(), ".") && path != srcDir {
			return filepath.SkipDir
		}

		// 计算相对路径
		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return nil
		}

		destPath := filepath.Join(shadowDir, relPath)

		if info.IsDir() {
			return os.MkdirAll(destPath, info.Mode())
		}

		// 复制文件
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}

		// 确保目标目录存在
		destDir := filepath.Dir(destPath)
		if err := os.MkdirAll(destDir, 0755); err != nil {
			return nil
		}

		return os.WriteFile(destPath, data, info.Mode())
	})
}

// hasChanges 检查影子仓库是否有未提交的变更。
func (m *CheckpointManager) hasChanges(shadowDir string) (bool, error) {
	cmd := exec.Command("git", "-C", shadowDir, "status", "--porcelain")
	cmd.Env = gitEnv()
	output, err := cmd.Output()
	if err != nil {
		return false, err
	}
	return len(strings.TrimSpace(string(output))) > 0, nil
}

// commitCheckpoint 暂存所有变更并创建提交。
func (m *CheckpointManager) commitCheckpoint(shadowDir string) error {
	// git add -A
	cmd := exec.Command("git", "-C", shadowDir, "add", "-A")
	cmd.Env = gitEnv()
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git add 失败: %s (%w)", string(output), err)
	}

	// git commit
	cmd = exec.Command("git", "-C", shadowDir, "commit", "-m", "自动检查点")
	cmd.Env = gitEnv()
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit 失败: %s (%w)", string(output), err)
	}

	return nil
}

// ListCheckpoints 列出目录的所有检查点提交。
// 返回按时间倒序排列的提交哈希列表。
func (m *CheckpointManager) ListCheckpoints(dir string) ([]string, error) {
	shadowDir := m.shadowRepoPath(dir)

	// 检查影子仓库是否存在
	if _, err := os.Stat(filepath.Join(shadowDir, ".git")); os.IsNotExist(err) {
		return []string{}, nil
	}

	cmd := exec.Command("git", "-C", shadowDir, "log", "--format=%H", "--all")
	cmd.Env = gitEnv()
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("获取检查点列表失败: %w", err)
	}

	hashes := strings.Split(strings.TrimSpace(string(output)), "\n")
	// 过滤空行
	var result []string
	for _, h := range hashes {
		h = strings.TrimSpace(h)
		if h != "" {
			result = append(result, h)
		}
	}

	return result, nil
}

// Diff 返回两个检查点之间的差异。
// 如果 from 为空，比较第一个提交和 to。
// 如果 to 为空，比较 from 和当前工作树。
func (m *CheckpointManager) Diff(dir string, from, to string) (string, error) {
	shadowDir := m.shadowRepoPath(dir)

	// 验证提交哈希
	if from != "" {
		if err := validateCommitHash(from); err != nil {
			return "", fmt.Errorf("无效的 from 提交: %w", err)
		}
	}
	if to != "" {
		if err := validateCommitHash(to); err != nil {
			return "", fmt.Errorf("无效的 to 提交: %w", err)
		}
	}

	// 检查影子仓库是否存在
	if _, err := os.Stat(filepath.Join(shadowDir, ".git")); os.IsNotExist(err) {
		return "", fmt.Errorf("影子仓库不存在: %s", dir)
	}

	// 构建 diff 命令
	args := []string{"-C", shadowDir, "diff"}
	if from != "" && to != "" {
		args = append(args, from, to)
	} else if from != "" {
		args = append(args, from)
	} else {
		args = append(args, "HEAD")
	}

	cmd := exec.Command("git", args...)
	cmd.Env = gitEnv()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("获取差异失败: %s (%w)", string(output), err)
	}

	if len(output) == 0 {
		return "(无差异)", nil
	}

	return string(output), nil
}

// Restore 将目录恢复到指定的检查点。
// 使用 git checkout 将影子仓库恢复到指定提交，然后复制回源目录。
func (m *CheckpointManager) Restore(dir string, commitHash string) error {
	// 验证提交哈希 (防注入)
	if err := validateCommitHash(commitHash); err != nil {
		return fmt.Errorf("无效的提交哈希: %w", err)
	}

	shadowDir := m.shadowRepoPath(dir)

	// 检查影子仓库是否存在
	if _, err := os.Stat(filepath.Join(shadowDir, ".git")); os.IsNotExist(err) {
		return fmt.Errorf("影子仓库不存在: %s", dir)
	}

	// 创建当前状态的检查点 (恢复前自动备份)
	if err := m.EnsureCheckpoint(dir); err != nil {
		slog.Warn("failed to create checkpoint before restore", "err", err)
	}

	// git checkout <commit>
	cmd := exec.Command("git", "-C", shadowDir, "checkout", commitHash, "--", ".")
	cmd.Env = gitEnv()
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout 失败: %s (%w)", string(output), err)
	}

	// 将影子仓库的文件复制回源目录
	if err := m.syncFromShadow(shadowDir, dir); err != nil {
		return fmt.Errorf("同步文件回源目录失败: %w", err)
	}

	// 清理源目录中不在检查点中的文件
	if err := m.cleanRemovedFiles(shadowDir, dir); err != nil {
		slog.Warn("failed to clean up deleted files", "err", err)
	}

	slog.Info("checkpoint restored", "dir", dir, "commit", commitHash)
	return nil
}

// syncFromShadow 将影子仓库的文件同步回源目录。
func (m *CheckpointManager) syncFromShadow(shadowDir, srcDir string) error {
	return filepath.Walk(shadowDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		// 跳过 .git 目录
		if info.IsDir() && info.Name() == ".git" {
			return filepath.SkipDir
		}

		// 跳过隐藏目录
		if info.IsDir() && strings.HasPrefix(info.Name(), ".") && path != shadowDir {
			return filepath.SkipDir
		}

		relPath, err := filepath.Rel(shadowDir, path)
		if err != nil {
			return nil
		}

		destPath := filepath.Join(srcDir, relPath)

		if info.IsDir() {
			return os.MkdirAll(destPath, info.Mode())
		}

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}

		destDir := filepath.Dir(destPath)
		if err := os.MkdirAll(destDir, 0755); err != nil {
			return nil
		}

		return os.WriteFile(destPath, data, info.Mode())
	})
}

// cleanRemovedFiles 清理源目录中不在影子仓库中的文件。
func (m *CheckpointManager) cleanRemovedFiles(shadowDir, srcDir string) error {
	// 收集影子仓库中的文件
	shadowFiles := make(map[string]bool)
	filepath.Walk(shadowDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if info.Name() == ".git" {
			return filepath.SkipDir
		}
		relPath, err := filepath.Rel(shadowDir, path)
		if err == nil {
			shadowFiles[relPath] = true
		}
		return nil
	})

	// 删除不在影子仓库中的文件
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		// 跳过隐藏文件
		if strings.HasPrefix(info.Name(), ".") {
			return nil
		}

		relPath, relErr := filepath.Rel(srcDir, path)
		if relErr != nil {
			return nil
		}

		if !shadowFiles[relPath] {
			os.Remove(path)
		}
		return nil
	})
}

// ───────────────────────────── 检查点工具 ─────────────────────────────

// CheckpointTool 提供检查点管理的工具接口。
type CheckpointTool struct {
	manager *CheckpointManager
}

// Name 返回工具名称。
func (t *CheckpointTool) Name() string { return "checkpoint" }

// Description 返回工具描述。
func (t *CheckpointTool) Description() string {
	return "管理操作检查点。支持创建快照、列出历史、比较差异和恢复到指定版本。"
}

// Toolset 返回工具所属工具集。
func (t *CheckpointTool) Toolset() string { return "security" }

// Emoji 返回工具图标。
func (t *CheckpointTool) Emoji() string { return "📸" }

// IsAvailable 检查 git 是否可用。
func (t *CheckpointTool) IsAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// MaxResultChars 返回结果最大字符数。
func (t *CheckpointTool) MaxResultChars() int { return 50000 }

// Schema 返回工具的 JSON Schema。
func (t *CheckpointTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "checkpoint",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "操作类型: create / list / diff / restore",
					"enum":        []string{"create", "list", "diff", "restore"},
				},
				"dir": map[string]any{
					"type":        "string",
					"description": "目标目录路径",
				},
				"from": map[string]any{
					"type":        "string",
					"description": "差异比较的起始提交哈希 (diff 操作时可选)",
				},
				"to": map[string]any{
					"type":        "string",
					"description": "差异比较的结束提交哈希 (diff 操作时可选)",
				},
				"commit": map[string]any{
					"type":        "string",
					"description": "要恢复到的提交哈希 (restore 操作时必填)",
				},
			},
			"required": []string{"action", "dir"},
		},
	}
}

// Execute 执行检查点操作。
func (t *CheckpointTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	action, ok := args["action"].(string)
	if !ok || action == "" {
		return ToolError("参数 action 是必填项"), nil
	}

	dir, ok := args["dir"].(string)
	if !ok || dir == "" {
		return ToolError("参数 dir 是必填项"), nil
	}

	if t.manager == nil {
		t.manager = NewCheckpointManager("")
	}

	switch action {
	case "create":
		return t.createCheckpoint(dir)
	case "list":
		return t.listCheckpoints(dir)
	case "diff":
		from, _ := args["from"].(string)
		to, _ := args["to"].(string)
		return t.diffCheckpoints(dir, from, to)
	case "restore":
		commit, ok := args["commit"].(string)
		if !ok || commit == "" {
			return ToolError("restore 操作需要 commit 参数"), nil
		}
		return t.restoreCheckpoint(dir, commit)
	default:
		return ToolError(fmt.Sprintf("未知操作: %s", action)), nil
	}
}

func (t *CheckpointTool) createCheckpoint(dir string) (string, error) {
	if err := t.manager.EnsureCheckpoint(dir); err != nil {
		return ToolError(fmt.Sprintf("创建检查点失败: %v", err)), nil
	}

	// 获取最新提交哈希
	checkpoints, _ := t.manager.ListCheckpoints(dir)
	commitHash := ""
	if len(checkpoints) > 0 {
		commitHash = checkpoints[0]
	}

	return ToolResult(map[string]any{
		"success":  true,
		"message":  "检查点已创建",
		"dir":      dir,
		"commit":   commitHash,
	}), nil
}

func (t *CheckpointTool) listCheckpoints(dir string) (string, error) {
	checkpoints, err := t.manager.ListCheckpoints(dir)
	if err != nil {
		return ToolError(fmt.Sprintf("列出检查点失败: %v", err)), nil
	}

	return ToolResult(map[string]any{
		"success":     true,
		"dir":         dir,
		"checkpoints": checkpoints,
		"count":       len(checkpoints),
	}), nil
}

func (t *CheckpointTool) diffCheckpoints(dir, from, to string) (string, error) {
	diff, err := t.manager.Diff(dir, from, to)
	if err != nil {
		return ToolError(fmt.Sprintf("获取差异失败: %v", err)), nil
	}

	// 限制差异输出大小
	if len(diff) > 100000 {
		diff = diff[:100000] + "\n...[差异输出已截断]"
	}

	return ToolResult(map[string]any{
		"success": true,
		"dir":     dir,
		"from":    from,
		"to":      to,
		"diff":    diff,
	}), nil
}

func (t *CheckpointTool) restoreCheckpoint(dir, commit string) (string, error) {
	if err := t.manager.Restore(dir, commit); err != nil {
		return ToolError(fmt.Sprintf("恢复检查点失败: %v", err)), nil
	}

	return ToolResult(map[string]any{
		"success": true,
		"message": fmt.Sprintf("已恢复到检查点 %s", commit),
		"dir":     dir,
		"commit":  commit,
	}), nil
}

// ───────────────────────────── init 注册 ─────────────────────────────

func init() {
	slog.Debug("registering checkpoint management tool")
	GetRegistry().Register(&CheckpointTool{})
}
