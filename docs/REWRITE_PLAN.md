# Nexus Go 重写补全计划

**生成日期**: 2026-05-10
**数据来源**: 6 个并行 agent 深度分析 (Go 重写 + Python 原版 + claw-code Rust 实现)

---

## 一、总览

| 维度 | Python 原版 | Go 重写 (Nexus) | 完成度 |
|------|------------|-----------------|--------|
| 源文件数 | 1562 .py | 203 .go | — |
| 核心模块 | ~40+ 包 | 16 internal 包 | — |
| 工具数 | ~70+ | ~42 | ~60% |
| 网关平台 | ~22 | ~22 | 100% |
| LLM 适配器 | ~8 | 5 (含 Codex) | ~63% |
| **整体完成度** | — | — | **~65-70%** |

---

## 二、缺失功能清单 (按优先级排序)

### P0 — 安全/稳定性关键 (必须立即补齐)

| # | 功能 | 来源 | Go 现状 | 工作量 |
|---|------|------|---------|--------|
| 1 | **AGENTS.md 注入检测** | Python `agent/prompt_builder.py` | 完全缺失 | 2d |
| 2 | **Tool Guardrails (工具调用循环守卫)** | Python `agent/tool_guardrails.py` | 完全缺失 | 3d |
| 3 | **File Safety (敏感文件写保护)** | Python `agent/file_safety.py` | 完全缺失 | 2d |
| 4 | **OSV 恶意包检查** | Python `tools/osv_check.py` | 完全缺失 | 2d |
| 5 | **Tirith 安全扫描** | Python `tools/tirith_security.py` | 完全缺失 | 3d |
| 6 | **Safe Stdio (broken pipe 容错)** | Python `run_agent.py` `_SafeWriter` | 完全缺失 | 1d |
| 7 | **Models.dev 模型数据库集成** | Python `agent/models_dev.py` | 完全缺失 | 3d |
| 8 | **测试套件补全** | Python `tests/` | 覆盖率极低 | 5d |

**P0 小计: ~21 人天**

### P1 — 功能完整性 (核心体验)

| # | 功能 | 来源 | Go 现状 | 工作量 |
|---|------|------|---------|--------|
| 9 | **i18n 国际化** (8 种语言) | Python `agent/i18n.py` + `locales/` | 完全缺失 | 5d |
| 10 | **execute_code PTC 架构** | Python `tools/code_execution_tool.py` | 只有简单子进程 | 5d |
| 11 | **Image Routing (智能视觉路由)** | Python `agent/image_routing.py` | 完全缺失 | 2d |
| 12 | **Think Scrubber (流式 think 清理)** | Python `agent/think_scrubber.py` | 完全缺失 | 2d |
| 13 | **Tool Result Storage (大结果持久化)** | Python `tools/tool_result_storage.py` | 完全缺失 | 3d |
| 14 | **Account Usage (账户用量查询)** | Python `agent/account_usage.py` | 完全缺失 | 2d |
| 15 | **Environment Tool Call Parsers** (11 种模型) | Python `environments/tool_call_parsers/` | 完全缺失 | 5d |
| 16 | **Delegate 增强** (并行子代理、审批回调) | Python `tools/delegate_tool.py` | 基础委派 | 4d |
| 17 | **上下文压缩增强** (结构化摘要模板) | Python `agent/context_compressor.py` | 基础 prune+summarize | 3d |
| 18 | **凭证池增强** (8+ 来源自发现) | Python `agent/credential_pool.py` | 基础池 | 3d |
| 19 | **StreamingContextScrubber** | Python `agent/memory_manager.py` | 完全缺失 | 1d |
| 20 | **Nous Rate Guard** | Python `agent/nous_rate_guard.py` | 完全缺失 | 2d |
| 21 | **SOUL.md / .hermes.md 人格文件** | Python `agent/prompt_builder.py` | 完全缺失 | 1d |
| 22 | **Tool Use Enforcement** (模型适配) | Python `agent/prompt_builder.py` | 完全缺失 | 2d |
| 23 | **Proxy 支持** | Python `run_agent.py` | 完全缺失 | 1d |

**P1 小计: ~41 人天**

### P2 — 生态集成 (用户拓展)

