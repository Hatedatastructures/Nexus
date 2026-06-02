// Package environments 提供 Agent 运行环境的抽象和具体实现。

package environments

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// ───────────────────────────── 软件工程环境 ─────────────────────────────

// SWEEnvironment 模拟软件工程任务的运行环境。
// 它支持代码编辑、测试执行和变更提交等标准软件工作流，
// 适用于代码生成、bug 修复、功能开发等任务。
type SWEEnvironment struct {
	*BaseEnvironment

	mu sync.Mutex

	// taskDescription 任务描述
	taskDescription string
	// phase 当前工作阶段
	phase swePhase
	// files 工作区文件 (路径 -> 内容)
	files map[string]string
	// tests 测试用例 (名称 -> 是否通过)
	tests map[string]bool
	// commits 提交历史
	commits []Commit
	// startTime 任务开始时间
	startTime time.Time
	// score 当前任务评分 (0-100)
	score int
}

// swePhase 表示软件工程任务的阶段。
type swePhase int

const (
	SWEAnalyze   swePhase = iota // 分析阶段
	SWECode                        // 编码阶段
	SWETest                        // 测试阶段
	SWECommit                      // 提交阶段
	SWEDone                        // 完成
)

// phaseName 返回阶段的名称。
func (p swePhase) phaseName() string {
	names := map[swePhase]string{
		SWEAnalyze:  "分析",
		SWECode:     "编码",
		SWETest:     "测试",
		SWECommit:   "提交",
		SWEDone:     "完成",
	}
	if name, ok := names[p]; ok {
		return name
	}
	return "未知阶段"
}

// Commit 表示一次代码提交。
type Commit struct {
	// Hash 提交哈希 (简化表示)
	Hash string
	// Message 提交信息
	Message string
	// Files 变更的文件列表
	Files []string
	// Timestamp 提交时间
	Timestamp time.Time
}

// TestResult 表示一次测试运行的结果。
type TestResult struct {
	// Name 测试名称
	Name string
	// Passed 是否通过
	Passed bool
	// Message 附加信息
	Message string
	// Duration 测试耗时
	Duration time.Duration
}

// NewSWEEnvironment 创建软件工程环境实例。
func NewSWEEnvironment() *SWEEnvironment {
	base := NewBaseEnvironment("swe", "软件工程任务模拟环境")
	return &SWEEnvironment{
		BaseEnvironment: base,
		phase:           SWEAnalyze,
		files:           make(map[string]string),
		tests:           make(map[string]bool),
		commits:         make([]Commit, 0),
		score:           0,
	}
}

// SetTask 设置任务描述。
func (s *SWEEnvironment) SetTask(desc string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.taskDescription = desc
	s.startTime = time.Now()
	slog.Info("SWE: task set", "description", desc)
}

// Execute 执行软件工程环境中的动作。
// 支持的动作类型: "read_file"、"write_file"、"run_test"、"commit"、"submit"。
func (s *SWEEnvironment) Execute(ctx context.Context, action Action) (*Observation, error) {
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("软件工程执行被取消: %w", ctx.Err())
	default:
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	switch action.Type {
	case "read_file":
		return s.handleReadFile(action)
	case "write_file":
		return s.handleWriteFile(action)
	case "run_test":
		return s.handleRunTest(action)
	case "commit":
		return s.handleCommit(action)
	case "submit":
		return s.handleSubmit()
	default:
		return nil, fmt.Errorf("软件工程环境不支持的动作类型: %s", action.Type)
	}
}

// handleReadFile 处理文件读取动作。
func (s *SWEEnvironment) handleReadFile(action Action) (*Observation, error) {
	path, _ := action.Parameters["path"].(string)
	if path == "" {
		return &Observation{
			State:  "文件路径为空",
			Reward: -0.1,
			Done:   false,
			Info:   map[string]any{"reason": "empty_path"},
		}, nil
	}

	content, exists := s.files[path]
	if !exists {
		return &Observation{
			State:  fmt.Sprintf("文件不存在: %s", path),
			Reward: 0.0,
			Done:   false,
			Info: map[string]any{
				"reason": "file_not_found",
				"path":   path,
			},
		}, nil
	}

	slog.Debug("SWE: read file", "path", path, "size", len(content))

	return &Observation{
		State:  fmt.Sprintf("已读取文件: %s (%d 字节)", path, len(content)),
		Reward: 0.05,
		Done:   false,
		Info: map[string]any{
			"path":    path,
			"content": content,
			"size":    len(content),
		},
	}, nil
}

