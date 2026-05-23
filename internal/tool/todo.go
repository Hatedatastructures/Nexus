// Package tool 提供待办列表工具。
// 使用简单的内存存储管理任务项，
// 支持创建、更新、删除和列表操作。
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

// ───────────────────────────── 待办项 ─────────────────────────────

// TodoItem 表示一个待办项。
type TodoItem struct {
	ID          int    `json:"id"`          // 唯一标识
	Task        string `json:"task"`        // 任务标题 (简短祈使句)
	Description string `json:"description"` // 详细描述 (可选)
	Status      string `json:"status"`      // 状态: pending, in_progress, completed, cancelled
	Owner       string `json:"owner"`       // 负责人 (可选)
	Blocks      []int  `json:"blocks"`      // 此任务阻塞的任务 ID
	BlockedBy   []int  `json:"blockedBy"`   // 阻塞此任务的任务 ID
}

// ───────────────────────────── 内存存储 ─────────────────────────────

// TodoStore 是并发安全的内存待办存储。
type TodoStore struct {
	mu     sync.Mutex
	items  map[int]*TodoItem
	nextID int
}

// NewTodoStore 创建新的待办存储。
func NewTodoStore() *TodoStore {
	return &TodoStore{
		items:  make(map[int]*TodoItem),
		nextID: 1,
	}
}

// Create 创建新的待办项。
func (s *TodoStore) Create(task string) *TodoItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	item := &TodoItem{
		ID:     s.nextID,
		Task:   task,
		Status: "pending",
	}
	s.items[s.nextID] = item
	s.nextID++
	return item
}

// CreateWithDetail 创建带描述和依赖关系的待办项。
func (s *TodoStore) CreateWithDetail(task, description, owner string, blockedBy []int) *TodoItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	item := &TodoItem{
		ID:          s.nextID,
		Task:        task,
		Description: description,
		Status:      "pending",
		Owner:       owner,
		BlockedBy:   blockedBy,
	}
	s.items[s.nextID] = item
	s.nextID++

	// 更新阻塞关系: 被依赖的 item 的 Blocks 包含新 item
	for _, depID := range blockedBy {
		if dep, ok := s.items[depID]; ok {
			dep.Blocks = append(dep.Blocks, item.ID)
		}
	}

	return item
}

// Update 更新待办项。
func (s *TodoStore) Update(id int, status string) (*TodoItem, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[id]
	if !ok {
		return nil, false
	}
	if status != "" {
		item.Status = status
	}
	return item, true
}

// Delete 删除待办项。
func (s *TodoStore) Delete(id int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[id]; !ok {
		return false
	}

	// 清理依赖关系
	item := s.items[id]
	for _, depID := range item.BlockedBy {
		if dep, ok := s.items[depID]; ok {
			newBlocks := make([]int, 0, len(dep.Blocks))
			for _, b := range dep.Blocks {
				if b != id {
					newBlocks = append(newBlocks, b)
				}
			}
			dep.Blocks = newBlocks
		}
	}

	delete(s.items, id)
	return true
}

// List 列出所有待办项。
func (s *TodoStore) List() []*TodoItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]*TodoItem, 0, len(s.items))
	for _, item := range s.items {
		result = append(result, item)
	}
	return result
}

// AddBlocks 设置任务阻塞关系。
func (s *TodoStore) AddBlocks(taskID int, blocksTaskIDs []int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.items[taskID]
	if !ok {
		return false
	}
	for _, bid := range blocksTaskIDs {
		if _, ok := s.items[bid]; !ok {
			continue
		}
		// 避免重复
		found := false
		for _, b := range task.Blocks {
			if b == bid {
				found = true
				break
			}
		}
		if !found {
			task.Blocks = append(task.Blocks, bid)
			s.items[bid].BlockedBy = append(s.items[bid].BlockedBy, taskID)
		}
	}
	return true
}

// AddBlockedBy 设置任务被阻塞关系。
func (s *TodoStore) AddBlockedBy(taskID int, blockedByIDs []int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.items[taskID]
	if !ok {
		return false
	}
	for _, depID := range blockedByIDs {
		if _, ok := s.items[depID]; !ok {
			continue
		}
		found := false
		for _, b := range task.BlockedBy {
			if b == depID {
				found = true
				break
			}
		}
		if !found {
			task.BlockedBy = append(task.BlockedBy, depID)
			s.items[depID].Blocks = append(s.items[depID].Blocks, taskID)
		}
	}
	return true
}

// ───────────────────────────── 待办工具 ─────────────────────────────

