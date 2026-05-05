// Package tool 提供后台进程管理工具。
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// ───────────────────────────── 进程注册表 ─────────────────────────────

// ManagedProcess 表示一个被管理的后台进程。
type ManagedProcess struct {
	PID     int    `json:"pid"`
	Name    string `json:"name"`
	Command string `json:"command"`
	Status  string `json:"status"` // running, stopped, error
	Started int64  `json:"started"`
}

var (
	processRegistry = make(map[int]*ManagedProcess)
	processMu       sync.RWMutex
)

// RegisterProcess 注册一个后台进程。
func RegisterProcess(proc *ManagedProcess) {
	processMu.Lock()
	defer processMu.Unlock()
	processRegistry[proc.PID] = proc
}

// UnregisterProcess 注销一个后台进程。
func UnregisterProcess(pid int) {
	processMu.Lock()
	defer processMu.Unlock()
	delete(processRegistry, pid)
}

// GetProcess 获取一个后台进程。
func GetProcess(pid int) *ManagedProcess {
	processMu.RLock()
	defer processMu.RUnlock()
	return processRegistry[pid]
}

// ListProcesses 列出所有后台进程。
func ListProcesses() []*ManagedProcess {
	processMu.RLock()
	defer processMu.RUnlock()

	procs := make([]*ManagedProcess, 0, len(processRegistry))
	for _, p := range processRegistry {
		procs = append(procs, p)
	}
	return procs
}

// ───────────────────────────── ProcessTool ─────────────────────────────

// ProcessTool 后台进程管理工具。
type ProcessTool struct{}

func (t *ProcessTool) Name() string        { return "process" }
func (t *ProcessTool) Description() string  { return "管理后台进程。支持列出进程、查看状态、终止进程。" }
func (t *ProcessTool) Toolset() string      { return "terminal" }
func (t *ProcessTool) IsAvailable() bool    { return true }
func (t *ProcessTool) Emoji() string        { return "⚙️" }
func (t *ProcessTool) MaxResultChars() int  { return 10000 }

func (t *ProcessTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "操作类型",
					"enum":        []string{"list", "status", "kill"},
				},
				"pid": map[string]any{
					"type":        "integer",
					"description": "进程 ID (status/kill 操作时必填)",
				},
			},
			"required": []string{"action"},
		},
	}
}

func (t *ProcessTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	action := getStringFromArgs(args, "action")
	if action == "" {
		return ToolError("action 参数是必填项"), nil
	}

	switch action {
	case "list":
		return t.listProcesses()
	case "status":
		pid := getIntFromArgs(args, "pid")
		if pid == 0 {
			return ToolError("pid 参数是必填项"), nil
		}
		return t.processStatus(pid)
	case "kill":
		pid := getIntFromArgs(args, "pid")
		if pid == 0 {
			return ToolError("pid 参数是必填项"), nil
		}
		return t.killProcess(pid)
	default:
		return ToolError(fmt.Sprintf("未知操作: %s", action)), nil
	}
}

func (t *ProcessTool) listProcesses() (string, error) {
	procs := ListProcesses()

	if len(procs) == 0 {
		return ToolResult(map[string]any{
			"success": true,
			"message": "无后台进程",
			"count":   0,
		}), nil
	}

	return ToolResult(map[string]any{
		"success":  true,
		"count":    len(procs),
		"processes": procs,
	}), nil
}

func (t *ProcessTool) processStatus(pid int) (string, error) {
	proc := GetProcess(pid)
	if proc == nil {
		return ToolError(fmt.Sprintf("未找到进程 PID: %d", pid)), nil
	}

	// 检查进程是否仍在运行
	if isProcessRunning(pid) {
		proc.Status = "running"
	} else {
		proc.Status = "stopped"
	}

	return ToolResult(map[string]any{
		"success": true,
		"process": proc,
	}), nil
}

func (t *ProcessTool) killProcess(pid int) (string, error) {
	proc := GetProcess(pid)
	if proc == nil {
		return ToolError(fmt.Sprintf("未找到进程 PID: %d", pid)), nil
	}

	// 尝试终止进程
	process, err := os.FindProcess(pid)
	if err != nil {
		return ToolError(fmt.Sprintf("找不到进程: %v", err)), nil
	}

	if err := process.Kill(); err != nil {
		return ToolError(fmt.Sprintf("终止进程失败: %v", err)), nil
	}

	// 更新状态
	proc.Status = "stopped"
	UnregisterProcess(pid)

	return ToolResult(map[string]any{
		"success": true,
		"message": fmt.Sprintf("进程 %d 已终止", pid),
	}), nil
}

func isProcessRunning(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// 在 Unix 上发送信号 0 检查进程是否存在
	err = process.Signal(os.Signal(nil))
	return err == nil
}

// 辅助函数
func getStringFromArgs(args map[string]any, key string) string {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getIntFromArgs(args map[string]any, key string) int {
	if v, ok := args[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		case json.Number:
			i, _ := n.Int64()
			return int(i)
		}
	}
	return 0
}

func init() {
	GetRegistry().Register(&ProcessTool{})
}
