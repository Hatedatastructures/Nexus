// Package agent 集成测试 — 文件安全和错误恢复。
package agent

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"nexus-agent/internal/approval"
	"nexus-agent/internal/llm"
	"nexus-agent/internal/tool"
	"nexus-agent/testutil"
)

// ───────────────────────────── 辅助函数 ─────────────────────────────

// newTestAgent creates an AIAgent instance for integration tests.
func newTestAgent(mock *testutil.MockProvider, extraOpts ...AgentOption) *AIAgent {
	reg := tool.NewRegistry()
	tool.RegisterAllTools(reg)
	baseOpts := []AgentOption{
		WithProvider(mock),
		WithModel("test-model"),
		WithMaxIterations(10),
		WithToolRegistry(reg),
		WithSessionID("integration-test"),
	}
	baseOpts = append(baseOpts, extraOpts...)
	return NewAgent(baseOpts...)
}

// ───────────────────────────── 1. 审批拦截测试 ─────────────────────────────

// TestConversationWithApproval verifies the approval checker correctly intercepts terminal tool calls.
func TestConversationWithApproval(t *testing.T) {
	mock := &testutil.MockProvider{}
	var callCount int32

	mock.CreateChatCompletionFunc = func(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
		n := atomic.AddInt32(&callCount, 1)
		if n == 1 {
			return &llm.ChatResponse{
				StopReason: llm.StopToolUse,
				ToolCalls: []llm.ToolCall{
					{
						ID:        "tc-approval-1",
						Name:      "terminal",
						Arguments: `{"command": "python dangerous_script.py"}`,
					},
				},
			}, nil
		}
		return &llm.ChatResponse{
			StopReason: llm.StopEndTurn,
			Content:    "任务完成",
		}, nil
	}

	checker := approval.NewChecker("always", nil, nil)

	agent := newTestAgent(mock,
		WithApprovalChecker(checker),
		WithGuardrails(nil),
	)

	result, err := agent.RunConversation(context.Background(), "执行一个命令", nil, "")
	if err != nil {
		t.Fatalf("RunConversation 不应返回错误: %v", err)
	}

	foundRejection := false
	for _, msg := range result.Messages {
		if msg.Role == llm.RoleTool && strings.Contains(msg.Content, "工具调用被拒绝") {
			foundRejection = true
			break
		}
	}
	if !foundRejection {
		t.Fatal("未找到审批拒绝消息，终端工具调用未被正确拦截")
	}

	if !result.Completed {
		t.Fatal("对话应正常完成")
	}
	if result.FinalResponse != "任务完成" {
		t.Fatalf("最终响应不匹配，期望 '任务完成'，实际 '%s'", result.FinalResponse)
	}
}

// ───────────────────────────── 2. 并行审批测试 ─────────────────────────────

// TestConversationParallelApproval verifies approval checks for multiple terminal tool calls.
func TestConversationParallelApproval(t *testing.T) {
	tool.SetTerminalConfig(nil, nil)

	mock := &testutil.MockProvider{}
	var callCount int32

	mock.CreateChatCompletionFunc = func(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
		n := atomic.AddInt32(&callCount, 1)
		if n == 1 {
			return &llm.ChatResponse{
				StopReason: llm.StopToolUse,
				ToolCalls: []llm.ToolCall{
					{ID: "tc-safe-1", Name: "terminal", Arguments: `{"command": "ls -la"}`},
					{ID: "tc-blocked-1", Name: "terminal", Arguments: `{"command": "make build"}`},
					{ID: "tc-blocked-2", Name: "terminal", Arguments: `{"command": "cargo run --release"}`},
				},
			}, nil
		}
		return &llm.ChatResponse{
			StopReason: llm.StopEndTurn,
			Content:    "执行完毕",
		}, nil
	}

	checker := approval.NewChecker("always", nil, nil)

	agent := newTestAgent(mock,
		WithApprovalChecker(checker),
		WithGuardrails(nil),
	)

	result, err := agent.RunConversation(context.Background(), "执行多个命令", nil, "")
	if err != nil {
		t.Fatalf("RunConversation 不应返回错误: %v", err)
	}

	rejectedCount := 0
	approvedCount := 0
	for _, msg := range result.Messages {
		if msg.Role == llm.RoleTool {
			if strings.Contains(msg.Content, "工具调用被拒绝") {
				rejectedCount++
			} else if strings.Contains(msg.Content, "终端环境未配置") {
				approvedCount++
			}
		}
	}

	if approvedCount < 1 {
		t.Fatal("安全命令 'ls -la' 应通过审批检查")
	}
	if rejectedCount < 2 {
		t.Fatalf("非安全命令应被拒绝，期望至少 2 个拒绝，实际 %d", rejectedCount)
	}
}

// ───────────────────────────── 3. 护栏拦截测试 ─────────────────────────────

// TestConversationGuardrails verifies tool call guardrails' duplicate detection.
func TestConversationGuardrails(t *testing.T) {
	mock := &testutil.MockProvider{}
	var callCount int32

	mock.CreateChatCompletionFunc = func(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
		n := atomic.AddInt32(&callCount, 1)

		if n <= 4 {
			return &llm.ChatResponse{
				StopReason: llm.StopToolUse,
				ToolCalls: []llm.ToolCall{
					{
						ID:        fmt.Sprintf("tc-guard-%d", n),
						Name:      "file_read",
						Arguments: `{"path": "repeated_file.txt"}`,
					},
				},
			}, nil
		}

		return &llm.ChatResponse{
			StopReason: llm.StopEndTurn,
			Content:    "已更换策略",
		}, nil
	}

	guardrails := NewToolCallGuardrails().WithMaxConsecutiveDuplicates(4)

	agent := newTestAgent(mock,
		WithGuardrails(guardrails),
	)

	result, err := agent.RunConversation(context.Background(), "读取文件", nil, "")
	if err != nil {
		t.Fatalf("RunConversation 不应返回错误: %v", err)
	}

	foundGuardrailBlock := false
	for _, msg := range result.Messages {
		if msg.Role == llm.RoleTool && strings.Contains(msg.Content, "安全护栏拦截") {
			foundGuardrailBlock = true
			break
		}
	}
	if !foundGuardrailBlock {
		t.Fatal("未找到护栏拦截消息，第 4 次重复调用未被拦截")
	}

	successToolResults := 0
	for _, msg := range result.Messages {
		if msg.Role == llm.RoleTool && !strings.Contains(msg.Content, "安全护栏拦截") {
			successToolResults++
		}
	}
	if successToolResults < 3 {
		t.Fatalf("前 3 次工具调用应成功执行，实际成功 %d 次", successToolResults)
	}

	if !result.Completed {
		t.Fatal("对话应正常完成")
	}
}
