// Package errors 提供统一的错误类型和错误码。
package errors

// Code 表示错误码类型。
type Code string

const (
	// Provider 错误
	ProviderAuth    Code = "PROVIDER_AUTH"
	ProviderAPI     Code = "PROVIDER_API"
	ProviderConfig  Code = "PROVIDER_CONFIG"
	ProviderTimeout Code = "PROVIDER_TIMEOUT"

	// Tool 错误
	ToolNotFound Code = "TOOL_NOT_FOUND"
	ToolExec     Code = "TOOL_EXEC"
	ToolArg      Code = "TOOL_ARG"

	// File 错误
	FileSafety Code = "FILE_SAFETY"
	FileIO     Code = "FILE_IO"
	NotFound   Code = "NOT_FOUND"

	// Network 错误
	NetworkHTTP    Code = "NETWORK_HTTP"
	NetworkTimeout Code = "NETWORK_TIMEOUT"

	// Memory 错误
	MemoryProvider Code = "MEMORY_PROVIDER"
	MemoryConfig   Code = "MEMORY_CONFIG"

	// Sandbox 错误
	SandboxExec   Code = "SANDBOX_EXEC"
	SandboxConfig Code = "SANDBOX_CONFIG"

	// Config 错误
	ConfigMissing Code = "CONFIG_MISSING"
	ConfigInvalid Code = "CONFIG_INVALID"

	// Auth 错误
	AuthFailed Code = "AUTH_FAILED"
	AuthOAuth  Code = "AUTH_OAUTH"

	// Platform 错误
	PlatformAPI    Code = "PLATFORM_API"
	PlatformConfig Code = "PLATFORM_CONFIG"

	// Session 错误
	SessionState Code = "SESSION_STATE"

	// Cron 错误
	CronJob Code = "CRON_JOB"

	// Skill 错误
	SkillNotFound Code = "SKILL_NOT_FOUND"
	SkillIO       Code = "SKILL_IO"

	// MCP 错误
	MCPProtocol Code = "MCP_PROTOCOL"
	MCPOAuth    Code = "MCP_OAUTH"
)

// Error 是统一的错误类型，包含错误码、消息和可选的原因。
type Error struct {
	Code    Code
	Message string
	Cause   error
}

// New 创建一个新的 Error。
func New(code Code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// Wrap 用错误码和消息包装一个已有错误。
func Wrap(code Code, message string, cause error) *Error {
	return &Error{Code: code, Message: message, Cause: cause}
}

// WithCode 给已有错误附加错误码。如果 cause 已经是 *Error，更新其 Code。
func WithCode(code Code, cause error) *Error {
	if e, ok := cause.(*Error); ok {
		return &Error{Code: code, Message: e.Message, Cause: e.Cause}
	}
	return &Error{Code: code, Message: cause.Error(), Cause: cause}
}

// Error 实现 error 接口。
func (e *Error) Error() string {
	if e.Cause != nil {
		return e.Message + ": " + e.Cause.Error()
	}
	return e.Message
}

// Unwrap 支持 errors.Is/As。
func (e *Error) Unwrap() error {
	return e.Cause
}
