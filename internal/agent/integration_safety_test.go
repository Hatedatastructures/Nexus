// Package agent 集成测试 — 文件安全、上下文压缩和错误恢复。
package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	ictx "nexus-agent/internal/context"
	"nexus-agent/internal/llm"
	"nexus-agent/testutil"
)

// ───────────────────────────── 4. 文件安全测试 ─────────────────────────────

// TestConversationFileSafety verifies file write safety checker blocks sensitive files.
func TestConversationFileSafety(t *testing.T) {
	mock := &testutil.MockProvider{}
	var callCount int32

	tmpFile := "integration_test_safety_output.txt"
	t.Cleanup(func() {
		_ = os.Remove(tmpFile)
	})

	mock.CreateChatCompletionFunc = func(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
		n := atomic.AddInt32(&callCount, 1)

		switch n {
		case 1:
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

	fileSafety := NewFileSafetyChecker()

	agent := newTestAgent(mock,
		WithFileSafety(fileSafety),
		WithGuardrails(nil),
	)

	result, err := agent.RunConversation(context.Background(), "写入文件", nil, "")
	if err != nil {
		t.Fatalf("RunConversation 不应返回错误: %v", err)
	}

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

// TestConversationContextCompression verifies context compression mechanism.
func TestConversationContextCompression(t *testing.T) {
	mock := &testutil.MockProvider{}
	var callCount int32
	var summaryTriggered atomic.Bool

	largeContent := strings.Repeat("这是一段很长的工具输出内容，用于模拟实际的大规模工具返回结果。", 20)

	mock.CreateChatCompletionFunc = func(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
		if req.Model == "claude-sonnet-4-20250514" {
			summaryTriggered.Store(true)
			return &llm.ChatResponse{
				Content: "[CONTEXT COMPACTION] 之前的对话已被压缩。用户要求读取文件内容。",
			}, nil
		}

		n := atomic.AddInt32(&callCount, 1)

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

		return &llm.ChatResponse{
			StopReason: llm.StopEndTurn,
			Content:    "压缩后继续工作",
		}, nil
	}

	compressor := ictx.NewCompressor(1, 50)

	agent := newTestAgent(mock,
		WithCompressor(compressor),
		WithMaxIterations(10),
	)

	history := []llm.Message{
		{Role: llm.RoleUser, Content: "请帮我读取多个文件的内容"},
		{Role: llm.RoleAssistant, Content: largeContent},
	}

	result, err := agent.RunConversation(context.Background(), "继续读取文件", history, "")
	if err != nil {
		t.Fatalf("RunConversation 不应返回错误: %v", err)
	}

	if !summaryTriggered.Load() {
		t.Fatal("上下文压缩应被触发，但总结生成调用未发生")
	}

	if !result.Completed {
		t.Fatal("对话应正常完成")
	}
	if result.FinalResponse != "压缩后继续工作" {
		t.Fatalf("最终响应不匹配，实际 '%s'", result.FinalResponse)
	}
}

// ───────────────────────────── 6. 错误恢复测试 ─────────────────────────────

// TestConversationRecovery verifies auto-retry after LLM call failure.
func TestConversationRecovery(t *testing.T) {
	mock := &testutil.MockProvider{}
	var callCount int32

	mock.CreateChatCompletionFunc = func(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
		n := atomic.AddInt32(&callCount, 1)

		if n == 1 {
			return nil, fmt.Errorf("429 too many requests: rate limit exceeded, retry after 1 seconds")
		}

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

	if !result.Completed {
		t.Fatal("对话应在重试后正常完成")
	}
	if result.FinalResponse != "重试成功后的回复" {
		t.Fatalf("最终响应不匹配，实际 '%s'", result.FinalResponse)
	}

	actualCalls := atomic.LoadInt32(&callCount)
	if actualCalls != 2 {
		t.Fatalf("期望 2 次 API 调用 (1 次失败 + 1 次重试)，实际 %d 次", actualCalls)
	}
}
