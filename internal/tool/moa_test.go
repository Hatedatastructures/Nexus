package tool

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMoATool_Basics(t *testing.T) {
	t.Parallel()
	tool := NewMoATool()

	if tool.Name() != "mixture_of_agents" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "mixture_of_agents")
	}
	if tool.Toolset() != "llm" {
		t.Errorf("Toolset() = %q, want %q", tool.Toolset(), "llm")
	}
	if tool.MaxResultChars() != 50000 {
		t.Errorf("MaxResultChars() = %d, want 50000", tool.MaxResultChars())
	}
	schema := tool.Schema()
	if schema.Name != "mixture_of_agents" {
		t.Errorf("Schema().Name = %q, want %q", schema.Name, "mixture_of_agents")
	}
}

func TestMoATool_ConstructAggregatorPrompt(t *testing.T) {
	t.Parallel()
	tool := &MoATool{}
	prompt := tool.constructAggregatorPrompt("What is Go?", []string{"Response A", "Response B"})

	if !strings.Contains(prompt, "What is Go?") {
		t.Error("aggregator prompt missing user question")
	}
	if !strings.Contains(prompt, "Response A") {
		t.Error("aggregator prompt missing response A")
	}
	if !strings.Contains(prompt, "Response B") {
		t.Error("aggregator prompt missing response B")
	}
	if !strings.Contains(prompt, "模型 1") {
		t.Error("aggregator prompt missing model 1 header")
	}
	if !strings.Contains(prompt, "模型 2") {
		t.Error("aggregator prompt missing model 2 header")
	}
}

func TestMoAToolResult_Serialization(t *testing.T) {
	t.Parallel()
	result := MoAToolResult{
		Success:    true,
		Response:   "test response",
		ModelsUsed: []string{"model-a", "model-b"},
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var parsed MoAToolResult
	if jsonErr := json.Unmarshal(data, &parsed); jsonErr != nil {
		t.Fatalf("failed to unmarshal: %v", jsonErr)
	}
	if parsed.Success != true {
		t.Error("Success should be true")
	}
	if parsed.Response != "test response" {
		t.Errorf("Response = %q, want %q", parsed.Response, "test response")
	}
	if len(parsed.ModelsUsed) != 2 {
		t.Errorf("ModelsUsed length = %d, want 2", len(parsed.ModelsUsed))
	}
}

func TestMoAToolResult_WithError(t *testing.T) {
	t.Parallel()
	result := MoAToolResult{
		Success: false,
		Error:   "something went wrong",
	}

	data, _ := json.Marshal(result)
	var parsed MoAToolResult
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if parsed.Error != "something went wrong" {
		t.Errorf("Error = %q, want %q", parsed.Error, "something went wrong")
	}
}

func TestMoAConstants(t *testing.T) {
	t.Parallel()
	if moaMinSuccessfulReferences < 1 {
		t.Error("moaMinSuccessfulReferences should be >= 1")
	}
	if moaMaxRetries < 1 {
		t.Error("moaMaxRetries should be >= 1")
	}
	if len(moaReferenceModels) == 0 {
		t.Error("moaReferenceModels should not be empty")
	}
	if moaAggregatorModel == "" {
		t.Error("moaAggregatorModel should not be empty")
	}
	if moaAggregatorSystemPrompt == "" {
		t.Error("moaAggregatorSystemPrompt should not be empty")
	}
}

func TestOpenRouterProvider_Basics(t *testing.T) {
	t.Parallel()
	provider := &OpenRouterProvider{apiKey: "test-key"}
	if provider.apiKey != "test-key" {
		t.Error("apiKey not set correctly")
	}
}
