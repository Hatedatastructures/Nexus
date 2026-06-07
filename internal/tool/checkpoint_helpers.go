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
	"strings"
)

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

// ───────────────────────────── 检查点内部操作 ─────────────────────────────

// initShadowRepo 初始化影子 Git 仓库。
func (m *CheckpointManager) initShadowRepo(ctx context.Context, srcDir, shadowDir string) error {
	// 清理可能存在的旧目录
	_ = os.RemoveAll(shadowDir)

	// 创建影子仓库目录
	if err := os.MkdirAll(shadowDir, 0755); err != nil {
		return err
	}

	// git init
	cmd := exec.CommandContext(ctx, "git", "init", shadowDir)
	cmd.Env = gitEnv()
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git init 失败: %s (%w)", string(output), err)
	}

	// 初始提交 (空仓库需要至少一个提交)
	cmd = exec.CommandContext(ctx, "git", "-C", shadowDir, "commit", "--allow-empty", "-m", "初始检查点")
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
			slog.Warn("checkpoint: skip inaccessible file", "path", path, "error", err)
			return nil
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
			slog.Warn("checkpoint: skip unreadable file", "path", path, "error", readErr)
			return nil
		}

		// 确保目标目录存在
		destDir := filepath.Dir(destPath)
		if err := os.MkdirAll(destDir, 0755); err != nil {
			slog.Warn("checkpoint: skip file, cannot create dest dir", "path", path, "error", err)
			return nil
		}

		return os.WriteFile(destPath, data, info.Mode())
	})
}

// hasChanges 检查影子仓库是否有未提交的变更。
func (m *CheckpointManager) hasChanges(ctx context.Context, shadowDir string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", shadowDir, "status", "--porcelain")
	cmd.Env = gitEnv()
	output, err := cmd.Output()
	if err != nil {
		return false, err
	}
	return len(strings.TrimSpace(string(output))) > 0, nil
}

// commitCheckpoint 暂存所有变更并创建提交。
func (m *CheckpointManager) commitCheckpoint(ctx context.Context, shadowDir string) error {
	// git add -A
	cmd := exec.CommandContext(ctx, "git", "-C", shadowDir, "add", "-A")
	cmd.Env = gitEnv()
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git add 失败: %s (%w)", string(output), err)
	}

	// git commit
	cmd = exec.CommandContext(ctx, "git", "-C", shadowDir, "commit", "-m", "自动检查点")
	cmd.Env = gitEnv()
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit 失败: %s (%w)", string(output), err)
	}

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
			slog.Warn("checkpoint: skip unreadable file", "path", path, "error", readErr)
			return nil
		}

		destDir := filepath.Dir(destPath)
		if err := os.MkdirAll(destDir, 0755); err != nil {
			slog.Warn("checkpoint: skip file, cannot create dest dir", "path", path, "error", err)
			return nil
		}

		return os.WriteFile(destPath, data, info.Mode())
	})
}

// cleanRemovedFiles 清理源目录中不在影子仓库中的文件。
func (m *CheckpointManager) cleanRemovedFiles(shadowDir, srcDir string) error {
	// 收集影子仓库中的文件
	shadowFiles := make(map[string]bool)
	_ = filepath.Walk(shadowDir, func(path string, info os.FileInfo, err error) error {
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
			_ = os.Remove(path)
		}
		return nil
	})
}