// handleWriteFile 处理文件写入动作。
func (s *SWEEnvironment) handleWriteFile(action Action) (*Observation, error) {
	path, _ := action.Parameters["path"].(string)
	content, _ := action.Parameters["content"].(string)

	if path == "" {
		return &Observation{
			State:  "文件路径为空",
			Reward: -0.2,
			Done:   false,
			Info:   map[string]any{"reason": "empty_path"},
		}, nil
	}

	// 推进到编码阶段
	if s.phase == SWEAnalyze {
		s.phase = SWECode
	}

	isNew := false
	if _, exists := s.files[path]; !exists {
		isNew = true
	}
	s.files[path] = content

	// 更新评分
	s.evaluateScore()

	slog.Info("SWE: write file",
		"path", path,
		"size", len(content),
		"new_file", isNew,
	)

	return &Observation{
		State:  fmt.Sprintf("已写入文件: %s (%d 字节, %s)", path, len(content), map[bool]string{true: "新建", false: "修改"}[isNew]),
		Reward: 0.1,
		Done:   false,
		Info: map[string]any{
			"path":     path,
			"size":     len(content),
			"new_file": isNew,
			"files_count": len(s.files),
		},
	}, nil
}

// handleRunTest 处理运行测试动作。
func (s *SWEEnvironment) handleRunTest(action Action) (*Observation, error) {
	// 推进到测试阶段
	if s.phase == SWEAnalyze || s.phase == SWECode {
		s.phase = SWETest
	}

	testName, _ := action.Parameters["name"].(string)
	if testName == "" {
		testName = "default_test"
	}

	// 模拟测试执行
	// 实际环境中应调用真实的测试框架
	passed := len(s.files) > 0
	msg := "测试通过"
	if !passed {
		msg = "测试失败: 无文件可测试"
	}

	s.tests[testName] = passed

	result := TestResult{
		Name:     testName,
		Passed:   passed,
		Message:  msg,
		Duration: time.Millisecond * 100,
	}

	s.evaluateScore()

	slog.Info("SWE: run test",
		"name", result.Name,
		"passed", result.Passed,
		"message", result.Message,
	)

	reward := 0.1
	if !passed {
		reward = -0.1
	}

	return &Observation{
		State:  fmt.Sprintf("测试结果: %s (%s)", result.Name, result.Message),
		Reward: reward,
		Done:   false,
		Info: map[string]any{
			"test_name": result.Name,
			"passed":    result.Passed,
			"message":   result.Message,
			"duration":  result.Duration.String(),
			"total_tests": len(s.tests),
		},
	}, nil
}

// handleCommit 处理提交动作。
func (s *SWEEnvironment) handleCommit(action Action) (*Observation, error) {
	// 推进到提交阶段
	if s.phase < SWECommit {
		s.phase = SWECommit
	}

	message, _ := action.Parameters["message"].(string)
	if message == "" {
		message = "auto commit"
	}

	// 收集变更的文件
	var changedFiles []string
	for path := range s.files {
		changedFiles = append(changedFiles, path)
	}

	commit := Commit{
		Hash:      fmt.Sprintf("commit_%d", len(s.commits)+1),
		Message:   message,
		Files:     changedFiles,
		Timestamp: time.Now(),
	}
	s.commits = append(s.commits, commit)

	s.evaluateScore()

	slog.Info("SWE: commit changes",
		"hash", commit.Hash,
		"message", commit.Message,
		"files_count", len(changedFiles),
	)

	return &Observation{
		State:  fmt.Sprintf("已提交: %s (%d 个文件)", commit.Hash, len(changedFiles)),
		Reward: 0.2,
		Done:   false,
		Info: map[string]any{
			"hash":        commit.Hash,
			"message":     commit.Message,
			"files":       changedFiles,
			"commits_count": len(s.commits),
		},
	}, nil
}

