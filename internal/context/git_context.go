// Package context 提供 git 上下文自动发现与注入能力。
// DiscoverGitContext 自动检测当前工作目录的 git 状态，
// 并将分支、最近提交、暂存文件等信息格式化为可注入系统提示词的文本块。
package context

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ───────────────────────────── 数据结构 ─────────────────────────────

// GitCommit 表示一条 git 提交记录。
type GitCommit struct {
	Hash    string // 提交哈希 (短格式)
	Message string // 提交消息 (首行)
	Author  string // 提交作者
	Time    string // 相对时间描述 (如 "2h ago", "1d ago")
}

// GitContext 包含从 git 仓库自动发现的上下文信息。
// 用于将当前仓库状态注入系统提示词，帮助模型理解工作环境。
type GitContext struct {
	Branch        string      // 当前分支名
	RecentCommits []GitCommit // 最近 N 条提交记录
	StagedFiles   []string    // 已暂存 (staged) 的文件列表
	Status        string      // git status --short 的原始输出
	DiffStaged    string      // git diff --cached output (staged changes)
	DiffUnstaged  string      // git diff output (unstaged changes)
}

// ───────────────────────────── 发现 ─────────────────────────────

const gitCommandTimeout = 5 * time.Second

// git 上下文缓存，避免每次 Build 都执行 7 个 git 子进程
var (
	gitCacheMu   sync.Mutex
	gitCache     *GitContext
	gitCacheTime time.Time
	gitCacheTTL  = 5 * time.Second
)

// DiscoverGitContext 从指定工作目录自动发现 git 上下文信息。
// 结果缓存 5 秒以避免频繁的子进程调用。
func DiscoverGitContext(cwd string) *GitContext {
	gitCacheMu.Lock()
	defer gitCacheMu.Unlock()

	if gitCache != nil && time.Since(gitCacheTime) < gitCacheTTL {
		return gitCache
	}

	result := discoverGitContextUncached(cwd)
	gitCache = result
	gitCacheTime = time.Now()
	return result
}

// discoverGitContextUncached 执行实际的 git 上下文发现（无缓存）。
func discoverGitContextUncached(cwd string) *GitContext {
	// 1. 检测是否在 git 仓库内
	if output, err := runGitCmd(cwd, "rev-parse", "--show-toplevel"); err != nil || output == "" {
		return nil
	}

	gc := &GitContext{}

	// 2. 获取当前分支
	if branch, err := runGitCmd(cwd, "branch", "--show-current"); err == nil && branch != "" {
		gc.Branch = branch
	}

	// 3. 获取最近提交记录
	if logOutput, err := runGitCmd(cwd, "log", "--oneline", "-5",
		"--format=%h|%s|%an|%ar"); err == nil {
		gc.RecentCommits = parseLogOutput(logOutput)
	}

	// 4. 获取工作区状态
	if status, err := runGitCmd(cwd, "status", "--short"); err == nil {
		gc.Status = strings.TrimSpace(status)
	}

	// 5. 获取已暂存文件
	if stagedOutput, err := runGitCmd(cwd, "diff", "--cached", "--name-only"); err == nil {
		staged := strings.Split(strings.TrimSpace(stagedOutput), "\n")
		for _, f := range staged {
			f = strings.TrimSpace(f)
			if f != "" {
				gc.StagedFiles = append(gc.StagedFiles, f)
			}
		}
	}

	// 6. Get staged diff
	if diffStaged, err := runGitCmd(cwd, "diff", "--cached"); err == nil {
		gc.DiffStaged = truncateDiff(diffStaged, 8000)
	}

	// 7. Get unstaged diff
	if diffUnstaged, err := runGitCmd(cwd, "diff"); err == nil {
		gc.DiffUnstaged = truncateDiff(diffUnstaged, 8000)
	}

	return gc
}

// ───────────────────────────── 渲染 ─────────────────────────────

