// Package tool 测试澄清工具。
package tool

import (
	"context"
	"encoding/json"
	"testing"
)

// TestClarifyToolBasics 测试澄清工具基本功能。
func TestClarifyToolBasics(t *testing.T) {
	tool := &ClarifyTool{}

	t.Run("Name", func(t *testing.T) {
		if tool.Name() != "clarify" {
			t.Errorf("Expected name 'clarify', got '%s'", tool.Name())
		}
	})

	t.Run("Toolset", func(t *testing.T) {
		if tool.Toolset() != "clarify" {
			t.Errorf("Expected toolset 'clarify', got '%s'", tool.Toolset())
		}
	})

	t.Run("Emoji", func(t *testing.T) {
		if tool.Emoji() != "❓" {
			t.Errorf("Expected emoji '❓', got '%s'", tool.Emoji())
		}
	})

	t.Run("MaxResultChars", func(t *testing.T) {
		if tool.MaxResultChars() != 2000 {
			t.Errorf("Expected 2000, got %d", tool.MaxResultChars())
		}
	})

	t.Run("Schema", func(t *testing.T) {
		schema := tool.Schema()
		if schema.Name != "clarify" {
			t.Errorf("Expected schema name 'clarify', got '%s'", schema.Name)
		}
	})
}

// TestClarifyToolAvailability 测试工具可用性。
func TestClarifyToolAvailability(t *testing.T) {
	tool := &ClarifyTool{}

	// 无回调时不可用
	SetClarifyCallback(nil)
	if tool.IsAvailable() {
		t.Error("Expected IsAvailable=false when no callback set")
	}

	// 有回调时可用
	SetClarifyCallback(func(q string, c []string) string { return "test" })
	if !tool.IsAvailable() {
		t.Error("Expected IsAvailable=true when callback set")
	}

	// 清理
	SetClarifyCallback(nil)
}