// 全局待办存储实例
var globalTodoStore = NewTodoStore()

// TodoTool 实现待办列表管理功能。
// 使用内存存储，不跨会话持久化。
type TodoTool struct{}

// Name 返回工具名称。
func (t *TodoTool) Name() string { return "todo" }

// Description 返回工具描述。
func (t *TodoTool) Description() string {
	return `管理当前会话的任务列表。支持创建、更新状态、删除、列表和依赖关系操作。

任务支持以下字段:
- task: 简短标题 (祈使句)
- description: 详细描述
- status: pending / in_progress / completed / cancelled
- blocks: 此任务阻塞的任务 ID 列表
- blockedBy: 阻塞此任务的任务 ID 列表`
}

// Toolset 返回工具所属工具集。
func (t *TodoTool) Toolset() string { return "todo" }

// Emoji 返回工具图标。
func (t *TodoTool) Emoji() string { return "📋" }

// IsAvailable 待办工具始终可用。
func (t *TodoTool) IsAvailable() bool { return true }

// MaxResultChars 返回结果最大字符数。
func (t *TodoTool) MaxResultChars() int { return 10000 }

	// Schema 返回工具的 JSON Schema。
func (t *TodoTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "todo",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "操作类型: create (创建), update (更新状态), delete (删除), list (列表), addBlocks (添加阻塞关系), addBlockedBy (添加被阻塞关系)",
					"enum":        []string{"create", "update", "delete", "list", "addBlocks", "addBlockedBy"},
				},
				"items": map[string]any{
					"type":        "array",
					"description": "操作相关的项目数组。create 时为 {task, description?, blockedBy?: [id...]}; update 时为 {id, status?}; delete 时为 [id]; addBlocks/addBlockedBy 时为 {id, targets: [id...]}",
				},
			},
			"required": []string{"action"},
		},
	}
}

// Execute 执行待办操作。
func (t *TodoTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	action, ok := args["action"].(string)
	if !ok || action == "" {
		return ToolError("参数 action 是必填项且必须为字符串"), nil
	}

	store := globalTodoStore

	switch action {
	case "create":
		return t.handleCreate(args, store)
	case "update":
		return t.handleUpdate(args, store)
	case "delete":
		return t.handleDelete(args, store)
	case "list":
		return t.handleList(store)
	case "addBlocks":
		return t.handleAddBlocks(args, store)
	case "addBlockedBy":
		return t.handleAddBlockedBy(args, store)
	default:
		return ToolError(fmt.Sprintf("不支持的 action: %s。支持: create, update, delete, list, addBlocks, addBlockedBy", action)), nil
	}
}

// handleCreate 处理创建操作。
func (t *TodoTool) handleCreate(args map[string]any, store *TodoStore) (string, error) {
	var items []map[string]any

	switch v := args["items"].(type) {
	case []any:
		for _, item := range v {
			switch it := item.(type) {
			case string:
				items = append(items, map[string]any{"task": it})
			case map[string]any:
				items = append(items, it)
			}
		}
	case []string:
		for _, s := range v {
			items = append(items, map[string]any{"task": s})
		}
	case string:
		for _, line := range strings.Split(v, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				items = append(items, map[string]any{"task": line})
			}
		}
	}

	if len(items) == 0 {
		return ToolError("参数 items 不能为空，需要至少一个待办任务"), nil
	}

	var created []map[string]any
	for _, it := range items {
		task, _ := it["task"].(string)
		task = strings.TrimSpace(task)
		if task == "" {
			continue
		}
		desc, _ := it["description"].(string)
		owner, _ := it["owner"].(string)
		var blockedBy []int
		if bb, ok := it["blockedBy"].([]any); ok {
			for _, id := range bb {
				if n, ok := id.(float64); ok {
					blockedBy = append(blockedBy, int(n))
				}
			}
		}

		item := store.CreateWithDetail(task, desc, owner, blockedBy)
		created = append(created, map[string]any{
			"id":          item.ID,
			"task":        item.Task,
			"description": item.Description,
			"status":      item.Status,
			"blockedBy":   item.BlockedBy,
		})
	}

	slog.Info("todo items created", "count", len(created))
	result, _ := json.Marshal(map[string]any{
		"output":  fmt.Sprintf("已创建 %d 个待办项", len(created)),
		"created": created,
	})
	return string(result), nil
}