// Render 将 GitContext 格式化为可注入系统提示词的 XML 文本块。
//
// 输出格式:
//
//	<git_context>
//	Branch: main
//	Recent commits:
//	- abc1234 Fix the bug (Author, 2h ago)
//	- def5678 Add feature (Author, 1d ago)
//	Staged files: file1.go, file2.go
//	Status: M file.go
//	</git_context>
func (gc *GitContext) Render() string {
	var sb strings.Builder

	sb.WriteString("<git_context>\n")
	sb.WriteString("Branch: ")
	sb.WriteString(gc.Branch)
	sb.WriteString("\n")

	if len(gc.RecentCommits) > 0 {
		sb.WriteString("Recent commits:\n")
		for _, commit := range gc.RecentCommits {
			sb.WriteString("- ")
			sb.WriteString(commit.Hash)
			sb.WriteString(" ")
			sb.WriteString(commit.Message)
			sb.WriteString(" (")
			sb.WriteString(commit.Author)
			sb.WriteString(", ")
			sb.WriteString(commit.Time)
			sb.WriteString(")\n")
		}
	}

	if len(gc.StagedFiles) > 0 {
		sb.WriteString("Staged files: ")
		sb.WriteString(strings.Join(gc.StagedFiles, ", "))
		sb.WriteString("\n")
	}

	if gc.Status != "" {
		sb.WriteString("Status: ")
		sb.WriteString(gc.Status)
		sb.WriteString("\n")
	}

	if gc.DiffStaged != "" {
		sb.WriteString("\nStaged changes:\n")
		sb.WriteString(gc.DiffStaged)
		sb.WriteString("\n")
	}

	if gc.DiffUnstaged != "" {
		sb.WriteString("\nUnstaged changes:\n")
		sb.WriteString(gc.DiffUnstaged)
		sb.WriteString("\n")
	}

	sb.WriteString("</git_context>")

	return sb.String()
}

// ───────────────────────────── 内部工具 ─────────────────────────────

// runGitCmd 在指定工作目录下执行 git 子命令，返回标准输出内容。
// 每个命令有独立的 5 秒超时限制，超时或执行失败均返回错误。
func runGitCmd(cwd string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitCommandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = cwd

	output, err := cmd.Output()
	if err != nil {
		slog.Debug("git command execution failed",
			"cwd", cwd,
			"args", strings.Join(args, " "),
			"err", err,
		)
		return "", err
	}

	return string(output), nil
}

// truncateDiff 截断 diff 输出，保留头部和尾部内容。
// 超过 maxChars 时保留头部 75% + 尾部 25%。
func truncateDiff(diff string, maxChars int) string {
	diff = strings.TrimSpace(diff)
	if len(diff) <= maxChars {
		return diff
	}
	headSize := maxChars * 3 / 4
	tailSize := maxChars - headSize
	head := diff[:headSize]
	tail := diff[len(diff)-tailSize:]
	return head + fmt.Sprintf("\n...[truncated %d chars]...\n", len(diff)-maxChars) + tail
}

// parseLogOutput 解析 git log --format=%h|%s|%an|%ar 的输出。
// 每行格式: "abc1234|Fix the bug|Author|2h ago"
func parseLogOutput(output string) []GitCommit {
	var commits []GitCommit

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, "|", 4)
		if len(parts) < 4 {
			continue
		}

		commits = append(commits, GitCommit{
			Hash:    strings.TrimSpace(parts[0]),
			Message: strings.TrimSpace(parts[1]),
			Author:  strings.TrimSpace(parts[2]),
			Time:    strings.TrimSpace(parts[3]),
		})
	}

	return commits
}

// ensure GitContext satisfies the renderable interface used by Builder.
// This allows future integration with the Builder pipeline via a simple type check.
var _ fmt.Stringer = (*GitContext)(nil)

// String implements fmt.Stringer by delegating to Render.
func (gc *GitContext) String() string {
	return gc.Render()
}
