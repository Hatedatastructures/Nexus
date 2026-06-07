package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"nexus-agent/internal/llm"
)

// ───────────────────────────── 工具执行 ─────────────────────────────

func (a *AIAgent) executeToolCalls(ctx context.Context, toolCalls []llm.ToolCall) []toolResult {
	if len(toolCalls) == 0 {
		return nil
	}

	// 解析所有工具调用的参数
	var parseFailed []toolResult
	parsed := make([]toolCall, 0, len(toolCalls))
	for _, tc := range toolCalls {
		args, err := parseToolArguments(tc.Arguments)
		if err != nil {
			slog.Warn("skipping tool call with unparseable arguments",
				"session_id", a.sessionID,
				"tool", tc.Name,
				"call_id", tc.ID,
				"err", err,
			)
			parseFailed = append(parseFailed, toolResult{
				CallID: tc.ID,
				Name:   tc.Name,
				Result: fmt.Sprintf(`{"error": "工具参数解析失败: %s"}`, err.Error()),
				Error:  fmt.Errorf("工具参数解析失败: %w", err),
			})
			continue
		}
		parsed = append(parsed, toolCall{call: tc, args: args})
	}

	if len(parsed) == 0 {
		return parseFailed
	}

	// 判断是否可以并行执行
	shouldParallel := a.shouldParallelize(toolCalls)

	var execResults []toolResult
	if shouldParallel && len(toolCalls) > 1 {
		execResults = a.executeParallel(ctx, parsed)
	} else {
		execResults = a.executeSequential(ctx, parsed)
	}

	return append(parseFailed, execResults...)
}

func (a *AIAgent) executeParallel(ctx context.Context, calls []toolCall) []toolResult {
	results := make([]toolResult, len(calls))
	var wg sync.WaitGroup
	a.mu.Lock()
	toolCb := a.toolCallback
	a.mu.Unlock()

	for i, pc := range calls {
		wg.Add(1)
		go func(idx int, c toolCall) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					slog.Error("tool execution panic in parallel",
						"tool", c.call.Name,
						"panic", r,
					)
					results[idx] = toolResult{
						CallID: c.call.ID,
						Name:   c.call.Name,
						Result: fmt.Sprintf(`{"error": "工具执行发生内部错误: %v"}`, r),
						Error:  fmt.Errorf("工具执行 panic: %v", r),
					}
				}
			}()

			// 命令安全审批（与 executeSequential 保持一致，防止绕过审批检查）
			if a.approvalChecker != nil {
				approved, reason := a.approvalChecker.CheckTool(ctx, c.call.Name, c.args)
				if approved != 0 { // 0 = Approved
					results[idx] = toolResult{
						CallID: c.call.ID,
						Name:   c.call.Name,
						Result: fmt.Sprintf(`{"error": "工具调用被拒绝: %s", "tool": "%s"}`, reason, c.call.Name),
						Error:  fmt.Errorf("审批未通过: %s", reason),
					}
					if toolCb != nil {
						toolCb(c.call.Name, c.args)
					}
					return
				}
			}

			result, err := a.dispatchTool(ctx, c.call.Name, c.args)
			results[idx] = toolResult{
				CallID: c.call.ID,
				Name:   c.call.Name,
				Result: result,
				Error:  err,
			}
			if toolCb != nil {
				toolCb(c.call.Name, c.args)
			}
		}(i, pc)
	}

	wg.Wait()
	return results
}

