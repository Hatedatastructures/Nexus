// Package tool 提供代码执行工具。
// 在独立子进程中执行代码片段，支持多种语言，
// 包含超时保护和输出截断。
package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"nexus-agent/internal/approval"
)

// ───────────────────────────── 代码执行工具 ─────────────────────────────

// CodeExecuteTool 实现在子进程中执行代码的功能。
// 支持 Python、Node.js、Bash 等语言。
type CodeExecuteTool struct{}

// Name 返回工具名称。
func (t *CodeExecuteTool) Name() string { return "code_execute" }

// Description 返回工具描述。
func (t *CodeExecuteTool) Description() string {
	return "在隔离的子进程中执行代码片段。支持 Python、JavaScript、Bash 等语言。包含超时保护和输出大小限制。"
}

// Toolset 返回工具所属工具集。
func (t *CodeExecuteTool) Toolset() string { return "code" }

// Emoji 返回工具图标。
func (t *CodeExecuteTool) Emoji() string { return "▶️" }

// IsAvailable 检查代码执行是否可用。
func (t *CodeExecuteTool) IsAvailable() bool {
	// 检查至少有一个运行时可用
	return t.findRuntime("python") != "" ||
		t.findRuntime("node") != "" ||
		t.findRuntime("bash") != ""
}

// MaxResultChars 返回结果最大字符数。
func (t *CodeExecuteTool) MaxResultChars() int { return 50000 }

// Schema 返回工具的 JSON Schema。
func (t *CodeExecuteTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "code_execute",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"code": map[string]any{
					"type":        "string",
					"description": "要执行的代码内容",
				},
				"language": map[string]any{
					"type":        "string",
					"description": "编程语言: python, javascript, bash, sh",
					"enum":        []string{"python", "javascript", "bash", "sh"},
				},
				"timeout": map[string]any{
					"type":        "integer",
					"description": "超时时间 (秒)，默认 30 秒，最大 300 秒",
				},
			},
			"required": []string{"code"},
		},
	}
}

// Execute 执行代码片段。
// 创建临时文件 → 启动子进程 → 捕获输出 → 清理。
func (t *CodeExecuteTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	code, ok := args["code"].(string)
	if !ok || code == "" {
		return ToolError("参数 code 是必填项且必须为字符串"), nil
	}

	// 审批检查
	if v := globalTerminalChecker.Load(); v != nil {
		checker := v.(*approval.Checker)
		result, reason := checker.Check(ctx, code)
		switch result {
		case approval.Denied:
			slog.Warn("code_execute denied by approval engine", "reason", reason)
			return ToolError(fmt.Sprintf("代码执行被拒绝: %s", reason)), nil
		case approval.Pending:
			return ToolError(fmt.Sprintf("代码执行需要审批: %s", reason)), nil
		}
	}

	language := "python"
	if lang, ok := args["language"].(string); ok && lang != "" {
		language = lang
	}

	timeout := 30 * time.Second
	if v, ok := args["timeout"].(float64); ok && v > 0 {
		timeout = time.Duration(v) * time.Second
		if timeout > 300*time.Second {
			timeout = 300 * time.Second
		}
	}

	// 查找运行时
	runtime := t.findRuntime(language)
	if runtime == "" {
		return ToolError(fmt.Sprintf("未找到 %s 运行时。请确保已安装相应的解释器。", language)), nil
	}

	// 创建临时目录
	tmpDir, err := os.MkdirTemp("", "nexus-code-*")
	if err != nil {
		slog.Error("failed to create temp directory", "err", err)
		return ToolError(fmt.Sprintf("创建临时目录失败: %v", err)), nil
	}
	defer os.RemoveAll(tmpDir)

	// 创建代码文件
	ext := t.fileExtension(language)
	codeFile := filepath.Join(tmpDir, "code"+ext)
	if err := os.WriteFile(codeFile, []byte(code), 0600); err != nil {
		return ToolError(fmt.Sprintf("写入代码文件失败: %v", err)), nil
	}

	// 构建命令
	var cmdArgs []string
	switch language {
	case "python":
		cmdArgs = []string{runtime, codeFile}
	case "javascript":
		cmdArgs = []string{runtime, codeFile}
	case "bash", "sh":
		cmdArgs = []string{runtime, codeFile}
	}

	// 创建带超时的 context
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 执行
	cmd := exec.CommandContext(execCtx, cmdArgs[0], cmdArgs[1:]...)
	cmd.Dir = tmpDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	startTime := time.Now()
	runErr := cmd.Run()
	duration := time.Since(startTime)

	output := stdout.String()
	if stderr.Len() > 0 {
		if output != "" {
			output += "\n"
		}
		output += "[stderr]\n" + stderr.String()
	}

	// 输出截断
	const maxOutput = 50000
	if len(output) > maxOutput {
		output = output[:maxOutput] + fmt.Sprintf("\n\n...[输出已截断，原始大小: %d 字节]", len(output))
	}

	exitCode := 0
	interrupted := false

	if runErr != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			output += fmt.Sprintf("\n\n[执行超时 (%v)，已终止]", timeout)
			interrupted = true
		} else if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			output += fmt.Sprintf("\n\n[执行错误: %v]", runErr)
			exitCode = -1
		}
	}

	slog.Info("code execution completed",
		"language", language,
		"duration", duration.String(),
		"exitCode", exitCode,
		"outputSize", len(output),
	)

	result, err := json.Marshal(map[string]any{
		"output":      output,
		"exit_code":   exitCode,
		"language":    language,
		"duration":    duration.String(),
		"interrupted": interrupted,
	})
	if err != nil {
		return ToolError(fmt.Sprintf("序列化结果失败: %v", err)), nil
	}

	return string(result), nil
}

// findRuntime 查找语言的运行时路径。
func (t *CodeExecuteTool) findRuntime(language string) string {
	switch language {
	case "python":
		for _, name := range []string{"python3", "python"} {
			if p, _ := exec.LookPath(name); p != "" {
				return p
			}
		}
	case "javascript":
		for _, name := range []string{"node", "nodejs"} {
			if p, _ := exec.LookPath(name); p != "" {
				return p
			}
		}
	case "bash":
		if p, _ := exec.LookPath("bash"); p != "" {
			return p
		}
	case "sh":
		if p, _ := exec.LookPath("sh"); p != "" {
			return p
		}
	}
	return ""
}

// fileExtension 返回语言对应的文件扩展名。
func (t *CodeExecuteTool) fileExtension(language string) string {
	switch language {
	case "python":
		return ".py"
	case "javascript":
		return ".js"
	case "bash", "sh":
		return ".sh"
	default:
		return ".txt"
	}
}

// ───────────────────────────── init 注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&CodeExecuteTool{})
}