// TestClarifyToolExecute 测试工具执行。
func TestClarifyToolExecute(t *testing.T) {
	tool := &ClarifyTool{}
	ctx := context.Background()

	t.Run("simple_question", func(t *testing.T) {
		SetClarifyCallback(func(q string, c []string) string {
			if q != "What color?" {
				t.Errorf("Expected question 'What color?', got '%s'", q)
			}
			if c != nil {
				t.Errorf("Expected nil choices, got %v", c)
			}
			return "blue"
		})

		result, err := tool.Execute(ctx, map[string]any{"question": "What color?"})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		var parsed map[string]any
		if err := json.Unmarshal([]byte(result), &parsed); err != nil {
			t.Fatalf("Failed to parse result: %v", err)
		}

		if parsed["question"] != "What color?" {
			t.Errorf("Expected question 'What color?', got '%s'", parsed["question"])
		}
		if parsed["user_response"] != "blue" {
			t.Errorf("Expected user_response 'blue', got '%s'", parsed["user_response"])
		}
	})

	t.Run("question_with_choices", func(t *testing.T) {
		SetClarifyCallback(func(q string, c []string) string {
			if q != "Pick a number" {
				t.Errorf("Expected question 'Pick a number', got '%s'", q)
			}
			if len(c) != 3 || c[0] != "1" || c[1] != "2" || c[2] != "3" {
				t.Errorf("Expected choices [1,2,3], got %v", c)
			}
			return "2"
		})

		result, err := tool.Execute(ctx, map[string]any{
			"question": "Pick a number",
			"choices":  []any{"1", "2", "3"},
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		var parsed map[string]any
		if err := json.Unmarshal([]byte(result), &parsed); err != nil {
			t.Fatalf("Failed to parse result: %v", err)
		}

		if parsed["user_response"] != "2" {
			t.Errorf("Expected user_response '2', got '%s'", parsed["user_response"])
		}
	})

	t.Run("empty_question_error", func(t *testing.T) {
		SetClarifyCallback(func(q string, c []string) string { return "ignored" })

		result, err := tool.Execute(ctx, map[string]any{"question": ""})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		var parsed map[string]any
		if err := json.Unmarshal([]byte(result), &parsed); err != nil {
			t.Fatalf("Failed to parse result: %v", err)
		}

		if parsed["error"] == nil {
			t.Error("Expected error for empty question")
		}
	})

	t.Run("whitespace_question_error", func(t *testing.T) {
		SetClarifyCallback(func(q string, c []string) string { return "ignored" })

		result, err := tool.Execute(ctx, map[string]any{"question": "   \n\t  "})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		var parsed map[string]any
		if err := json.Unmarshal([]byte(result), &parsed); err != nil {
			t.Fatalf("Failed to parse result: %v", err)
		}

		if parsed["error"] == nil {
			t.Error("Expected error for whitespace-only question")
		}
	})

	t.Run("no_callback_error", func(t *testing.T) {
		SetClarifyCallback(nil)

		result, err := tool.Execute(ctx, map[string]any{"question": "What do you want?"})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		var parsed map[string]any
		if err := json.Unmarshal([]byte(result), &parsed); err != nil {
			t.Fatalf("Failed to parse result: %v", err)
		}

		if parsed["error"] == nil {
			t.Error("Expected error when no callback")
		}
	})

	t.Run("choices_trimmed_to_max", func(t *testing.T) {
		var receivedChoices []string
		SetClarifyCallback(func(q string, c []string) string {
			receivedChoices = c
			return "picked"
		})

		_, _ = tool.Execute(ctx, map[string]any{
			"question": "Pick one",
			"choices":  []any{"a", "b", "c", "d", "e", "f", "g"},
		})

		if len(receivedChoices) != MaxClarifyChoices {
			t.Errorf("Expected %d choices, got %d", MaxClarifyChoices, len(receivedChoices))
		}
	})

	t.Run("invalid_choices_type_error", func(t *testing.T) {
		SetClarifyCallback(func(q string, c []string) string { return "ignored" })

		result, err := tool.Execute(ctx, map[string]any{
			"question": "Question?",
			"choices":  "not a list", // string instead of array
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		var parsed map[string]any
		if err := json.Unmarshal([]byte(result), &parsed); err != nil {
			t.Fatalf("Failed to parse result: %v", err)
		}

		if parsed["error"] == nil {
			t.Error("Expected error for invalid choices type")
		}
	})

	t.Run("stripped_question_and_response", func(t *testing.T) {
		SetClarifyCallback(func(q string, c []string) string {
			if q != "Question with spaces" {
				t.Errorf("Expected trimmed question, got '%s'", q)
			}
			return "  response with spaces  \n"
		})

		result, err := tool.Execute(ctx, map[string]any{"question": "  Question with spaces  \n"})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		var parsed map[string]any
		if err := json.Unmarshal([]byte(result), &parsed); err != nil {
			t.Fatalf("Failed to parse result: %v", err)
		}

		if parsed["user_response"] != "response with spaces" {
			t.Errorf("Expected trimmed response, got '%s'", parsed["user_response"])
		}
	})

	// 清理
	SetClarifyCallback(nil)
}

// TestMaxClarifyChoices 测试最大选项数常量。
func TestMaxClarifyChoices(t *testing.T) {
	if MaxClarifyChoices != 4 {
		t.Errorf("Expected MaxClarifyChoices=4, got %d", MaxClarifyChoices)
	}
}

// TestParseClarifyResult 测试结果解析。
func TestParseClarifyResult(t *testing.T) {
	jsonStr := `{"question":"Test?","choices_offered":["a","b"],"user_response":"a"}`

	result, err := ParseClarifyResult(jsonStr)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if result.Question != "Test?" {
		t.Errorf("Expected question 'Test?', got '%s'", result.Question)
	}
	if len(result.ChoicesOffered) != 2 {
		t.Errorf("Expected 2 choices, got %d", len(result.ChoicesOffered))
	}
	if result.UserResponse != "a" {
		t.Errorf("Expected user_response 'a', got '%s'", result.UserResponse)
	}
}