package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// ───────────────────────────── 看板心跳工具 ─────────────────────────────

// KanbanHeartbeatTool 发送任务心跳 (延长任务占用)
type KanbanHeartbeatTool struct{}

func (t *KanbanHeartbeatTool) Name() string { return "kanban_heartbeat" }
func (t *KanbanHeartbeatTool) Description() string {
	return "发送任务心跳，延长任务的执行占用时间。"
}
func (t *KanbanHeartbeatTool) Toolset() string     { return "kanban" }
func (t *KanbanHeartbeatTool) Emoji() string       { return "💓" }
func (t *KanbanHeartbeatTool) IsAvailable() bool   { return true }
func (t *KanbanHeartbeatTool) MaxResultChars() int { return 5000 }

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
		"success":        true,
		"task_id":        taskID,
		"last_heartbeat": now.Format(time.RFC3339),
	}), nil
}

// ───────────────────────────── 看板评论工具 ─────────────────────────────

// KanbanCommentTool 给任务添加评论
type KanbanCommentTool struct{}

func (t *KanbanCommentTool) Name() string        { return "kanban_comment" }
func (t *KanbanCommentTool) Description() string { return "给看板任务添加评论。" }
func (t *KanbanCommentTool) Toolset() string     { return "kanban" }
func (t *KanbanCommentTool) Emoji() string       { return "💬" }
func (t *KanbanCommentTool) IsAvailable() bool   { return true }
func (t *KanbanCommentTool) MaxResultChars() int { return 10000 }

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
		"success":       true,
		"task_id":       taskID,
		"comment_count": len(task.Comments),
	}), nil
}

// ───────────────────────────── 看板链接工具 ─────────────────────────────

// KanbanLinkTool 建立任务间的父子依赖关系
type KanbanLinkTool struct{}

func (t *KanbanLinkTool) Name() string        { return "kanban_link" }
func (t *KanbanLinkTool) Description() string { return "建立看板任务间的父子依赖关系。" }
func (t *KanbanLinkTool) Toolset() string     { return "kanban" }
func (t *KanbanLinkTool) Emoji() string       { return "🔗" }
func (t *KanbanLinkTool) IsAvailable() bool   { return true }
func (t *KanbanLinkTool) MaxResultChars() int { return 5000 }

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

	store.Lock()
	defer store.Unlock()

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
