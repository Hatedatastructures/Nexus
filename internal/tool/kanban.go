// Package tool 提供看板 (Kanban) 多智能体协调工具。
// 用于任务编排: 创建、分配、完成、阻塞子任务，
// 支持父子依赖、心跳续约和状态追踪。
package tool

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
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
	StatusTriage  TaskStatus = "triage"
	StatusTodo    TaskStatus = "todo"
	StatusReady   TaskStatus = "ready"
	StatusRunning TaskStatus = "running"
	StatusBlocked TaskStatus = "blocked"
	StatusDone    TaskStatus = "done"
	StatusArchived TaskStatus = "archived"
)

// KanbanTask 看板任务
type KanbanTask struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Body        string            `json:"body,omitempty"`
	Assignee    string            `json:"assignee"`
	Status      TaskStatus        `json:"status"`
	Priority    int               `json:"priority,omitempty"`
	Parents     []string          `json:"parents,omitempty"`
	Children    []string          `json:"children,omitempty"`
	Comments    []KanbanComment   `json:"comments,omitempty"`
	Events      []KanbanEvent     `json:"events,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	LastHeartbeat time.Time       `json:"last_heartbeat,omitempty"`
	Metadata    map[string]any    `json:"metadata,omitempty"`
	Board       string            `json:"board,omitempty"`
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
	// 只允许字母、数字、下划线、短横线
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
		tmpFile.Close()
		os.Remove(tmpPath)
		return err
	}
	tmpFile.Close()
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}

// ───────────────────────────── 看板列表工具 ─────────────────────────────

// KanbanListTool 列出看板任务 (编排者专用)
type KanbanListTool struct{}

func (t *KanbanListTool) Name() string        { return "kanban_list" }
func (t *KanbanListTool) Description() string  { return "列出看板任务，支持按状态、分配者筛选。" }
func (t *KanbanListTool) Toolset() string      { return "kanban" }
func (t *KanbanListTool) Emoji() string        { return "📋" }
func (t *KanbanListTool) IsAvailable() bool    { return true }
func (t *KanbanListTool) MaxResultChars() int  { return 20000 }

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

	tasks, err := store.loadBoard(board)
	if err != nil {
		return ToolError(fmt.Sprintf("加载看板失败: %v", err)), nil
	}

	// 检查 ready 状态提升 (所有 parent 完成的 todo 任务提升为 ready)
	promoteReadyTasks(tasks)
	if err := store.saveBoard(board, tasks); err != nil {
		slog.Warn("kanban: failed to save board after promotion", "err", err)
	}

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

func (t *KanbanShowTool) Name() string        { return "kanban_show" }
func (t *KanbanShowTool) Description() string  { return "查看指定看板任务的完整状态和上下文。" }
func (t *KanbanShowTool) Toolset() string      { return "kanban" }
func (t *KanbanShowTool) Emoji() string        { return "🔍" }
func (t *KanbanShowTool) IsAvailable() bool    { return true }
func (t *KanbanShowTool) MaxResultChars() int  { return 20000 }

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
	tasks, err := store.loadBoard(board)
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

// ───────────────────────────── 看板创建工具 ─────────────────────────────

// KanbanCreateTool 创建新的子任务
type KanbanCreateTool struct{}

func (t *KanbanCreateTool) Name() string        { return "kanban_create" }
func (t *KanbanCreateTool) Description() string  { return "在看板上创建新的子任务，支持父子依赖。" }
func (t *KanbanCreateTool) Toolset() string      { return "kanban" }
func (t *KanbanCreateTool) Emoji() string        { return "➕" }
func (t *KanbanCreateTool) IsAvailable() bool    { return true }
func (t *KanbanCreateTool) MaxResultChars() int  { return 10000 }

func (t *KanbanCreateTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "kanban_create",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title": map[string]any{
					"type":        "string",
					"description": "任务标题",
				},
				"assignee": map[string]any{
					"type":        "string",
					"description": "分配给谁",
				},
				"body": map[string]any{
					"type":        "string",
					"description": "任务描述",
				},
				"parents": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "父任务 ID 列表",
				},
				"priority": map[string]any{
					"type":        "integer",
					"description": "优先级 (数字越大越优先)",
				},
				"board": map[string]any{
					"type":        "string",
					"description": "看板名称",
				},
			},
			"required": []string{"title", "assignee"},
		},
	}
}

func (t *KanbanCreateTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	title := getKanbanStringFromArgs(args, "title")
	if title == "" {
		return ToolError("title 是必填项"), nil
	}
	assignee := getKanbanStringFromArgs(args, "assignee")
	if assignee == "" {
		return ToolError("assignee 是必填项"), nil
	}

	store := getKanbanStore()
	board := getKanbanStringFromArgs(args, "board")
	tasks, err := store.loadBoard(board)
	if err != nil {
		return ToolError(fmt.Sprintf("加载看板失败: %v", err)), nil
	}

	body := getKanbanStringFromArgs(args, "body")
	priority := getKanbanIntFromArgs(args, "priority", 0)

	var parents []string
	if p, ok := args["parents"].([]any); ok {
		for _, v := range p {
			if s, ok := v.(string); ok {
				parents = append(parents, s)
			}
		}
	}

	now := time.Now()
	id := generateTaskID()

	// 有父任务时初始状态为 todo，否则为 ready
	status := StatusReady
	if len(parents) > 0 {
		status = StatusTodo
	}

	task := &KanbanTask{
		ID:        id,
		Title:     title,
		Body:      body,
		Assignee:  assignee,
		Status:    status,
		Priority:  priority,
		Parents:   parents,
		Comments:  []KanbanComment{},
		Events: []KanbanEvent{
			{Type: "created", Timestamp: now, Detail: "任务创建"},
		},
		CreatedAt: now,
		UpdatedAt: now,
		Board:     board,
	}

	tasks[id] = task

	// 将此任务添加到父任务的 Children 中
	for _, pid := range parents {
		if parent, ok := tasks[pid]; ok {
			parent.Children = append(parent.Children, id)
		}
	}

	if err := store.saveBoard(board, tasks); err != nil {
		return ToolError(fmt.Sprintf("保存看板失败: %v", err)), nil
	}

	return ToolResult(map[string]any{
		"success":  true,
		"task_id":  id,
		"status":   string(status),
		"message":  fmt.Sprintf("任务 %q 已创建", title),
	}), nil
}

// ───────────────────────────── 看板完成工具 ─────────────────────────────

// KanbanCompleteTool 标记任务为完成
type KanbanCompleteTool struct{}

func (t *KanbanCompleteTool) Name() string        { return "kanban_complete" }
func (t *KanbanCompleteTool) Description() string  { return "标记看板任务为已完成，需提供摘要或结果。" }
func (t *KanbanCompleteTool) Toolset() string      { return "kanban" }
func (t *KanbanCompleteTool) Emoji() string        { return "✅" }
func (t *KanbanCompleteTool) IsAvailable() bool    { return true }
func (t *KanbanCompleteTool) MaxResultChars() int  { return 10000 }

func (t *KanbanCompleteTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "kanban_complete",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "任务 ID",
				},
				"summary": map[string]any{
					"type":        "string",
					"description": "完成摘要",
				},
				"result": map[string]any{
					"type":        "string",
					"description": "任务结果",
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

func (t *KanbanCompleteTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	taskID := getKanbanStringFromArgs(args, "task_id")
	if taskID == "" {
		return ToolError("task_id 是必填项"), nil
	}

	summary := getKanbanStringFromArgs(args, "summary")
	result := getKanbanStringFromArgs(args, "result")
	if summary == "" && result == "" {
		return ToolError("需要提供 summary 或 result 中的至少一个"), nil
	}

	store := getKanbanStore()
	board := getKanbanStringFromArgs(args, "board")
	tasks, err := store.loadBoard(board)
	if err != nil {
		return ToolError(fmt.Sprintf("加载看板失败: %v", err)), nil
	}

	task, ok := tasks[taskID]
	if !ok {
		return ToolError(fmt.Sprintf("任务 %q 不存在", taskID)), nil
	}

	now := time.Now()
	task.Status = StatusDone
	task.UpdatedAt = now
	task.Events = append(task.Events, KanbanEvent{
		Type: "completed", Timestamp: now,
		Detail: fmt.Sprintf("summary=%s result=%s", summary, result),
	})

	// 检查子任务是否可以提升为 ready
	promoteReadyTasks(tasks)

	if err := store.saveBoard(board, tasks); err != nil {
		return ToolError(fmt.Sprintf("保存看板失败: %v", err)), nil
	}

	return ToolResult(map[string]any{
		"success": true,
		"task_id": taskID,
		"status":  string(StatusDone),
	}), nil
}

// ───────────────────────────── 看板阻塞工具 ─────────────────────────────

// KanbanBlockTool 将任务标记为阻塞
type KanbanBlockTool struct{}

func (t *KanbanBlockTool) Name() string        { return "kanban_block" }
func (t *KanbanBlockTool) Description() string  { return "将看板任务标记为阻塞状态，需提供阻塞原因。" }
func (t *KanbanBlockTool) Toolset() string      { return "kanban" }
func (t *KanbanBlockTool) Emoji() string        { return "🚫" }
func (t *KanbanBlockTool) IsAvailable() bool    { return true }
func (t *KanbanBlockTool) MaxResultChars() int  { return 10000 }

func (t *KanbanBlockTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "kanban_block",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "任务 ID",
				},
				"reason": map[string]any{
					"type":        "string",
					"description": "阻塞原因",
				},
				"board": map[string]any{
					"type":        "string",
					"description": "看板名称",
				},
			},
			"required": []string{"task_id", "reason"},
		},
	}
}

func (t *KanbanBlockTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	taskID := getKanbanStringFromArgs(args, "task_id")
	reason := getKanbanStringFromArgs(args, "reason")
	if taskID == "" {
		return ToolError("task_id 是必填项"), nil
	}
	if reason == "" {
		return ToolError("reason 是必填项"), nil
	}

	store := getKanbanStore()
	board := getKanbanStringFromArgs(args, "board")
	tasks, err := store.loadBoard(board)
	if err != nil {
		return ToolError(fmt.Sprintf("加载看板失败: %v", err)), nil
	}

	task, ok := tasks[taskID]
	if !ok {
		return ToolError(fmt.Sprintf("任务 %q 不存在", taskID)), nil
	}

	now := time.Now()
	task.Status = StatusBlocked
	task.UpdatedAt = now
	task.Events = append(task.Events, KanbanEvent{
		Type: "blocked", Timestamp: now, Detail: reason,
	})

	if err := store.saveBoard(board, tasks); err != nil {
		return ToolError(fmt.Sprintf("保存看板失败: %v", err)), nil
	}

	return ToolResult(map[string]any{
		"success": true,
		"task_id": taskID,
		"status":  string(StatusBlocked),
		"reason":  reason,
	}), nil
}

// ───────────────────────────── 看板解除阻塞工具 ─────────────────────────────

// KanbanUnblockTool 解除任务的阻塞状态 (编排者专用)
type KanbanUnblockTool struct{}

func (t *KanbanUnblockTool) Name() string        { return "kanban_unblock" }
func (t *KanbanUnblockTool) Description() string  { return "解除看板任务的阻塞状态，将其恢复为 ready。" }
func (t *KanbanUnblockTool) Toolset() string      { return "kanban" }
func (t *KanbanUnblockTool) Emoji() string        { return "🔓" }
func (t *KanbanUnblockTool) IsAvailable() bool    { return true }
func (t *KanbanUnblockTool) MaxResultChars() int  { return 10000 }

func (t *KanbanUnblockTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "kanban_unblock",
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

func (t *KanbanUnblockTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	taskID := getKanbanStringFromArgs(args, "task_id")
	if taskID == "" {
		return ToolError("task_id 是必填项"), nil
	}

	store := getKanbanStore()
	board := getKanbanStringFromArgs(args, "board")
	tasks, err := store.loadBoard(board)
	if err != nil {
		return ToolError(fmt.Sprintf("加载看板失败: %v", err)), nil
	}

	task, ok := tasks[taskID]
	if !ok {
		return ToolError(fmt.Sprintf("任务 %q 不存在", taskID)), nil
	}

	if task.Status != StatusBlocked {
		return ToolError(fmt.Sprintf("任务 %q 当前状态为 %s，非 blocked", taskID, task.Status)), nil
	}

	now := time.Now()
	task.Status = StatusReady
	task.UpdatedAt = now
	task.Events = append(task.Events, KanbanEvent{
		Type: "unblocked", Timestamp: now, Detail: "解除阻塞",
	})

	if err := store.saveBoard(board, tasks); err != nil {
		return ToolError(fmt.Sprintf("保存看板失败: %v", err)), nil
	}

	return ToolResult(map[string]any{
		"success": true,
		"task_id": taskID,
		"status":  string(StatusReady),
	}), nil
}

// ───────────────────────────── 看板心跳工具 ─────────────────────────────

// KanbanHeartbeatTool 发送任务心跳 (延长任务占用)
type KanbanHeartbeatTool struct{}

func (t *KanbanHeartbeatTool) Name() string        { return "kanban_heartbeat" }
func (t *KanbanHeartbeatTool) Description() string  { return "发送任务心跳，延长任务的执行占用时间。" }
func (t *KanbanHeartbeatTool) Toolset() string      { return "kanban" }
func (t *KanbanHeartbeatTool) Emoji() string        { return "💓" }
func (t *KanbanHeartbeatTool) IsAvailable() bool    { return true }
func (t *KanbanHeartbeatTool) MaxResultChars() int  { return 5000 }

func (t *KanbanHeartbeatTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "kanban_heartbeat",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "任务 ID",
				},
				"note": map[string]any{
					"type":        "string",
					"description": "心跳备注",
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

func (t *KanbanHeartbeatTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	taskID := getKanbanStringFromArgs(args, "task_id")
	if taskID == "" {
		return ToolError("task_id 是必填项"), nil
	}

	store := getKanbanStore()
	board := getKanbanStringFromArgs(args, "board")
	note := getKanbanStringFromArgs(args, "note")
	tasks, err := store.loadBoard(board)
	if err != nil {
		return ToolError(fmt.Sprintf("加载看板失败: %v", err)), nil
	}

	task, ok := tasks[taskID]
	if !ok {
		return ToolError(fmt.Sprintf("任务 %q 不存在", taskID)), nil
	}

	now := time.Now()
	task.LastHeartbeat = now
	task.UpdatedAt = now
	detail := "心跳续约"
	if note != "" {
		detail += ": " + note
	}
	task.Events = append(task.Events, KanbanEvent{
		Type: "heartbeat", Timestamp: now, Detail: detail,
	})

	if err := store.saveBoard(board, tasks); err != nil {
		return ToolError(fmt.Sprintf("保存看板失败: %v", err)), nil
	}

	return ToolResult(map[string]any{
		"success":         true,
		"task_id":         taskID,
		"last_heartbeat":  now.Format(time.RFC3339),
	}), nil
}

// ───────────────────────────── 看板评论工具 ─────────────────────────────

// KanbanCommentTool 给任务添加评论
type KanbanCommentTool struct{}

func (t *KanbanCommentTool) Name() string        { return "kanban_comment" }
func (t *KanbanCommentTool) Description() string  { return "给看板任务添加评论。" }
func (t *KanbanCommentTool) Toolset() string      { return "kanban" }
func (t *KanbanCommentTool) Emoji() string        { return "💬" }
func (t *KanbanCommentTool) IsAvailable() bool    { return true }
func (t *KanbanCommentTool) MaxResultChars() int  { return 10000 }

func (t *KanbanCommentTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "kanban_comment",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "任务 ID",
				},
				"body": map[string]any{
					"type":        "string",
					"description": "评论内容",
				},
				"board": map[string]any{
					"type":        "string",
					"description": "看板名称",
				},
			},
			"required": []string{"task_id", "body"},
		},
	}
}

func (t *KanbanCommentTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	taskID := getKanbanStringFromArgs(args, "task_id")
	body := getKanbanStringFromArgs(args, "body")
	if taskID == "" {
		return ToolError("task_id 是必填项"), nil
	}
	if body == "" {
		return ToolError("body 是必填项"), nil
	}

	store := getKanbanStore()
	board := getKanbanStringFromArgs(args, "board")
	tasks, err := store.loadBoard(board)
	if err != nil {
		return ToolError(fmt.Sprintf("加载看板失败: %v", err)), nil
	}

	task, ok := tasks[taskID]
	if !ok {
		return ToolError(fmt.Sprintf("任务 %q 不存在", taskID)), nil
	}

	author := os.Getenv("HERMES_PROFILE")
	if author == "" {
		author = "agent"
	}

	now := time.Now()
	task.Comments = append(task.Comments, KanbanComment{
		Author:    author,
		Body:      body,
		CreatedAt: now,
	})
	task.UpdatedAt = now
	task.Events = append(task.Events, KanbanEvent{
		Type: "comment", Timestamp: now,
		Detail: fmt.Sprintf("%s: %s", author, body),
	})

	if err := store.saveBoard(board, tasks); err != nil {
		return ToolError(fmt.Sprintf("保存看板失败: %v", err)), nil
	}

	return ToolResult(map[string]any{
		"success":    true,
		"task_id":    taskID,
		"comment_count": len(task.Comments),
	}), nil
}

// ───────────────────────────── 看板链接工具 ─────────────────────────────

// KanbanLinkTool 建立任务间的父子依赖关系
type KanbanLinkTool struct{}

func (t *KanbanLinkTool) Name() string        { return "kanban_link" }
func (t *KanbanLinkTool) Description() string  { return "建立看板任务间的父子依赖关系。" }
func (t *KanbanLinkTool) Toolset() string      { return "kanban" }
func (t *KanbanLinkTool) Emoji() string        { return "🔗" }
func (t *KanbanLinkTool) IsAvailable() bool    { return true }
func (t *KanbanLinkTool) MaxResultChars() int  { return 5000 }

func (t *KanbanLinkTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "kanban_link",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"parent_id": map[string]any{
					"type":        "string",
					"description": "父任务 ID",
				},
				"child_id": map[string]any{
					"type":        "string",
					"description": "子任务 ID",
				},
				"board": map[string]any{
					"type":        "string",
					"description": "看板名称",
				},
			},
			"required": []string{"parent_id", "child_id"},
		},
	}
}

func (t *KanbanLinkTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	parentID := getKanbanStringFromArgs(args, "parent_id")
	childID := getKanbanStringFromArgs(args, "child_id")
	if parentID == "" || childID == "" {
		return ToolError("parent_id 和 child_id 都是必填项"), nil
	}
	if parentID == childID {
		return ToolError("不能将任务链接到自身"), nil
	}

	store := getKanbanStore()
	board := getKanbanStringFromArgs(args, "board")
	tasks, err := store.loadBoard(board)
	if err != nil {
		return ToolError(fmt.Sprintf("加载看板失败: %v", err)), nil
	}

	parent, ok := tasks[parentID]
	if !ok {
		return ToolError(fmt.Sprintf("父任务 %q 不存在", parentID)), nil
	}
	child, ok := tasks[childID]
	if !ok {
		return ToolError(fmt.Sprintf("子任务 %q 不存在", childID)), nil
	}

	// 检查循环依赖
	if hasCycle(tasks, parentID, childID) {
		return ToolError("添加此依赖会导致循环引用"), nil
	}

	parent.Children = append(parent.Children, childID)
	child.Parents = append(child.Parents, parentID)

	promoteReadyTasks(tasks)

	if err := store.saveBoard(board, tasks); err != nil {
		return ToolError(fmt.Sprintf("保存看板失败: %v", err)), nil
	}

	return ToolResult(map[string]any{
		"success":   true,
		"parent_id": parentID,
		"child_id":  childID,
	}), nil
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

// promoteReadyTasks 将所有父任务已完成的 todo 任务提升为 ready
func promoteReadyTasks(tasks map[string]*KanbanTask) {
	for _, task := range tasks {
		if task.Status != StatusTodo {
			continue
		}
		allParentsDone := true
		for _, pid := range task.Parents {
			if parent, ok := tasks[pid]; ok && parent.Status != StatusDone {
				allParentsDone = false
				break
			}
		}
		if allParentsDone && len(task.Parents) > 0 {
			task.Status = StatusReady
			task.UpdatedAt = time.Now()
		}
	}
}

// hasCycle 检查从 child 向上遍历是否可达 parent (间接循环)
func hasCycle(tasks map[string]*KanbanTask, parentID, childID string) bool {
	visited := map[string]bool{}
	var walk func(id string) bool
	walk = func(id string) bool {
		if id == childID {
			return true
		}
		if visited[id] {
			return false
		}
		visited[id] = true
		task, ok := tasks[id]
		if !ok {
			return false
		}
		for _, pid := range task.Parents {
			if walk(pid) {
				return true
			}
		}
		return false
	}
	return walk(parentID)
}

// getKanbanIntFromArgs 从参数中获取整数值 (带默认值)
func getKanbanIntFromArgs(args map[string]any, key string, defaultVal int) int {
	switch v := args[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return int(i)
		}
	}
	return defaultVal
}

// getKanbanStringFromArgs 从参数中获取字符串值
func getKanbanStringFromArgs(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return strings.TrimSpace(v)
}

// ───────────────────────────── init 注册 ─────────────────────────────

func init() {
	slog.Debug("registering kanban tool")
	GetRegistry().Register(&KanbanListTool{})
	GetRegistry().Register(&KanbanShowTool{})
	GetRegistry().Register(&KanbanCreateTool{})
	GetRegistry().Register(&KanbanCompleteTool{})
	GetRegistry().Register(&KanbanBlockTool{})
	GetRegistry().Register(&KanbanUnblockTool{})
	GetRegistry().Register(&KanbanHeartbeatTool{})
	GetRegistry().Register(&KanbanCommentTool{})
	GetRegistry().Register(&KanbanLinkTool{})
}
