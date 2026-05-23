// Package context Builder 组装逻辑的单元测试。
// 覆盖完整流水线、动态边界标记、记忆注入、上下文文件加载、运行时配置和平台提示。
package context

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nexus-agent/internal/llm"
	"nexus-agent/internal/memory"
)

// ───────────────────────────── 辅助 ─────────────────────────────

// stubProvider 实现 memory.Provider 接口，用于在测试中注入受控行为。
type stubProvider struct {
	block string
}

func (s *stubProvider) Name() string                                          { return "stub" }
func (s *stubProvider) Initialize(_ context.Context, _ string) error         { return nil }
func (s *stubProvider) SystemPromptBlock() string                             { return s.block }
func (s *stubProvider) Prefetch(_ context.Context, _ string) (string, error) { return "", nil }
func (s *stubProvider) QueuePrefetch(_ context.Context, _ string)            {}
func (s *stubProvider) SyncTurn(_ context.Context, _, _ string) error        { return nil }
func (s *stubProvider) GetToolSchemas() []llm.ToolSchema                      { return nil }
func (s *stubProvider) HandleToolCall(_ context.Context, _ string, _ map[string]any) (string, error) {
	return `{"success":true}`, nil
}
func (s *stubProvider) Shutdown(_ context.Context) error   { return nil }
func (s *stubProvider) OnTurnStart(_ context.Context, _ int, _ string) error {
	return nil
}
func (s *stubProvider) OnSessionEnd(_ context.Context, _ []llm.Message) error {
	return nil
}
func (s *stubProvider) OnPreCompress(_ context.Context, _ []llm.Message) error {
	return nil
}
func (s *stubProvider) OnDelegation(_ context.Context, _, _, _ string) error {
	return nil
}

// boundaryMarker 是动态边界标记的常量，测试中多处引用。
const boundaryMarker = "__SYSTEM_PROMPT_DYNAMIC_BOUNDARY__"

// countSubstring 返回 sub 在 s 中出现的次数。
func countSubstring(s, sub string) int {
	return strings.Count(s, sub)
}

// ───────────────────────────── 测试用例 ─────────────────────────────

// TestBuild_FullPipeline 验证 Build 完整流水线包含所有必要段落。
func TestBuild_FullPipeline(t *testing.T) {
	t.Parallel()

	b := NewBuilder("You are Nexus", "cli", nil, nil)
	result, err := b.Build(context.Background(), &BuildOptions{
		SystemMessage: "extra system note",
		SessionID:     "sess-001",
		Model:         "gpt-4o",
	})
	if err != nil {
		t.Fatalf("Build 返回错误: %v", err)
	}

	checks := []struct {
		label   string
		needle  string
	}{
		{"identity text", "You are Nexus"},
		{"dynamic boundary marker", boundaryMarker},
		{"tool guidance header", "# System"},
		{"environment info header", "## 运行环境"},
		{"runtime config header", "## Runtime config"},
		{"platform hint CLI", "## 平台提示 (CLI)"},
		{"session id", "sess-001"},
		{"model name", "gpt-4o"},
	}

	for _, c := range checks {
		if !strings.Contains(result, c.needle) {
			t.Errorf("输出缺少 %s (%q)", c.label, c.needle)
		}
	}
}

// TestBuild_DynamicBoundary 验证动态边界标记恰好出现一次，
// 且位于工具指导文本之后、任何动态内容之前。
func TestBuild_DynamicBoundary(t *testing.T) {
	t.Parallel()

	b := NewBuilder("id", "cli", nil, nil)
	result, err := b.Build(context.Background(), &BuildOptions{})
	if err != nil {
		t.Fatalf("Build 返回错误: %v", err)
	}

	if n := countSubstring(result, boundaryMarker); n != 1 {
		t.Errorf("边界标记出现 %d 次, 期望 1", n)
	}

	// 边界标记必须在工具指导 "# Code style" 之后
	toolIdx := strings.Index(result, "# Code style")
	boundaryIdx := strings.Index(result, boundaryMarker)
	if toolIdx == -1 {
		t.Fatal("输出缺少 # Code style")
	}
	if boundaryIdx == -1 {
		t.Fatal("输出缺少边界标记")
	}
	if boundaryIdx <= toolIdx {
		t.Error("边界标记应出现在 # Code style 之后")
	}
}

// TestBuild_WithMemory 验证记忆块在边界标记之后出现。
func TestBuild_WithMemory(t *testing.T) {
	t.Parallel()

	const memContent = "## 持久记忆\n- 用户偏好: 中文回复"
	prov := &stubProvider{block: memContent}
	mgr := memory.NewManager(prov)

	b := NewBuilder("id", "cli", mgr, nil)
	result, err := b.Build(context.Background(), &BuildOptions{})
	if err != nil {
		t.Fatalf("Build 返回错误: %v", err)
	}

	if !strings.Contains(result, memContent) {
		t.Error("输出应包含记忆块内容")
	}

	boundaryIdx := strings.Index(result, boundaryMarker)
	memIdx := strings.Index(result, memContent)
	if boundaryIdx == -1 || memIdx == -1 {
		t.Fatal("输出缺少边界标记或记忆块")
	}
	if memIdx <= boundaryIdx {
		t.Error("记忆块应出现在边界标记之后")
	}
}

