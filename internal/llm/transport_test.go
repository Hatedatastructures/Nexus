package llm

import (
	"testing"
)

func TestTransportRegistry_Get_Found(t *testing.T) {
	reg := NewTransportRegistry()
	mock := &OpenAITransport{baseURL: "https://example.com"}
	_ = reg.Register("test_mode", mock)

	got, ok := reg.Get("test_mode")
	if !ok {
		t.Fatal("expected to find registered transport")
	}
	if got != mock {
		t.Error("returned transport does not match registered one")
	}
}

func TestTransportRegistry_Get_NotFound(t *testing.T) {
	reg := NewTransportRegistry()
	_, ok := reg.Get("nonexistent")
	if ok {
		t.Error("should not find unregistered transport")
	}
}

func TestTransportRegistry_List(t *testing.T) {
	reg := NewTransportRegistry()
	mock := &OpenAITransport{}
	_ = reg.Register("mode_a", mock)
	_ = reg.Register("mode_b", mock)

	modes := reg.List()
	if len(modes) != 2 {
		t.Fatalf("List() returned %d modes, want 2", len(modes))
	}

	set := map[string]bool{}
	for _, m := range modes {
		set[m] = true
	}
	if !set["mode_a"] || !set["mode_b"] {
		t.Errorf("List() = %v, want mode_a and mode_b", modes)
	}
}

func TestTransportRegistry_ListEmpty(t *testing.T) {
	reg := NewTransportRegistry()
	modes := reg.List()
	if len(modes) != 0 {
		t.Errorf("empty registry List() = %v, want empty", modes)
	}
}

func TestTransportRegistry_Register_Duplicate(t *testing.T) {
	reg := NewTransportRegistry()
	first := &OpenAITransport{baseURL: "https://first.com"}
	second := &OpenAITransport{baseURL: "https://second.com"}

	err := reg.Register("mode", first)
	if err != nil {
		t.Fatalf("first Register should succeed: %v", err)
	}

	err = reg.Register("mode", second)
	if err == nil {
		t.Error("duplicate Register should return error")
	}

	got, ok := reg.Get("mode")
	if !ok {
		t.Fatal("expected to find transport")
	}
	if got.(*OpenAITransport).baseURL != "https://first.com" {
		t.Error("duplicate Register should not overwrite")
	}
}

func TestGetTransport_GlobalRegistry(t *testing.T) {
	RegisterAllTransports()
	got, ok := GetTransport("chat_completions")
	if !ok {
		t.Fatal("global registry should have chat_completions after RegisterAllTransports()")
	}
	if got.APIMode() != "chat_completions" {
		t.Errorf("APIMode = %q, want chat_completions", got.APIMode())
	}
}

func TestGetTransport_NotFound(t *testing.T) {
	_, ok := GetTransport("nonexistent_mode_xyz")
	if ok {
		t.Error("should not find unregistered global transport")
	}
}
