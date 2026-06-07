package interfaces

import (
	"fmt"
)

// Container 是 DI 容器，持有 AIAgent 所需的全部依赖。
// 所有字段均为接口类型，具体实现在外部注入。
// agent 包内部类型 (ProviderRouter, FallbackChain, ToolCallGuardrails,
// RecoveryEngine, FileSafetyChecker) 因循环导入限制，保留为 any 类型，
// 由 agent 包内部进行类型断言。
type Container struct {
	Registry        ToolRegistry
	MemoryManager   MemoryManager
	SkillManager    SkillManager
	ContextBuilder  ContextBuilder
	Compressor      ContextCompressor
	StateStore      StateStore
	Persister       SessionPersister
	CredentialPool  CredentialPool
	ApprovalChecker ApprovalChecker
	FileSafety      any // *agent.FileSafetyChecker
	Router          any // *agent.ProviderRouter
	FallbackChain   any // *agent.FallbackChain
	Guardrails      any // *agent.ToolCallGuardrails
	RecoveryEngine  any // *agent.RecoveryEngine
}

// NewContainer 创建空 DI 容器。
func NewContainer() *Container {
	return &Container{}
}

// Validate 检查所有必需依赖是否已注入。
func (c *Container) Validate() error {
	if c.Registry == nil {
		return fmt.Errorf("缺少依赖: Registry")
	}
	return nil
}