| # | 功能 | 来源 | Go 现状 | 工作量 |
|---|------|------|---------|--------|
| 24 | **Google OAuth PKCE** | Python `agent/google_oauth.py` | 完全缺失 | 3d |
| 25 | **Copilot ACP Client** | Python `agent/copilot_acp_client.py` | 完全缺失 | 3d |
| 26 | **LM Studio Reasoning** | Python `agent/lmstudio_reasoning.py` | 完全缺失 | 2d |
| 27 | **Onboarding (新手引导)** | Python `agent/onboarding.py` | 完全缺失 | 2d |
| 28 | **Manual Compression Feedback** | Python `agent/manual_compression_feedback.py` | 完全缺失 | 1d |
| 29 | **Context Engine 插件接口** | Python `plugins/context_engine/` | 完全缺失 | 3d |
| 30 | **Browserbase 云浏览器** | Python `tools/browser_tool.py` | 缺少云后端 | 3d |
| 31 | **Moonshot/GLM Schema 适配** | Python `agent/moonshot_schema.py` | 完全缺失 | 2d |
| 32 | **Docker 支持** | Python `Dockerfile` | 完全缺失 | 2d |

**P2 小计: ~21 人天**

### P3 — 增强功能 (锦上添花)

| # | 功能 | 来源 | 工作量 |
|---|------|------|--------|
| 33 | Computer Use 桌面控制 | Python | 5d |
| 34 | Kanban 看板系统 | Python | 4d |
| 35 | Voice Mode 语音模式 | Python | 3d |
| 36 | Skin Engine 皮肤引擎 | Python | 3d |
| 37 | Shell Completion | Python | 2d |
| 38 | RL Training Tool | Python | 4d |
| 39 | Kanban/Achievements/Spotify 插件 | Python | 5d |
| 40 | Clipboard/Dump/Oneshot 等 CLI 工具 | Python | 3d |

**P3 小计: ~29 人天**

---

## 三、从 claw-code (Rust) 借鉴的精华设计

claw-code 是 Claude Code 的 Rust 重新实现，其架构设计非常成熟。以下是值得移植到 Go 的核心设计模式：

### 3.1 Agent 循环核心 (conversation.rs)

**精华**: 泛型参数化的对话运行时，通过 trait 接口完全解耦 API 和工具执行。

```
ConversationRuntime<C: ApiClient, T: ToolExecutor>
```

**Go 移植方案**:
```go
// internal/agent/runtime.go
type ConversationRuntime struct {
    session      *session.Session
    apiClient    llm.ProviderClient    // 接口
    toolExecutor tool.Executor         // 接口
    hooks        hooks.Runner
    permissions  permissions.Policy
}
```

**工作量**: 3d (重构现有 conversation.go，引入接口抽象)

### 3.2 Hook 拦截链 (hooks.rs)

**精华**: 每次工具调用前后的 shell 脚本 hook，可修改输入、拒绝执行、追加反馈。

**Go 移植方案**:
```go
// internal/hooks/runner.go
type HookRunner struct {
    preToolUse  []HookEntry
    postToolUse []HookEntry
}

type HookResult struct {
    Decision   string // "allow", "deny", "modify"
    Modified   map[string]any
    Feedback   string
}
```

**工作量**: 3d

### 3.3 多层权限模型 (permissions.rs)

**精华**: 五级权限 (ReadOnly → WorkspaceWrite → DangerFullAccess → Prompt → Allow)，每个工具有声明的最低权限要求，配置规则可覆盖，hook 可动态覆盖。

**Go 移植方案**:
```go
// internal/permissions/policy.go
type PermissionLevel int
const (
    ReadOnly PermissionLevel = iota
    WorkspaceWrite
    DangerFullAccess
    Prompt
    Allow
)

type PermissionPolicy struct {
    rules    []PermissionRule
    overrides map[string]PermissionLevel
}
```

**工作量**: 3d (增强现有 approval.go)

### 3.4 会话压缩 (compact.rs)

**精华**: 保留最近 N 条消息原文，旧消息压缩为摘要，插入续接指令。摘要支持二次压缩（去重行、截断长行、行预算）。

**Go 移植方案**: 增强现有 `internal/context/compressor.go`，加入：
- 续接指令注入
- 二次压缩 (summary compression)
- 健康探测 (压缩后 glob_search 验证)

**工作量**: 2d

### 3.5 会话持久化 (session.rs)

**精华**: JSONL 格式，原子写入（temp + rename），自动轮转（256KB 阈值，最多 3 个轮转文件），格式自动迁移。

