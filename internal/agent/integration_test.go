// Package agent 集成测试。
// 使用 mock LLM 和真实工具注册表验证对话循环中的安全机制。
package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"nexus-agent/internal/approval"
	ictx "nexus-agent/internal/context"
	"nexus-agent/internal/llm"
	"nexus-agent/internal/tool"
	"nexus-agent/testutil"
)

// ───────────────────────────── 辅助函数 ─────────────────────────────

// newTestAgent 创建用于集成测试的 AIAgent 实例。
// 使用提供的 mock 提供者和可选的额外选项。
func newTestAgent(mock *testutil.MockProvider, extraOpts ...AgentOption) *AIAgent {
	baseOpts := []AgentOption{
		WithProvider(mock),
		WithModel("test-model"),
		WithMaxIterations(10),
		WithToolRegistry(tool.GetRegistry()),
		WithSessionID("integration-test"),
	}
	baseOpts = append(baseOpts, extraOpts...)
	return NewAgent(baseOpts...)
}

// ───────────────────────────── 1. 审批拦截测试 ─────────────────────────────

// TestConversationWithApproval 验证审批检查器能正确拦截终端工具调用。
// 使用 "always" 模式，非安全命令应被拦截返回 Pending。
func TestConversationWithApproval(t *testing.T) {
	mock := &testutil.MockProvider{}
	var callCount int32

	mock.CreateChatCompletionFunc = func(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
		n := atomic.AddInt32(&callCount, 1)
		if n == 1 {
			// 第一次调用: 返回一个终端工具调用，使用非安全命令
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
		// 后续调用: 返回最终文本响应
		return &llm.ChatResponse{
			StopReason: llm.StopEndTurn,
			Content:    "任务完成",
		}, nil
	}

	// 创建 "always" 模式的审批检查器 (非安全命令返回 Pending)
	checker := approval.NewChecker("always", nil, nil)

	agent := newTestAgent(mock,
		WithApprovalChecker(checker),
		WithGuardrails(nil), // 禁用护栏以专注测试审批
	)

	result, err := agent.RunConversation(context.Background(), "执行一个命令", nil, "")
	if err != nil {
		t.Fatalf("RunConversation 不应返回错误: %v", err)
	}

	// 验证工具调用被审批拦截
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

	// 验证对话仍能正常完成
	if !result.Completed {
		t.Fatal("对话应正常完成")
	}
	if result.FinalResponse != "任务完成" {
		t.Fatalf("最终响应不匹配，期望 '任务完成'，实际 '%s'", result.FinalResponse)
	}
}

// ───────────────────────────── 2. 并行审批测试 ─────────────────────────────

// TestConversationParallelApproval 验证多个终端工具调用的审批检查。
// 由于 terminal 不在 parallelSafe 集合中，多个终端调用走 executeSequential 路径，
// 其中审批检查逻辑与 executeParallel 完全一致。
// 测试混合安全/非安全命令，验证拒绝和放行行为正确。
func TestConversationParallelApproval(t *testing.T) {
	mock := &testutil.MockProvider{}
	var callCount int32

	mock.CreateChatCompletionFunc = func(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
		n := atomic.AddInt32(&callCount, 1)
		if n == 1 {
			// 返回多个终端工具调用: 一个安全命令 + 两个非安全命令
			return &llm.ChatResponse{
				StopReason: llm.StopToolUse,
				ToolCalls: []llm.ToolCall{
					{
						ID:        "tc-safe-1",
						Name:      "terminal",
						Arguments: `{"command": "ls -la"}`,
					},
					{
						ID:        "tc-blocked-1",
						Name:      "terminal",
						Arguments: `{"command": "make build"}`,
					},
					{
						ID:        "tc-blocked-2",
						Name:      "terminal",
						Arguments: `{"command": "cargo run --release"}`,
					},
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

	// 统计被拒绝和通过的工具调用
	rejectedCount := 0
	approvedCount := 0
	for _, msg := range result.Messages {
		if msg.Role == llm.RoleTool {
			if strings.Contains(msg.Content, "工具调用被拒绝") {
				rejectedCount++
			} else if strings.Contains(msg.Content, "终端环境未配置") {
				// "ls -la" 通过审批但因无沙箱环境而执行失败，这也算通过了审批
				approvedCount++
			}
		}
	}

	// "ls -la" 是安全命令应通过审批; "make build" 和 "cargo run" 应被拒绝
	if approvedCount < 1 {
		t.Fatal("安全命令 'ls -la' 应通过审批检查")
	}
	if rejectedCount < 2 {
		t.Fatalf("非安全命令应被拒绝，期望至少 2 个拒绝，实际 %d", rejectedCount)
	}
}

// ───────────────────────────── 3. 护栏拦截测试 ─────────────────────────────

// TestConversationGuardrails 验证工具调用安全护栏的重复检测功能。
// 当 LLM 连续返回相同的工具调用时，护栏应在第 4 次调用时拦截。
func TestConversationGuardrails(t *testing.T) {
	mock := &testutil.MockProvider{}
	var callCount int32

	mock.CreateChatCompletionFunc = func(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
		n := atomic.AddInt32(&callCount, 1)

		// 前 4 次调用返回相同的工具调用
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

		// 第 5 次调用: 返回最终响应 (护栏拦截后 LLM 被要求换策略)
		return &llm.ChatResponse{
			StopReason: llm.StopEndTurn,
			Content:    "已更换策略",
		}, nil
	}

	// 设置护栏阈值为 4 (第 4 次精确重复时拦截)
	guardrails := NewToolCallGuardrails().WithMaxConsecutiveDuplicates(4)

	agent := newTestAgent(mock,
		WithGuardrails(guardrails),
	)

	result, err := agent.RunConversation(context.Background(), "读取文件", nil, "")
	if err != nil {
		t.Fatalf("RunConversation 不应返回错误: %v", err)
	}

	// 验证护栏拦截消息被注入给 LLM
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

	// 验证前 3 次工具调用成功执行 (有 tool 结果且不含拦截消息)
	successToolResults := 0
	for _, msg := range result.Messages {
		if msg.Role == llm.RoleTool && !strings.Contains(msg.Content, "安全护栏拦截") {
			successToolResults++
		}
	}
	if successToolResults < 3 {
		t.Fatalf("前 3 次工具调用应成功执行，实际成功 %d 次", successToolResults)
	}

	// 验证对话最终完成
	if !result.Completed {
		t.Fatal("对话应正常完成")
	}
}

// ───────────────────────────── 4. 文件安全测试 ─────────────────────────────

// TestConversationFileSafety 验证文件写入安全检查器对敏感文件的拦截。
// 写入 .env 文件应被拦截，写入普通 .txt 文件应被允许。
func TestConversationFileSafety(t *testing.T) {
	mock := &testutil.MockProvider{}
	var callCount int32

	// 创建临时文件路径用于测试写入成功的情况
	tmpFile := "integration_test_safety_output.txt"
	t.Cleanup(func() {
		os.Remove(tmpFile)
	})

	mock.CreateChatCompletionFunc = func(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
		n := atomic.AddInt32(&callCount, 1)

		switch n {
		case 1:
			// 第 1 次: 尝试写入 .env 文件 (应被 fileSafety 拦截)
			return &llm.ChatResponse{
				StopReason: llm.StopToolUse,
				ToolCalls: []llm.ToolCall{
					{
						ID:        "tc-env-1",
						Name:      "file_write",
						Arguments: `{"path": ".env", "content": "SECRET_KEY=abc123"}`,
					},
				},
			}, nil
		case 2:
			// 第 2 次: 写入普通 .txt 文件 (应被允许)
			return &llm.ChatResponse{
				StopReason: llm.StopToolUse,
				ToolCalls: []llm.ToolCall{
					{
						ID:        "tc-txt-1",
						Name:      "file_write",
						Arguments: fmt.Sprintf(`{"path": "%s", "content": "正常内容"}`, tmpFile),
					},
				},
			}, nil
		default:
			return &llm.ChatResponse{
				StopReason: llm.StopEndTurn,
				Content:    "文件操作完成",
			}, nil
		}
	}

	// 使用默认的 FileSafetyChecker (保护 .env 等敏感文件)
	fileSafety := NewFileSafetyChecker()

	agent := newTestAgent(mock,
		WithFileSafety(fileSafety),
		WithGuardrails(nil),
	)

	result, err := agent.RunConversation(context.Background(), "写入文件", nil, "")
	if err != nil {
		t.Fatalf("RunConversation 不应返回错误: %v", err)
	}

	// 验证 .env 写入被拦截
	foundEnvBlock := false
	foundTxtSuccess := false
	for _, msg := range result.Messages {
		if msg.Role == llm.RoleTool {
			if strings.Contains(msg.Content, "文件写入被安全策略拦截") && strings.Contains(msg.Content, ".env") {
				foundEnvBlock = true
			}
			if strings.Contains(msg.Content, tmpFile) && strings.Contains(msg.Content, "文件写入成功") {
				foundTxtSuccess = true
			}
		}
	}

	if !foundEnvBlock {
		t.Fatal("写入 .env 文件应被 fileSafety 拦截")
	}
	if !foundTxtSuccess {
		t.Fatal("写入普通 .txt 文件应成功")
	}
}

// ───────────────────────────── 5. 上下文压缩测试 ─────────────────────────────

// TestConversationContextCompression 验证上下文压缩机制。
// 当对话 token 数超过阈值时，压缩器应被触发，生成总结并缩减消息列表。
func TestConversationContextCompression(t *testing.T) {
	mock := &testutil.MockProvider{}
	var callCount int32
	var summaryTriggered atomic.Bool

	// 生成大型工具结果以快速膨胀上下文
	largeContent := strings.Repeat("这是一段很长的工具输出内容，用于模拟实际的大规模工具返回结果。", 20)

	mock.CreateChatCompletionFunc = func(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
		// 识别压缩器的总结生成调用 (使用硬编码的模型名)
		if req.Model == "claude-sonnet-4-20250514" {
			summaryTriggered.Store(true)
			return &llm.ChatResponse{
				Content: "[CONTEXT COMPACTION] 之前的对话已被压缩。用户要求读取文件内容。",
			}, nil
		}

		n := atomic.AddInt32(&callCount, 1)

		// 前 3 次调用返回工具调用以膨胀上下文
		if n <= 3 {
			return &llm.ChatResponse{
				StopReason: llm.StopToolUse,
				ToolCalls: []llm.ToolCall{
					{
						ID:        fmt.Sprintf("tc-compress-%d", n),
						Name:      "file_read",
						Arguments: fmt.Sprintf(`{"path": "large_file_%d.txt"}`, n),
					},
				},
			}, nil
		}

		// 后续调用: 返回最终响应
		return &llm.ChatResponse{
			StopReason: llm.StopEndTurn,
			Content:    "压缩后继续工作",
		}, nil
	}

	// 创建压缩器: 小尾部预算以快速触发压缩
	compressor := ictx.NewCompressor(1, 50)

	agent := newTestAgent(mock,
		WithCompressor(compressor),
		WithMaxIterations(10),
	)

	// 注入大型历史消息以加速 token 累积
	history := []llm.Message{
		{Role: llm.RoleUser, Content: "请帮我读取多个文件的内容"},
		{Role: llm.RoleAssistant, Content: largeContent},
	}

	result, err := agent.RunConversation(context.Background(), "继续读取文件", history, "")
	if err != nil {
		t.Fatalf("RunConversation 不应返回错误: %v", err)
	}

	// 验证压缩被触发
	if !summaryTriggered.Load() {
		t.Fatal("上下文压缩应被触发，但总结生成调用未发生")
	}

	// 验证对话正常完成
	if !result.Completed {
		t.Fatal("对话应正常完成")
	}
	if result.FinalResponse != "压缩后继续工作" {
		t.Fatalf("最终响应不匹配，实际 '%s'", result.FinalResponse)
	}
}

// ───────────────────────────── 6. 错误恢复测试 ─────────────────────────────

// TestConversationRecovery 验证 LLM 调用失败后的自动重试恢复。
// 第一次调用返回速率限制错误，第二次调用成功，验证重试机制正常工作。
func TestConversationRecovery(t *testing.T) {
	mock := &testutil.MockProvider{}
	var callCount int32

	mock.CreateChatCompletionFunc = func(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
		n := atomic.AddInt32(&callCount, 1)

		if n == 1 {
			// 第一次调用: 返回速率限制错误 (429)
			return nil, fmt.Errorf("429 too many requests: rate limit exceeded, retry after 1 seconds")
		}

		// 第二次调用: 成功
		return &llm.ChatResponse{
			StopReason: llm.StopEndTurn,
			Content:    "重试成功后的回复",
			Usage: &llm.TokenUsage{
				PromptTokens:     10,
				CompletionTokens: 5,
				TotalTokens:      15,
			},
		}, nil
	}

	agent := newTestAgent(mock,
		WithGuardrails(nil),
	)

	result, err := agent.RunConversation(context.Background(), "测试恢复", nil, "")
	if err != nil {
		t.Fatalf("RunConversation 不应返回错误 (应自动重试成功): %v", err)
	}

	// 验证重试后成功完成
	if !result.Completed {
		t.Fatal("对话应在重试后正常完成")
	}
	if result.FinalResponse != "重试成功后的回复" {
		t.Fatalf("最终响应不匹配，实际 '%s'", result.FinalResponse)
	}

	// 验证实际发生了 2 次 API 调用 (1 次失败 + 1 次成功)
	actualCalls := atomic.LoadInt32(&callCount)
	if actualCalls != 2 {
		t.Fatalf("期望 2 次 API 调用 (1 次失败 + 1 次重试)，实际 %d 次", actualCalls)
	}
}