// handleSubmit 处理任务提交动作。
func (s *SWEEnvironment) handleSubmit() (*Observation, error) {
	if len(s.files) == 0 {
		return &Observation{
			State:  "无变更文件可提交",
			Reward: -0.3,
			Done:   false,
			Info:   map[string]any{"reason": "no_files"},
		}, nil
	}

	// 检查是否有测试通过
	hasPassingTest := false
	for _, passed := range s.tests {
		if passed {
			hasPassingTest = true
			break
		}
	}

	if !hasPassingTest && len(s.tests) > 0 {
		return &Observation{
			State:  "存在失败的测试，请先修复",
			Reward: -0.4,
			Done:   false,
			Info:   map[string]any{"reason": "failing_tests"},
		}, nil
	}

	s.phase = SWEDone
	s.done = true
	s.evaluateScore()

	slog.Info("SWE: task completed",
		"files", len(s.files),
		"tests", len(s.tests),
		"commits", len(s.commits),
		"score", s.score,
	)

	return &Observation{
		State:  "任务已完成并提交",
		Reward: 1.0,
		Done:   true,
		Info: map[string]any{
			"files":       len(s.files),
			"tests":       len(s.tests),
			"commits":     len(s.commits),
			"score":       s.score,
			"duration":    time.Since(s.startTime).String(),
		},
	}, nil
}

// Step 推进软件工程环境的内部阶段。
func (s *SWEEnvironment) Step(ctx context.Context) (*Observation, error) {
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("软件工程步进被取消: %w", ctx.Err())
	default:
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 自动推进阶段
	switch s.phase {
	case SWEAnalyze:
		s.phase = SWECode
	case SWECode:
		s.phase = SWETest
	case SWETest:
		s.phase = SWECommit
	case SWECommit:
		s.phase = SWEDone
		s.done = true
	}

	slog.Debug("SWE: phase advanced", "phase", s.phase.phaseName())

	return &Observation{
		State:  fmt.Sprintf("阶段已推进至: %s", s.phase.phaseName()),
		Reward: 0.05,
		Done:   s.done,
		Info: map[string]any{
			"phase": s.phase.phaseName(),
			"files_count": len(s.files),
			"tests_count": len(s.tests),
		},
	}, nil
}

// Render 返回软件工程环境的可渲染状态描述。
func (s *SWEEnvironment) Render() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 统计测试结果
	passedTests := 0
	failedTests := 0
	for _, passed := range s.tests {
		if passed {
			passedTests++
		} else {
			failedTests++
		}
	}

	return fmt.Sprintf("SWE Environment\n"+
		"  Task: %s\n"+
		"  Phase: %s\n"+
		"  Files: %d\n"+
		"  Tests: %d passed, %d failed\n"+
		"  Commits: %d\n"+
		"  Score: %d/100\n"+
		"  Duration: %s\n"+
		"  Done: %v",
		s.taskDescription,
		s.phase.phaseName(),
		len(s.files),
		passedTests, failedTests,
		len(s.commits),
		s.score,
		time.Since(s.startTime).Truncate(time.Second),
		s.done,
	)
}

// evaluateScore 评估当前任务完成质量。
func (s *SWEEnvironment) evaluateScore() {
	score := 0

	// 文件数量评分 (最多 20 分)
	if len(s.files) > 0 {
		score += 10
		if len(s.files) >= 3 {
			score += 10
		}
	}

	// 测试通过评分 (最多 40 分)
	passedCount := 0
	totalTests := len(s.tests)
	for _, passed := range s.tests {
		if passed {
			passedCount++
		}
	}
	if totalTests > 0 {
		score += (passedCount * 40) / totalTests
	}

	// 提交评分 (最多 20 分)
	if len(s.commits) > 0 {
		score += 20
	}

	// 阶段完成评分 (最多 20 分)
	switch s.phase {
	case SWEDone:
		score += 20
	case SWECommit:
		score += 15
	case SWETest:
		score += 10
	case SWECode:
		score += 5
	}

	if score > 100 {
		score = 100
	}
	s.score = score
}

// Score 获取当前任务评分。
func (s *SWEEnvironment) Score() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.score
}

// Files 返回工作区文件映射的副本。
func (s *SWEEnvironment) Files() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make(map[string]string, len(s.files))
	for k, v := range s.files {
		cp[k] = v
	}
	return cp
}

// Commits 返回提交历史副本。
func (s *SWEEnvironment) Commits() []Commit {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]Commit, len(s.commits))
	copy(cp, s.commits)
	return cp
}