**Go 移植方案**:
```go
// internal/session/store.go
type Store struct {
    dir         string
    maxSize     int64  // 256KB
    maxRotated  int    // 3
}

func (s *Store) Append(msg ConversationMessage) error  // 原子追加
func (s *Store) Rotate() error                          // 自动轮转
func (s *Store) Load() ([]ConversationMessage, error)   // 自动迁移
```

**工作量**: 3d

### 3.6 自动恢复配方 (recovery_recipes.rs)

**精华**: 7 种失败场景的预定义恢复步骤序列，含升级策略 (AlertHuman / LogAndContinue / Abort)。

**Go 移植方案**:
```go
// internal/agent/recovery.go
type FailureScenario int
const (
    TrustPromptUnresolved FailureScenario = iota
    ProviderFailure
    McpHandshakeFailure
    // ...
)

type RecoveryRecipe struct {
    Steps    []RecoveryStep
    Escalation EscalationPolicy
}
```

**工作量**: 3d

### 3.7 MCP 生命周期硬化 (mcp_lifecycle_hardened.rs)

**精华**: 11 阶段状态机 (ConfigLoad → ServerRegistration → SpawnConnect → InitializeHandshake → ToolDiscovery → ResourceDiscovery → Ready → Invocation → ErrorSurfacing → Shutdown → Cleanup)，每阶段有结构化错误报告。

**Go 移植方案**: 增强现有 `internal/mcp/`，加入状态机和降级报告。

**工作量**: 3d

### 3.8 SSE 增量解析 (sse.rs)

**精华**: buffer-based 增量解析器，支持 `\n\n` 和 `\r\n\r\n` 帧分隔符，携带 provider/model 上下文。

**Go 移植方案**:
```go
// internal/llm/sse.go
type SSEParser struct {
    buffer   []byte
    provider string
    model    string
}

func (p *SSEParser) Push(chunk []byte) ([]StreamEvent, error)
func (p *SSEParser) Finish() ([]StreamEvent, error)
```

**工作量**: 2d (替换现有的简单 SSE 处理)

### 3.9 错误分类体系 (error.rs)

**精华**: `safe_failure_class()` 返回稳定字符串 (provider_auth, context_window, provider_rate_limit 等)，支持结构化遥测。`is_retryable()` 递归解包嵌套错误。

**Go 移植方案**: 增强现有 `internal/llm/error.go`。

**工作量**: 2d

### 3.10 Worker 生命周期状态机 (worker_boot.rs)

**精华**: Spawning → TrustRequired → ToolPermissionRequired → ReadyForPrompt → Running → Finished/Failed，支持信任门检测、就绪握手、提示投递错误检测。

**Go 移植方案**:
```go
// internal/worker/boot.go
type WorkerStatus int
const (
    Spawning WorkerStatus = iota
    TrustRequired
    ToolPermissionRequired
    ReadyForPrompt
    Running
    Finished
    Failed
)
```

**工作量**: 3d

### 3.11 Prompt Cache 追踪 (prompt_cache.rs)

**精华**: FNV 指纹哈希请求，磁盘缓存完成结果，可配 TTL，追踪 cache hit/miss 异常下降。

**Go 移植方案**: 增强现有 `internal/context/prompt_cache.go`。

**工作量**: 2d

### 3.12 Provider 回退链 (client.rs)

**精华**: 主提供者返回可重试故障 (429/500/503) 时，自动尝试回退链中的下一个。

**Go 移植方案**: 增强现有 `internal/agent/fallback.go`。

**工作量**: 2d

---

## 四、实施路线图

### Phase 1: 安全加固 (第 1-3 周)

**目标**: 补齐所有 P0 安全缺失，使 Go 版本达到生产安全标准。

| 周次 | 任务 | 文件 |
|------|------|------|
| W1 | AGENTS.md 注入检测 | `internal/context/builder.go` (增强) |
| W1 | File Safety 敏感文件保护 | `internal/agent/file_safety.go` (新建) |
| W1 | Safe Stdio 容错 | `internal/agent/safe_writer.go` (新建) |
| W2 | Tool Guardrails 循环守卫 | `internal/agent/guardrails.go` (新建) |
| W2 | OSV 恶意包检查 | `internal/tool/osv_check.go` (新建) |
| W2 | Tirith 安全扫描 | `internal/tool/tirith.go` (新建) |
| W3 | Models.dev 集成 | `internal/llm/models_dev.go` (新建) |
| W3 | 错误分类体系增强 | `internal/llm/error.go` (增强) |

