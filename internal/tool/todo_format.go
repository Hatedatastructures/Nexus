package tool

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
)

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
	result, err := json.Marshal(map[string]any{
		"output":  fmt.Sprintf("已创建 %d 个待办项", len(created)),
		"created": created,
	})
	if err != nil {
		return ToolError(fmt.Sprintf("序列化结果失败: %v", err)), nil
	}
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
	result, err := json.Marshal(map[string]any{
		"output":  fmt.Sprintf("已更新 %d 个待办项", len(results)),
		"updated": results,
	})
	if err != nil {
		return ToolError(fmt.Sprintf("序列化结果失败: %v", err)), nil
	}
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
	result, err := json.Marshal(map[string]any{
		"output":  fmt.Sprintf("已删除 %d 个待办项", len(ids)),
		"deleted": results,
	})
	if err != nil {
		return ToolError(fmt.Sprintf("序列化结果失败: %v", err)), nil
	}
	return string(result), nil
}

// handleList 处理列表操作。
func (t *TodoTool) handleList(store *TodoStore) (string, error) {
	items := store.List()
	result, err := json.Marshal(map[string]any{
		"output": fmt.Sprintf("共 %d 个待办项", len(items)),
		"items":  items,
	})
	if err != nil {
		return ToolError(fmt.Sprintf("序列化结果失败: %v", err)), nil
	}
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

	result, err := json.Marshal(map[string]any{
		"output":  fmt.Sprintf("已更新 %d 个依赖关系", len(results)),
		"results": results,
	})
	if err != nil {
		return ToolError(fmt.Sprintf("序列化结果失败: %v", err)), nil
	}
	return string(result), nil
}
