package tool

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// generateTaskID 生成不可预测的任务 ID。
func generateTaskID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("task-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("task-%x", b)
}

// ───────────────────────────── 看板数据模型 ─────────────────────────────

// TaskStatus 任务状态
type TaskStatus string

const (
	StatusTriage   TaskStatus = "triage"
	StatusTodo     TaskStatus = "todo"
	StatusReady    TaskStatus = "ready"
	StatusRunning  TaskStatus = "running"
	StatusBlocked  TaskStatus = "blocked"
	StatusDone     TaskStatus = "done"
	StatusArchived TaskStatus = "archived"
)

// KanbanTask 看板任务
type KanbanTask struct {
	ID            string          `json:"id"`
	Title         string          `json:"title"`
	Body          string          `json:"body,omitempty"`
	Assignee      string          `json:"assignee"`
	Status        TaskStatus      `json:"status"`
	Priority      int             `json:"priority,omitempty"`
	Parents       []string        `json:"parents,omitempty"`
	Children      []string        `json:"children,omitempty"`
	Comments      []KanbanComment `json:"comments,omitempty"`
	Events        []KanbanEvent   `json:"events,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	LastHeartbeat time.Time       `json:"last_heartbeat,omitempty"`
	Metadata      map[string]any  `json:"metadata,omitempty"`
	Board         string          `json:"board,omitempty"`
}

// KanbanComment 任务评论
type KanbanComment struct {
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

// KanbanEvent 任务事件
type KanbanEvent struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Detail    string    `json:"detail,omitempty"`
}

// ───────────────────────────── 看板存储 ─────────────────────────────

// KanbanStore 看板存储 (文件持久化, 全局单例)
type KanbanStore struct {
	mu       sync.Mutex
	boardDir string
}

var globalKanbanStore *KanbanStore
var kanbanStoreOnce sync.Once

func getKanbanStore() *KanbanStore {
	kanbanStoreOnce.Do(func() {
		home, _ := os.UserHomeDir()
		dir := filepath.Join(home, ".nexus", "kanban")
		globalKanbanStore = &KanbanStore{boardDir: dir}
	})
	return globalKanbanStore
}

// sanitizeBoardName 清理看板名称，防止路径遍历
func sanitizeBoardName(board string) string {
	if board == "" {
		return "default"
	}
	var b strings.Builder
	for _, r := range board {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	name := b.String()
	if name == "" {
		return "default"
	}
	return name
}

func (s *KanbanStore) boardPath(board string) string {
	board = sanitizeBoardName(board)
	return filepath.Join(s.boardDir, board+".json")
}

// Lock 获取看板存储的互斥锁，用于保护 load-modify-save 原子操作。
func (s *KanbanStore) Lock()   { s.mu.Lock() }
func (s *KanbanStore) Unlock() { s.mu.Unlock() }

func (s *KanbanStore) loadBoard(board string) (map[string]*KanbanTask, error) {
	data, err := os.ReadFile(s.boardPath(board))
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]*KanbanTask), nil
		}
		return nil, err
	}
	var tasks map[string]*KanbanTask
	if err := json.Unmarshal(data, &tasks); err != nil {
		return nil, err
	}
	return tasks, nil
}

func (s *KanbanStore) saveBoard(board string, tasks map[string]*KanbanTask) error {
	if err := os.MkdirAll(s.boardDir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(tasks, "", "  ")
	if err != nil {
		return err
	}
	// 原子写入: 先写临时文件再重命名
	path := s.boardPath(board)
	tmpFile, err := os.CreateTemp(s.boardDir, ".kanban_*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	_ = tmpFile.Close()
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

// ───────────────────────────── 看板列表工具 ─────────────────────────────

// KanbanListTool 列出看板任务 (编排者专用)
type KanbanListTool struct{}

func (t *KanbanListTool) Name() string { return "kanban_list" }
func (t *KanbanListTool) Description() string {
	return "列出看板任务，支持按状态、分配者筛选。"
}
func (t *KanbanListTool) Toolset() string     { return "kanban" }
func (t *KanbanListTool) Emoji() string       { return "📋" }
func (t *KanbanListTool) IsAvailable() bool   { return true }
func (t *KanbanListTool) MaxResultChars() int { return 20000 }

func (t *KanbanListTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "kanban_list",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"status": map[string]any{
					"type":        "string",
					"enum":        []string{"triage", "todo", "ready", "running", "blocked", "done", "archived"},
					"description": "按状态筛选",
				},
				"assignee": map[string]any{
					"type":        "string",
					"description": "按分配者筛选",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "返回数量上限 (默认 50, 最大 200)",
				},
				"board": map[string]any{
					"type":        "string",
					"description": "看板名称 (默认 default)",
				},
			},
		},
	}
}

func (t *KanbanListTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	store := getKanbanStore()
	board := getKanbanStringFromArgs(args, "board")
	statusFilter := getKanbanStringFromArgs(args, "status")
	assigneeFilter := getKanbanStringFromArgs(args, "assignee")
	limit := getKanbanIntFromArgs(args, "limit", 50)
	if limit > 200 {
		limit = 200
	}

	store.Lock()
	tasks, err := store.loadBoard(board)
	if err != nil {
		store.Unlock()
		return ToolError(fmt.Sprintf("加载看板失败: %v", err)), nil
	}

	// 检查 ready 状态提升 (所有 parent 完成的 todo 任务提升为 ready)
	promoteReadyTasks(tasks)

	if err := store.saveBoard(board, tasks); err != nil {
		store.Unlock()
		return ToolError(fmt.Sprintf("保存看板失败: %v", err)), nil
	}
	store.Unlock()

	var rows []map[string]any
	for _, task := range tasks {
		if statusFilter != "" && string(task.Status) != statusFilter {
			continue
		}
		if assigneeFilter != "" && task.Assignee != assigneeFilter {
			continue
		}
		rows = append(rows, map[string]any{
			"id":       task.ID,
			"title":    task.Title,
			"status":   task.Status,
			"assignee": task.Assignee,
			"priority": task.Priority,
			"parents":  len(task.Parents),
			"children": len(task.Children),
		})
	}

	if len(rows) > limit {
		rows = rows[:limit]
	}

	return ToolResult(map[string]any{
		"count": len(rows),
		"tasks": rows,
	}), nil
}

// ───────────────────────────── 看板查看工具 ─────────────────────────────

// KanbanShowTool 查看单个任务详情
type KanbanShowTool struct{}

func (t *KanbanShowTool) Name() string { return "kanban_show" }
func (t *KanbanShowTool) Description() string {
	return "查看指定看板任务的完整状态和上下文。"
}
func (t *KanbanShowTool) Toolset() string     { return "kanban" }
func (t *KanbanShowTool) Emoji() string       { return "🔍" }
func (t *KanbanShowTool) IsAvailable() bool   { return true }
func (t *KanbanShowTool) MaxResultChars() int { return 20000 }

func (t *KanbanShowTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "kanban_show",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "任务 ID",
				},
				"board": map[string]any{
					"type":        "string",
					"description": "看板名称",
				},
			},
			"required": []string{"task_id"},
		},
	}
}

func (t *KanbanShowTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	taskID := getKanbanStringFromArgs(args, "task_id")
	if taskID == "" {
		return ToolError("task_id 是必填项"), nil
	}

	store := getKanbanStore()
	board := getKanbanStringFromArgs(args, "board")
	store.Lock()
	tasks, err := store.loadBoard(board)
	store.Unlock()
	if err != nil {
		return ToolError(fmt.Sprintf("加载看板失败: %v", err)), nil
	}

	task, ok := tasks[taskID]
	if !ok {
		return ToolError(fmt.Sprintf("任务 %q 不存在", taskID)), nil
	}

	return ToolResult(map[string]any{
		"task": task,
	}), nil
}