// TestBuild_WithNilMemory 验证当 memoryManager 为 nil 时 Build 优雅跳过记忆块。
func TestBuild_WithNilMemory(t *testing.T) {
	t.Parallel()

	b := NewBuilder("id", "cli", nil, nil)
	result, err := b.Build(context.Background(), &BuildOptions{})
	if err != nil {
		t.Fatalf("Build 返回错误: %v", err)
	}

	if strings.Contains(result, "## 持久记忆") {
		t.Error("memoryManager 为 nil 时不应出现记忆块")
	}
}

// TestBuild_WithContextFiles 验证显式指定的上下文文件内容注入到输出。
func TestBuild_WithContextFiles(t *testing.T) {
	t.Parallel()

	// 创建临时上下文文件
	dir := t.TempDir()
	const fileBody = "# Project Notes\nThis is a test project."
	fpath := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(fpath, []byte(fileBody), 0o644); err != nil {
		t.Fatalf("写入临时文件失败: %v", err)
	}

	// 保存原始 readFileBytes，测试结束后恢复
	orig := readFileBytes
	readFileBytes = func(p string) ([]byte, error) {
		return os.ReadFile(p)
	}
	t.Cleanup(func() { readFileBytes = orig })

	b := NewBuilder("id", "cli", nil, nil)
	result, err := b.Build(context.Background(), &BuildOptions{
		ContextFiles: []string{fpath},
	})
	if err != nil {
		t.Fatalf("Build 返回错误: %v", err)
	}

	if !strings.Contains(result, fileBody) {
		t.Error("输出应包含上下文文件内容")
	}
	if !strings.Contains(result, "AGENTS.md") {
		t.Error("输出应引用文件名 AGENTS.md")
	}
}

// TestBuild_RuntimeConfig 验证运行时配置段内容。
func TestBuild_RuntimeConfig(t *testing.T) {
	t.Parallel()

	b := NewBuilder("id", "cli", nil, nil)
	result, err := b.Build(context.Background(), &BuildOptions{})
	if err != nil {
		t.Fatalf("Build 返回错误: %v", err)
	}

	if !strings.Contains(result, "## Runtime config") {
		t.Error("输出缺少 ## Runtime config")
	}
	if !strings.Contains(result, "No Nexus settings files loaded") {
		t.Error("输出缺少 No Nexus settings files loaded")
	}
}

// TestBuild_PlatformHint_Telegram 验证 Telegram 平台提示。
func TestBuild_PlatformHint_Telegram(t *testing.T) {
	t.Parallel()

	b := NewBuilder("id", "telegram", nil, nil)
	result, err := b.Build(context.Background(), &BuildOptions{})
	if err != nil {
		t.Fatalf("Build 返回错误: %v", err)
	}

	if !strings.Contains(result, "## 平台提示 (Telegram)") {
		t.Error("输出缺少 ## 平台提示 (Telegram)")
	}
	if !strings.Contains(result, "4096 字符内") {
		t.Error("Telegram 平台提示应包含 4096 字符限制")
	}
}

// TestBuild_PlatformHint_Discord 验证 Discord 平台提示。
func TestBuild_PlatformHint_Discord(t *testing.T) {
	t.Parallel()

	b := NewBuilder("id", "discord", nil, nil)
	result, err := b.Build(context.Background(), &BuildOptions{})
	if err != nil {
		t.Fatalf("Build 返回错误: %v", err)
	}

	if !strings.Contains(result, "## 平台提示 (Discord)") {
		t.Error("输出缺少 ## 平台提示 (Discord)")
	}
}

// TestBuild_PlatformHint_Slack 验证 Slack 平台提示。
func TestBuild_PlatformHint_Slack(t *testing.T) {
	t.Parallel()

	b := NewBuilder("id", "slack", nil, nil)
	result, err := b.Build(context.Background(), &BuildOptions{})
	if err != nil {
		t.Fatalf("Build 返回错误: %v", err)
	}

	if !strings.Contains(result, "## 平台提示 (Slack)") {
		t.Error("输出缺少 ## 平台提示 (Slack)")
	}
}

// TestBuild_PlatformHint_Default 验证未知平台的默认提示。
func TestBuild_PlatformHint_Default(t *testing.T) {
	t.Parallel()

	b := NewBuilder("id", "unknown", nil, nil)
	result, err := b.Build(context.Background(), &BuildOptions{})
	if err != nil {
		t.Fatalf("Build 返回错误: %v", err)
	}

	if !strings.Contains(result, "## 平台提示") {
		t.Error("输出缺少 ## 平台提示")
	}
	// 默认提示不应包含具体平台名
	if strings.Contains(result, "## 平台提示 (") {
		t.Error("未知平台不应出现带括号的平台提示标题")
	}
}
