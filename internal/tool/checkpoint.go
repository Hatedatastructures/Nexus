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

// ───────────────────────────── 提交哈希验证 ─────────────────────────────

// validCommitHash 匹配合法的 Git 提交哈希 (短格式或长格式)。
var validCommitHash = regexp.MustCompile(`^[0-9a-fA-F]{4,40}$`)

// ───────────────────────────── 检查点操作 ─────────────────────────────

// EnsureCheckpoint 确保目录有对应的影子 Git 仓库，并创建一个新的检查点。
// 如果影子仓库不存在，会自动初始化。然后暂存所有变更并提交。
func (m *CheckpointManager) EnsureCheckpoint(ctx context.Context, dir string) error {
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
		if err := m.initShadowRepo(ctx, dir, shadowDir); err != nil {
			return fmt.Errorf("初始化影子仓库失败: %w", err)
		}
	}

	// 同步文件到影子仓库
	if err := m.syncToShadow(dir, shadowDir); err != nil {
		return fmt.Errorf("同步文件失败: %w", err)
	}

	// 检查是否有变更
	hasChanges, err := m.hasChanges(ctx, shadowDir)
	if err != nil {
		return fmt.Errorf("检查变更失败: %w", err)
	}

	if !hasChanges {
		slog.Debug("checkpoint: no changes, skipping commit", "dir", dir)
		return nil
	}

	// 暂存并提交
	if err := m.commitCheckpoint(ctx, shadowDir); err != nil {
		return fmt.Errorf("提交检查点失败: %w", err)
	}

	slog.Info("checkpoint created", "dir", dir)
	return nil
}

// ListCheckpoints 列出目录的所有检查点提交。
// 返回按时间倒序排列的提交哈希列表。
func (m *CheckpointManager) ListCheckpoints(ctx context.Context, dir string) ([]string, error) {
	shadowDir := m.shadowRepoPath(dir)

	// 检查影子仓库是否存在
	if _, err := os.Stat(filepath.Join(shadowDir, ".git")); os.IsNotExist(err) {
		return []string{}, nil
	}

	cmd := exec.CommandContext(ctx, "git", "-C", shadowDir, "log", "--format=%H", "--all")
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
func (m *CheckpointManager) Diff(ctx context.Context, dir string, from, to string) (string, error) {
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

	cmd := exec.CommandContext(ctx, "git", args...)
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
func (m *CheckpointManager) Restore(ctx context.Context, dir string, commitHash string) error {
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
	if err := m.EnsureCheckpoint(ctx, dir); err != nil {
		slog.Warn("failed to create checkpoint before restore", "err", err)
	}

	// git checkout <commit>
	cmd := exec.CommandContext(ctx, "git", "-C", shadowDir, "checkout", commitHash, "--", ".")
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
		return t.createCheckpoint(ctx, dir)
	case "list":
		return t.listCheckpoints(ctx, dir)
	case "diff":
		from, _ := args["from"].(string)
		to, _ := args["to"].(string)
		return t.diffCheckpoints(ctx, dir, from, to)
	case "restore":
		commit, ok := args["commit"].(string)
		if !ok || commit == "" {
			return ToolError("restore 操作需要 commit 参数"), nil
		}
		return t.restoreCheckpoint(ctx, dir, commit)
	default:
		return ToolError(fmt.Sprintf("未知操作: %s", action)), nil
	}
}

func (t *CheckpointTool) createCheckpoint(ctx context.Context, dir string) (string, error) {
	if err := t.manager.EnsureCheckpoint(ctx, dir); err != nil {
		return ToolError(fmt.Sprintf("创建检查点失败: %v", err)), nil
	}

	// 获取最新提交哈希
	checkpoints, _ := t.manager.ListCheckpoints(ctx, dir)
	commitHash := ""
	if len(checkpoints) > 0 {
		commitHash = checkpoints[0]
	}

	return ToolResult(map[string]any{
		"success": true,
		"message": "检查点已创建",
		"dir":     dir,
		"commit":  commitHash,
	}), nil
}

func (t *CheckpointTool) listCheckpoints(ctx context.Context, dir string) (string, error) {
	checkpoints, err := t.manager.ListCheckpoints(ctx, dir)
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

func (t *CheckpointTool) diffCheckpoints(ctx context.Context, dir, from, to string) (string, error) {
	diff, err := t.manager.Diff(ctx, dir, from, to)
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

func (t *CheckpointTool) restoreCheckpoint(ctx context.Context, dir, commit string) (string, error) {
	if err := t.manager.Restore(ctx, dir, commit); err != nil {
		return ToolError(fmt.Sprintf("恢复检查点失败: %v", err)), nil
	}

	return ToolResult(map[string]any{
		"success": true,
		"message": fmt.Sprintf("已恢复到检查点 %s", commit),
		"dir":     dir,
		"commit":  commit,
	}), nil
}