func (a *AIAgent) executeSequential(ctx context.Context, calls []toolCall) []toolResult {
	results := make([]toolResult, len(calls))

	for i, pc := range calls {
		// 命令安全审批
		if a.approvalChecker != nil {
			approved, reason := a.approvalChecker.CheckTool(ctx, pc.call.Name, pc.args)
			if approved != 0 { // 0 = Approved
				result := fmt.Sprintf(`{"error": "工具调用被拒绝: %s", "tool": "%s"}`, reason, pc.call.Name)
				results[i] = toolResult{
					CallID: pc.call.ID,
					Name:   pc.call.Name,
					Result: result,
					Error:  fmt.Errorf("审批未通过: %s", reason),
				}
				a.mu.Lock()
				toolCb := a.toolCallback
				a.mu.Unlock()

				if toolCb != nil {
					toolCb(pc.call.Name, pc.args)
				}
				continue
			}
		}

		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("tool execution panic",
						"tool", pc.call.Name,
						"panic", r,
					)
					results[i] = toolResult{
						CallID: pc.call.ID,
						Name:   pc.call.Name,
						Result: fmt.Sprintf(`{"error": "工具执行发生内部错误: %v"}`, r),
						Error:  fmt.Errorf("工具执行 panic: %v", r),
					}
				}
			}()
			result, err := a.dispatchTool(ctx, pc.call.Name, pc.args)
			results[i] = toolResult{
				CallID: pc.call.ID,
				Name:   pc.call.Name,
				Result: result,
				Error:  err,
			}

			a.mu.Lock()
			defer a.mu.Unlock()
			toolCb2 := a.toolCallback

			if toolCb2 != nil {
				toolCb2(pc.call.Name, pc.args)
			}
		}()
	}

	return results
}

// applyGuardrails 对工具调用列表应用安全护栏检查。
// 过滤掉被护栏拦截的工具调用，返回允许执行的子集。
func (a *AIAgent) applyGuardrails(toolCalls []llm.ToolCall) []llm.ToolCall {
	var filtered []llm.ToolCall
	for _, tc := range toolCalls {
		args, err := parseToolArguments(tc.Arguments)
		if err != nil {
			slog.Warn("guardrails: skipping tool call with unparseable arguments",
				"session_id", a.sessionID,
				"tool", tc.Name,
				"err", err,
			)
			continue
		}
		allowed, reason := a.guardrails.Check(tc.Name, args)
		if allowed {
			filtered = append(filtered, tc)
		} else {
			slog.Warn("tool call blocked by guardrails",
				"session_id", a.sessionID,
				"tool", tc.Name,
				"reason", reason,
			)
		}
	}
	return filtered
}

func (a *AIAgent) dispatchTool(ctx context.Context, name string, args map[string]any) (string, error) {
	if a.registry == nil {
		return `{"error": "工具注册中心未初始化"}`, fmt.Errorf("工具注册中心未初始化")
	}

	// 文件写入安全检查: 对 file_write/file_edit/patch 工具进行二次防护
	if a.fileSafety != nil && isFileWriteTool(name) {
		if path, ok := args["path"].(string); ok && path != "" {
			// 计算写入内容大小
			var contentSize int64
			if content, ok := args["content"].(string); ok {
				contentSize = int64(len(content))
			}
			if newText, ok := args["new_text"].(string); ok {
				contentSize = int64(len(newText))
			}

			allowed, reason := a.fileSafety.CheckWrite(path, contentSize)
			if !allowed {
				slog.Warn("file write blocked by safety checker",
					"session_id", a.sessionID,
					"tool", name,
					"path", path,
					"reason", reason,
				)
				return fmt.Sprintf(`{"error": "文件写入被安全策略拦截: %s"}`, reason), nil
			}
		}
	}

	result, err := a.registry.Dispatch(ctx, name, args)
	if err != nil {
		return fmt.Sprintf(`{"error": "工具执行失败: %s"}`, err.Error()), err
	}
	return result, nil
}

// isFileWriteTool 判断工具名称是否为文件写入类工具。
func isFileWriteTool(name string) bool {
	switch name {
	case "file_write", "file_edit", "patch":
		return true
	default:
		return false
	}
}

func (a *AIAgent) shouldParallelize(toolCalls []llm.ToolCall) bool {
	if len(toolCalls) <= 1 {
		return false
	}

	parallelSafe := map[string]bool{
		"file_read":        true,
		"file_search":      true,
		"web_search":       true,
		"web_extract":      true,
		"browser_snapshot": true,
		"vision_analyze":   true,
		"skills_list":      true,
		"skill_view":       true,
		"list_directory":   true,
	}

	for _, tc := range toolCalls {
		if !parallelSafe[tc.Name] {
			return false
		}
	}
	return true
}

// ───────────────────────────── 工具参数解析 ─────────────────────────────

func parseToolArguments(argsJSON string) (map[string]any, error) {
	if argsJSON == "" {
		return make(map[string]any), nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		slog.Warn("tool arguments JSON parse failed", "err", err)
		return nil, fmt.Errorf("tool arguments JSON parse failed: %w", err)
	}
	return args, nil
}