// handleUpdate 处理更新操作。
func (t *TodoTool) handleUpdate(args map[string]any, store *TodoStore) (string, error) {
	var updates []map[string]any
	switch v := args["items"].(type) {
	case []any:
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				updates = append(updates, m)
			}
		}
	}

	if len(updates) == 0 {
		return ToolError("参数 items 不能为空，格式: [{id: N, status: 'completed'}]"), nil
	}

	var results []map[string]any
	for _, upd := range updates {
		id := 0
		if v, ok := upd["id"].(float64); ok {
			id = int(v)
		}
		status, _ := upd["status"].(string)

		if id == 0 {
			results = append(results, map[string]any{
				"error": "缺少 id 字段",
			})
			continue
		}

		item, ok := store.Update(id, status)
		if !ok {
			results = append(results, map[string]any{
				"id":    id,
				"error": fmt.Sprintf("待办项 %d 不存在", id),
			})
			continue
		}
		results = append(results, map[string]any{
			"id":     item.ID,
			"task":   item.Task,
			"status": item.Status,
		})
	}

	slog.Info("todo items updated", "count", len(results))
	result, _ := json.Marshal(map[string]any{
		"output":  fmt.Sprintf("已更新 %d 个待办项", len(results)),
		"updated": results,
	})
	return string(result), nil
}

// handleDelete 处理删除操作。
func (t *TodoTool) handleDelete(args map[string]any, store *TodoStore) (string, error) {
	var ids []int
	switch v := args["items"].(type) {
	case []any:
		for _, item := range v {
			if n, ok := item.(float64); ok {
				ids = append(ids, int(n))
			}
		}
	}

	if len(ids) == 0 {
		return ToolError("参数 items 不能为空，格式: [id1, id2, ...]"), nil
	}

	var results []map[string]any
	for _, id := range ids {
		if store.Delete(id) {
			results = append(results, map[string]any{
				"id":     id,
				"status": "deleted",
			})
		} else {
			results = append(results, map[string]any{
				"id":    id,
				"error": fmt.Sprintf("待办项 %d 不存在", id),
			})
		}
	}

	slog.Info("todo items deleted", "count", len(results))
	result, _ := json.Marshal(map[string]any{
		"output":  fmt.Sprintf("已删除 %d 个待办项", len(ids)),
		"deleted": results,
	})
	return string(result), nil
}

// handleList 处理列表操作。
func (t *TodoTool) handleList(store *TodoStore) (string, error) {
	items := store.List()
	result, _ := json.Marshal(map[string]any{
		"output": fmt.Sprintf("共 %d 个待办项", len(items)),
		"items":  items,
	})
	return string(result), nil
}

// handleAddBlocks 处理添加阻塞关系。
func (t *TodoTool) handleAddBlocks(args map[string]any, store *TodoStore) (string, error) {
	return t.handleDependencyOp(args, store, "addBlocks")
}

// handleAddBlockedBy 处理添加被阻塞关系。
func (t *TodoTool) handleAddBlockedBy(args map[string]any, store *TodoStore) (string, error) {
	return t.handleDependencyOp(args, store, "addBlockedBy")
}

// handleDependencyOp 处理依赖关系操作。
func (t *TodoTool) handleDependencyOp(args map[string]any, store *TodoStore, op string) (string, error) {
	var ops []map[string]any
	switch v := args["items"].(type) {
	case []any:
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				ops = append(ops, m)
			}
		}
	}

	if len(ops) == 0 {
		return ToolError("参数 items 不能为空，格式: [{id: N, targets: [id1, id2]}]"), nil
	}

	var results []map[string]any
	for _, op_item := range ops {
		id := 0
		if v, ok := op_item["id"].(float64); ok {
			id = int(v)
		}
		var targets []int
		if t, ok := op_item["targets"].([]any); ok {
			for _, tid := range t {
				if n, ok := tid.(float64); ok {
					targets = append(targets, int(n))
				}
			}
		}

		if id == 0 || len(targets) == 0 {
			results = append(results, map[string]any{"error": "缺少 id 或 targets"})
			continue
		}

		var ok bool
		if op == "addBlocks" {
			ok = store.AddBlocks(id, targets)
		} else {
			ok = store.AddBlockedBy(id, targets)
		}

		if !ok {
			results = append(results, map[string]any{"id": id, "error": fmt.Sprintf("任务 %d 不存在", id)})
			continue
		}
		results = append(results, map[string]any{"id": id, "targets": targets, "status": "updated"})
	}

	result, _ := json.Marshal(map[string]any{
		"output":  fmt.Sprintf("已更新 %d 个依赖关系", len(results)),
		"results": results,
	})
	return string(result), nil
}

// ───────────────────────────── init 注册 ─────────────────────────────

func init() {
	GetRegistry().Register(&TodoTool{})
}
