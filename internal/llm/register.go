package llm

import "log/slog"

// RegisterAllTransports registers all built-in transport implementations
// into the global DefaultRegistry. Call this once at program startup.
func RegisterAllTransports() {
	_ = DefaultRegistry.Register("bedrock_converse", &BedrockTransport{region: DefaultBedrockRegion})
	_ = DefaultRegistry.Register("gemini_api", &GeminiTransport{baseURL: DefaultGeminiBaseURL})
	_ = DefaultRegistry.Register(lmStudioTransportID, &LMStudioTransport{baseURL: lmStudioDefaultBaseURL})
	_ = DefaultRegistry.Register("chat_completions", &OpenAITransport{baseURL: "https://api.openai.com"})
	slog.Debug("all transports registered")
}