### Phase 2: 核心体验 (第 4-7 周)

**目标**: 补齐 P1 功能，使 Go 版本在日常使用中与 Python 版本对等。

| 周次 | 任务 | 文件 |
|------|------|------|
| W4 | i18n 国际化框架 + 中英文 | `internal/i18n/` (新建) |
| W4 | Think Scrubber 流式清理 | `internal/agent/think_scrubber.go` (新建) |
| W4 | Image Routing 视觉路由 | `internal/agent/image_routing.go` (新建) |
| W5 | execute_code PTC 架构 | `internal/tool/code_execute.go` (重写) |
| W5 | Tool Result Storage | `internal/tool/result_store.go` (新建) |
| W5 | Delegate 并行增强 | `internal/tool/delegate.go` (增强) |
| W6 | 上下文压缩增强 | `internal/context/compressor.go` (增强) |
| W6 | 凭证池多来源 | `internal/credential/pool.go` (增强) |
| W6 | StreamingContextScrubber | `internal/agent/scrubber.go` (新建) |
| W7 | Tool Parsers (11 种模型) | `internal/environments/parsers/` (新建) |
| W7 | SOUL.md + Tool Use Enforcement | `internal/context/builder.go` (增强) |

### Phase 3: 架构借鉴 (第 8-11 周)

**目标**: 从 claw-code 移植核心设计模式，提升 Go 版本架构质量。

| 周次 | 任务 | 文件 |
|------|------|------|
| W8 | Hook 拦截链 | `internal/hooks/` (新建) |
| W8 | 多层权限模型 | `internal/permissions/` (重写) |
| W8 | SSE 增量解析 | `internal/llm/sse.go` (新建) |
| W9 | 会话持久化 (JSONL + 轮转) | `internal/session/` (重写) |
| W9 | 自动恢复配方 | `internal/agent/recovery.go` (新建) |
| W9 | Provider 回退链 | `internal/agent/fallback.go` (增强) |
| W10 | MCP 生命周期硬化 | `internal/mcp/` (增强) |
| W10 | Worker 状态机 | `internal/worker/` (新建) |
| W10 | Prompt Cache 追踪 | `internal/context/prompt_cache.go` (增强) |
| W11 | Agent Runtime 接口抽象 | `internal/agent/runtime.go` (重构) |
| W11 | 测试套件补全 | `*_test.go` (大量) |

### Phase 4: 生态拓展 (第 12-14 周)

**目标**: 补齐 P2 生态集成功能。

| 周次 | 任务 | 文件 |
|------|------|------|
| W12 | Google OAuth PKCE | `internal/credential/google.go` (新建) |
| W12 | Copilot ACP Client | `internal/llm/copilot.go` (新建) |
| W12 | LM Studio Reasoning | `internal/llm/lmstudio.go` (新建) |
| W13 | Onboarding 新手引导 | `internal/agent/onboarding.go` (新建) |
| W13 | Context Engine 插件接口 | `internal/plugin/context.go` (新建) |
| W13 | Browserbase 云浏览器 | `internal/tool/browser_browserbase.go` (新建) |
| W14 | Docker 支持 | `Dockerfile` + `docker-compose.yml` |
| W14 | Moonshot/GLM Schema | `internal/llm/schema_adapters.go` (新建) |
| W14 | Proxy 支持 | `internal/llm/client.go` (增强) |

### Phase 5: 增强功能 (第 15+ 周)

**目标**: P3 锦上添花功能，按需实施。

- Computer Use 桌面控制
- Kanban 看板系统
- Voice Mode 语音模式
- Skin Engine / Shell Completion
- RL Training Tool
- 各种 CLI 增强工具

---

## 五、关键架构改进建议

### 5.1 引入 `internal/session/` 包

当前 Nexus 的会话管理分散在 `internal/state/` (SQLite) 和 `internal/agent/` (内存)。建议：
- 新建 `internal/session/` 统一会话抽象
- 支持 JSONL 持久化 (借鉴 claw-code)
- 支持 SQLite 持久化 (现有)
- 自动轮转 + 原子写入

### 5.2 引入 `internal/hooks/` 包

当前 Nexus 没有 hook 系统。建议：
- 新建 `internal/hooks/` 包
- Pre/Post tool-use hook (shell 脚本)
- Pre/Post LLM-call hook
- 与权限系统联动

### 5.3 增强 `internal/permissions/`

