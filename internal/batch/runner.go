// Package batch 提供并行批处理运行器。
// 使用 goroutine worker pool 并行处理多个 prompt，
// 支持内容级恢复、容器镜像覆盖和 JSONL 输出。
package batch

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"nexus-agent/internal/config"
)

// ───────────────────────────── 数据结构 ─────────────────────────────

// Prompt 表示一个批处理 prompt。
type Prompt struct {
	Text        string            `json:"text"`
	Model       string            `json:"model,omitempty"`
	Container   string            `json:"container,omitempty"` // Docker/Modal/Singularity/Daytona
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// Trajectory 表示一条处理轨迹。
type Trajectory struct {
	Prompt      string        `json:"prompt"`
	Response    string        `json:"response"`
	Model       string        `json:"model"`
	ToolCalls   int           `json:"tool_calls"`
	Tokens      int64         `json:"tokens"`
	Duration    time.Duration `json:"duration"`
	Completed   bool          `json:"completed"`
	Error       string        `json:"error,omitempty"`
	Timestamp   time.Time     `json:"timestamp"`
}

// BatchResult 批处理结果。
type BatchResult struct {
	Total      int           `json:"total"`
	Completed  int           `json:"completed"`
	Failed     int           `json:"failed"`
	Skipped    int           `json:"skipped"`
	Duration   time.Duration `json:"duration"`
	OutputFile string        `json:"output_file"`
}

// WorkerFunc 是处理单个 prompt 的函数类型。
type WorkerFunc func(ctx context.Context, prompt Prompt) (Trajectory, error)

// ───────────────────────────── 运行器 ─────────────────────────────

// BatchRunner 并行批处理运行器。
type BatchRunner struct {
	cfg      config.BatchConfig
	workerFn WorkerFunc
	outputDir string
}

// NewBatchRunner 创建批处理运行器。
func NewBatchRunner(cfg config.BatchConfig, workerFn WorkerFunc, outputDir string) *BatchRunner {
	if cfg.MaxWorkers <= 0 {
		cfg.MaxWorkers = 4
	}
	return &BatchRunner{
		cfg:       cfg,
		workerFn:  workerFn,
		outputDir: outputDir,
	}
}

// Run 执行批处理。
func (br *BatchRunner) Run(ctx context.Context, prompts []Prompt) (*BatchResult, error) {
	startTime := time.Now()

	// 内容级恢复: 加载已有结果
	existingResults, err := br.loadExistingResults()
	if err != nil {
		slog.Warn("failed to load existing results", "err", err)
		existingResults = make(map[string]bool)
	}

	// 过滤已完成的 prompt
	var pending []Prompt
	for _, p := range prompts {
		hash := contentHash(p.Text)
		if existingResults[hash] {
			continue
		}
		pending = append(pending, p)
	}

	result := &BatchResult{
		Total:   len(prompts),
		Skipped: len(prompts) - len(pending),
	}

	slog.Info("batch started",
		"total", result.Total,
		"pending", len(pending),
		"skipped", result.Skipped,
		"workers", br.cfg.MaxWorkers,
	)

	if len(pending) == 0 {
		result.Duration = time.Since(startTime)
		return result, nil
	}

	// 创建 worker pool
	workCh := make(chan Prompt, len(pending))
	resultCh := make(chan Trajectory, len(pending))
	var wg sync.WaitGroup

	// 启动 workers
	for i := 0; i < br.cfg.MaxWorkers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for prompt := range workCh {
				select {
				case <-ctx.Done():
					return
				default:
				}

				slog.Debug("worker processing prompt", "worker", id, "text", truncateStr(prompt.Text, 50))
				traj, err := br.workerFn(ctx, prompt)
				if err != nil {
					traj = Trajectory{
						Prompt:    prompt.Text,
						Error:     err.Error(),
						Timestamp: time.Now(),
					}
				}
				traj.Prompt = prompt.Text
				if traj.Timestamp.IsZero() {
					traj.Timestamp = time.Now()
				}
				resultCh <- traj
			}
		}(i)
	}

	// 分发任务
	go func() {
		defer close(workCh)
		for _, p := range pending {
			select {
			case <-ctx.Done():
				return
			case workCh <- p:
			}
		}
	}()

	// 收集结果
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// 写入输出
	outputPath := br.outputPath()
	f, err := os.Create(outputPath)
	if err != nil {
		return nil, fmt.Errorf("创建输出文件: %w", err)
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	defer w.Flush()

	for traj := range resultCh {
		if traj.Error != "" {
			result.Failed++
		} else {
			result.Completed++
		}

		data, err := json.Marshal(traj)
		if err != nil {
			slog.Warn("序列化轨迹失败", "err", err)
			continue
		}
		if _, err := w.Write(data); err != nil {
			slog.Warn("写入轨迹失败", "err", err)
		}
		w.WriteByte('\n')

		// 记录到恢复映射
		br.saveResultHash(contentHash(traj.Prompt))
	}

	result.Duration = time.Since(startTime)
	result.OutputFile = outputPath

	slog.Info("batch completed",
		"completed", result.Completed,
		"failed", result.Failed,
		"skipped", result.Skipped,
		"duration", result.Duration,
	)

	return result, nil
}

// ───────────────────────────── 恢复机制 ─────────────────────────────

func (br *BatchRunner) loadExistingResults() (map[string]bool, error) {
	results := make(map[string]bool)

	// 扫描已有输出文件
	pattern := filepath.Join(br.outputDir, "batch_*.jsonl")
	matches, _ := filepath.Glob(pattern)

	for _, path := range matches {
		f, err := os.Open(path)
		if err != nil {
			continue
		}

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			var traj Trajectory
			if json.Unmarshal(scanner.Bytes(), &traj) == nil && traj.Error == "" {
				results[contentHash(traj.Prompt)] = true
			}
		}
		if err := scanner.Err(); err != nil {
			slog.Warn("读取轨迹文件出错", "path", path, "err", err)
		}
		f.Close()
	}

	return results, nil
}

func (br *BatchRunner) saveResultHash(hash string) {
	hashFile := filepath.Join(br.outputDir, ".batch_hashes")
	f, err := os.OpenFile(hashFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\n", hash)
}

func (br *BatchRunner) outputPath() string {
	return filepath.Join(br.outputDir, fmt.Sprintf("batch_%d.jsonl", time.Now().Unix()))
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

// contentHash 生成内容的 SHA-256 哈希用于去重。
func contentHash(text string) string {
	h := sha256.Sum256([]byte(text))
	return fmt.Sprintf("%x", h[:8]) // 取前 8 字节 (16 字符)
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// MergeJSONL 将多个 batch_*.jsonl 合并为 trajectories.jsonl，
// 过滤损坏条目。
func MergeJSONL(outputDir string) (string, int, error) {
	pattern := filepath.Join(outputDir, "batch_*.jsonl")
	matches, _ := filepath.Glob(pattern)

	outputPath := filepath.Join(outputDir, "trajectories.jsonl")
	out, err := os.Create(outputPath)
	if err != nil {
		return "", 0, err
	}
	defer out.Close()

	w := bufio.NewWriter(out)
	defer w.Flush()

	total := 0
	for _, path := range matches {
		f, err := os.Open(path)
		if err != nil {
			continue
		}

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Bytes()
			var traj Trajectory
			if json.Unmarshal(line, &traj) == nil && traj.Prompt != "" {
				w.Write(line)
				w.WriteByte('\n')
				total++
			}
		}
		if err := scanner.Err(); err != nil {
			slog.Warn("读取轨迹文件出错", "path", path, "err", err)
		}
		f.Close()
	}

	return outputPath, total, nil
}
