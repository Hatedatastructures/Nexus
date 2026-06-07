package environments

import (
	"fmt"
	"log/slog"
	"time"
)

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
			"path":        path,
			"size":        len(content),
			"new_file":    isNew,
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
			"test_name":   result.Name,
			"passed":      result.Passed,
			"message":     result.Message,
			"duration":    result.Duration.String(),
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
			"hash":          commit.Hash,
			"message":       commit.Message,
			"files":         changedFiles,
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
			"files":    len(s.files),
			"tests":    len(s.tests),
			"commits":  len(s.commits),
			"score":    s.score,
			"duration": time.Since(s.startTime).String(),
		},
	}, nil
}