当前 Nexus 的 approval.go 只有三级模式 (off/smart/always)。建议：
- 扩展为五级权限模型
- 支持配置规则覆盖
- 支持 hook 动态覆盖
- Bash 命令内容动态分类

### 5.4 统一错误分类

当前 Nexus 的 `internal/llm/error.go` 已有基础分类。建议：
- 引入 `safe_failure_class()` 模式
- 所有错误通过分类器输出稳定 token
- 支持结构化遥测

### 5.5 Agent Runtime 接口化

当前 Nexus 的 `conversation.go` 直接依赖具体实现。建议：
- 引入 `ApiClient` 和 `ToolExecutor` 接口
- 支持 mock 注入测试
- 支持运行时替换 (如远程工具执行)

---

## 六、总工作量估算

| 阶段 | 人天 | 周数 |
|------|------|------|
| Phase 1: 安全加固 | 21d | 3 周 |
| Phase 2: 核心体验 | 41d | 4 周 |
| Phase 3: 架构借鉴 | 30d | 4 周 |
| Phase 4: 生态拓展 | 21d | 3 周 |
| Phase 5: 增强功能 | 29d | 按需 |
| **总计** | **~142d** | **~14 周 (3.5 月)** |

---

## 七、文件清单 (需新建/修改)

### 新建文件 (约 30 个)

```
internal/agent/file_safety.go          # 敏感文件保护
internal/agent/guardrails.go           # 工具调用循环守卫
internal/agent/safe_writer.go          # Safe Stdio
internal/agent/think_scrubber.go       # Think 标签清理
internal/agent/image_routing.go        # 图像路由
internal/agent/scrubber.go             # StreamingContextScrubber
internal/agent/onboarding.go           # 新手引导
internal/agent/recovery.go             # 自动恢复配方
internal/agent/file_safety_test.go     # 测试
internal/agent/guardrails_test.go      # 测试
internal/tool/osv_check.go             # OSV 恶意包检查
internal/tool/tirith.go                # Tirith 安全扫描
internal/tool/result_store.go          # 大结果持久化
internal/tool/browser_browserbase.go   # Browserbase 云浏览器
internal/llm/models_dev.go             # Models.dev 集成
internal/llm/sse.go                    # SSE 增量解析
internal/llm/copilot.go                # Copilot ACP
internal/llm/lmstudio.go               # LM Studio
internal/llm/schema_adapters.go        # Schema 适配
internal/i18n/i18n.go                  # 国际化
internal/i18n/locales/en.yaml          # 英文
internal/i18n/locales/zh.yaml          # 中文
internal/hooks/runner.go               # Hook 执行器
internal/hooks/types.go                # Hook 类型
internal/permissions/policy.go         # 权限策略
internal/session/store.go              # JSONL 会话存储
internal/session/rotate.go             # 会话轮转
internal/worker/boot.go                # Worker 状态机
internal/credential/google.go          # Google OAuth
internal/environments/parsers/*.go     # 11 种模型解析器
```

### 需增强的现有文件 (约 15 个)

```
internal/agent/conversation.go         # Agent Runtime 接口化
internal/agent/fallback.go             # Provider 回退链
internal/context/builder.go            # 注入检测 + SOUL.md + Tool Enforcement
internal/context/compressor.go         # 结构化摘要 + 二次压缩
internal/context/prompt_cache.go       # Cache 追踪
internal/tool/delegate.go              # 并行子代理
internal/tool/code_execute.go          # PTC 架构重写
internal/llm/error.go                  # 错误分类增强
internal/llm/client.go                 # Proxy 支持
internal/credential/pool.go            # 多来源自发现
internal/mcp/client.go                 # MCP 生命周期硬化
internal/approval/approval.go          # 五级权限
internal/gateway/session.go            # busy input + session context
```

---

## 八、结论

Nexus Go 重写已经完成了 **~65-70%** 的功能，核心架构清晰、代码质量高。主要缺失集中在三个领域：

1. **安全层** (5 个独立安全模块缺失) — 最高优先级
2. **生态集成** (模型发现、OAuth、推理适配) — 用户体验关键
3. **架构成熟度** (hook 系统、权限模型、恢复机制) — 从 claw-code 借鉴

通过 14 周的系统性补全，Nexus 可以达到与 Python 原版功能对等的水平，同时凭借 Go 的性能优势（原生并发、单二进制部署、零 CGo 依赖）提供更好的用户体验。
