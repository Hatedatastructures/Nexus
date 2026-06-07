package tool

import (
	"context"
	"fmt"
	"time"
)

// ───────────────────────────── 看板创建工具 ─────────────────────────────

// KanbanCreateTool 创建新的子任务
type KanbanCreateTool struct{}

func (t *KanbanCreateTool) Name() string { return "kanban_create" }
func (t *KanbanCreateTool) Description() string {
	return "在看板上创建新的子任务，支持父子依赖。"
}
func (t *KanbanCreateTool) Toolset() string     { return "kanban" }
func (t *KanbanCreateTool) Emoji() string       { return "➕" }
func (t *KanbanCreateTool) IsAvailable() bool   { return true }
func (t *KanbanCreateTool) MaxResultChars() int { return 10000 }

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

	store.Lock()
	defer store.Unlock()

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

	status := StatusReady
	if len(parents) > 0 {
		status = StatusTodo
	}

	task := &KanbanTask{
		ID:       id,
		Title:    title,
		Body:     body,
		Assignee: assignee,
		Status:   status,
		Priority: priority,
		Parents:  parents,
		Comments: []KanbanComment{},
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
		"success": true,
		"task_id": id,
		"status":  string(status),
		"message": fmt.Sprintf("任务 %q 已创建", title),
	}), nil
}

// ───────────────────────────── 看板完成工具 ─────────────────────────────

// KanbanCompleteTool 标记任务为完成
type KanbanCompleteTool struct{}

func (t *KanbanCompleteTool) Name() string { return "kanban_complete" }
func (t *KanbanCompleteTool) Description() string {
	return "标记看板任务为已完成，需提供摘要或结果。"
}
func (t *KanbanCompleteTool) Toolset() string     { return "kanban" }
func (t *KanbanCompleteTool) Emoji() string       { return "✅" }
func (t *KanbanCompleteTool) IsAvailable() bool   { return true }
func (t *KanbanCompleteTool) MaxResultChars() int { return 10000 }

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

	store.Lock()
	defer store.Unlock()

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

func (t *KanbanBlockTool) Name() string { return "kanban_block" }
func (t *KanbanBlockTool) Description() string {
	return "将看板任务标记为阻塞状态，需提供阻塞原因。"
}
func (t *KanbanBlockTool) Toolset() string     { return "kanban" }
func (t *KanbanBlockTool) Emoji() string       { return "🚫" }
func (t *KanbanBlockTool) IsAvailable() bool   { return true }
func (t *KanbanBlockTool) MaxResultChars() int { return 10000 }

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

	store.Lock()
	defer store.Unlock()

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

func (t *KanbanUnblockTool) Name() string { return "kanban_unblock" }
func (t *KanbanUnblockTool) Description() string {
	return "解除看板任务的阻塞状态，将其恢复为 ready。"
}
func (t *KanbanUnblockTool) Toolset() string     { return "kanban" }
func (t *KanbanUnblockTool) Emoji() string       { return "🔓" }
func (t *KanbanUnblockTool) IsAvailable() bool   { return true }
func (t *KanbanUnblockTool) MaxResultChars() int { return 10000 }

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

	store.Lock()
	defer store.Unlock()

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
