package agent

import (
	"testing"
)

// ───────────────────────────── TestScrubAnthropic ─────────────────────────────

func TestScrubAnthropic(t *testing.T) {
	tests := []struct {
		name         string
		deltas       []string
		wantVisible  string
		wantThink    string
	}{
		{
			name:         "<think>hello\nworld</think> 应输出空",
			deltas:       []string{"<think>hello\nworld</think>"},
			wantVisible:  "",
			wantThink:    "hello\nworld",
		},
		{
			name:         "标签前后有文本",
			deltas:       []string{"before<think>inside</think>after"},
			wantVisible:  "beforeafter",
			wantThink:    "inside",
		},
		{
			name:         "多块 delta 传递",
			deltas:       []string{"before<think>thin", "king content</think>after"},
			wantVisible:  "beforeafter",
			wantThink:    "thinking content",
		},
		{
			name:         "空思考内容",
			deltas:       []string{"before<think></think>after"},
			wantVisible:  "beforeafter",
			wantThink:    "",
		},
		{
			name:         "多行思考内容",
			deltas:       []string{"<think>line1\nline2\nline3</think>"},
			wantVisible:  "",
			wantThink:    "line1\nline2\nline3",
		},
		{
			name:         "多个 think 标签",
			deltas:       []string{"a<think>one</think>b<think>two</think>c"},
			wantVisible:  "abc",
			wantThink:    "onetwo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewThinkScrubber("anthropic", nil)

			var visible string
			for _, d := range tt.deltas {
				visible += s.Scrub(d)
			}

			if visible != tt.wantVisible {
				t.Errorf("可见文本 = %q, want %q", visible, tt.wantVisible)
			}
			if s.ThinkContent() != tt.wantThink {
				t.Errorf("ThinkContent() = %q, want %q", s.ThinkContent(), tt.wantThink)
			}
		})
	}
}

// ───────────────────────────── TestScrubDeepSeek ─────────────────────────────

func TestScrubDeepSeek(t *testing.T) {
	tests := []struct {
		name         string
		deltas       []string
		wantVisible  string
		wantThink    string
	}{
		{
			name:         "完整 DeepSeek 标签",
			deltas:       []string{"<|thinking|>deep thoughts<|/thinking|>"},
			wantVisible:  "",
			wantThink:    "deep thoughts",
		},
		{
			name:         "标签前后有文本",
			deltas:       []string{"before<|thinking|>inside<|/thinking|>after"},
			wantVisible:  "beforeafter",
			wantThink:    "inside",
		},
		{
			name:         "多块 delta 传递",
			deltas:       []string{"<|think", "ing|>content<|/thin", "king|>"},
			wantVisible:  "",
			wantThink:    "content",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewThinkScrubber("deepseek", nil)

			var visible string
			for _, d := range tt.deltas {
				visible += s.Scrub(d)
			}

			if visible != tt.wantVisible {
				t.Errorf("可见文本 = %q, want %q", visible, tt.wantVisible)
			}
			if s.ThinkContent() != tt.wantThink {
				t.Errorf("ThinkContent() = %q, want %q", s.ThinkContent(), tt.wantThink)
			}
		})
	}
}

// ───────────────────────────── TestScrubPartialTag ─────────────────────────────

func TestScrubPartialTag(t *testing.T) {
	tests := []struct {
		name         string
		deltas       []string
		wantVisible  string
		wantThink    string
	}{
		{
			// <think = 7 chars: split as <thin + k>
			name:         "Anthropic 开始标签被分割",
			deltas:       []string{"<thin", "k>content</thin", "k>"},
			wantVisible:  "",
			wantThink:    "content",
		},
		{
			// </think = 8 chars: split as </thi + nk>
			name:         "Anthropic 结束标签被分割",
			deltas:       []string{"<think>content</thi", "nk>"},
			wantVisible:  "",
			wantThink:    "content",
		},
		{
			name:         "每个字符一个 delta",
			deltas:       []string{"<", "t", "h", "i", "n", "k", ">", "a", "b", "c", "<", "/", "t", "h", "i", "n", "k", ">"},
			wantVisible:  "",
			wantThink:    "abc",
		},
		{
			name:         "标签匹配失败回吐",
			deltas:       []string{"<thinkX"},
			wantVisible:  "<thinkX",
			wantThink:    "",
		},
		{
			name:         "部分匹配后失败回吐",
			deltas:       []string{"<thi", "n", "X"},
			wantVisible:  "<thinX",
			wantThink:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewThinkScrubber("anthropic", nil)

			var visible string
			for _, d := range tt.deltas {
				visible += s.Scrub(d)
			}

			if visible != tt.wantVisible {
				t.Errorf("可见文本 = %q, want %q", visible, tt.wantVisible)
			}
			if s.ThinkContent() != tt.wantThink {
				t.Errorf("ThinkContent() = %q, want %q", s.ThinkContent(), tt.wantThink)
			}
		})
	}
}

// ───────────────────────────── TestScrubNoThink ─────────────────────────────

func TestScrubNoThink(t *testing.T) {
	tests := []struct {
		name        string
		deltas      []string
		wantVisible string
	}{
		{
			name:        "纯文本无标签",
			deltas:      []string{"hello world"},
			wantVisible: "hello world",
		},
		{
			name:        "多块纯文本",
			deltas:      []string{"hello ", "world", "!"},
			wantVisible: "hello world!",
		},
		{
			name:        "空输入",
			deltas:      []string{""},
			wantVisible: "",
		},
		{
			name:        "包含尖括号但非标签",
			deltas:      []string{"a < b > c"},
			wantVisible: "a < b > c",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewThinkScrubber("anthropic", nil)

			var visible string
			for _, d := range tt.deltas {
				visible += s.Scrub(d)
			}

			if visible != tt.wantVisible {
				t.Errorf("可见文本 = %q, want %q", visible, tt.wantVisible)
			}
			if s.ThinkContent() != "" {
				t.Errorf("ThinkContent() 应为空，实际 = %q", s.ThinkContent())
			}
		})
	}
}

// ───────────────────────────── TestScrubGenericTag ─────────────────────────────

func TestScrubGenericTag(t *testing.T) {
	s := NewThinkScrubber("generic", nil)

	visible := s.Scrub("before<scratchpad>thinking</scratchpad>after")
	if visible != "beforeafter" {
		t.Errorf("可见文本 = %q, want %q", visible, "beforeafter")
	}
	if s.ThinkContent() != "thinking" {
		t.Errorf("ThinkContent() = %q, want %q", s.ThinkContent(), "thinking")
	}
}

// ───────────────────────────── TestScrubOnThinkCallback ─────────────────────────────

func TestScrubOnThinkCallback(t *testing.T) {
	var captured string
	s := NewThinkScrubber("anthropic", func(chunk string) {
		captured += chunk
	})

	s.Scrub("<think>callback test</think>")

	if captured != "callback test" {
		t.Errorf("回调捕获 = %q, want %q", captured, "callback test")
	}
}

// ───────────────────────────── TestScrubReset ─────────────────────────────

func TestScrubReset(t *testing.T) {
	s := NewThinkScrubber("anthropic", nil)
	s.Scrub("<think>first content</think>visible1")
	s.Reset()

	// Reset 后重新开始
	visible := s.Scrub("<think>second</think>visible2")
	if visible != "visible2" {
		t.Errorf("Reset 后可见文本 = %q, want %q", visible, "visible2")
	}
	// Reset 后 ThinkContent 应只有第二次的内容
	if s.ThinkContent() != "second" {
		t.Errorf("Reset 后 ThinkContent() = %q, want %q", s.ThinkContent(), "second")
	}
}
