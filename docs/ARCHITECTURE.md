# Nexus Agent Go 重写 —— 架构设计文档

> 版本: v0.1.0  
> 日期: 2026-04-28  
> 作者: Hatedatastructures  
> 语言: Go 1.23+, 全部中文注释

---

## 目录

1. [设计目标与原则](#1-设计目标与原则)
2. [总体架构](#2-总体架构)
3. [模块详解](#3-模块详解)
4. [数据流设计](#4-数据流设计)
5. [并发模型](#5-并发模型)
6. [配置系统](#6-配置系统)
7. [关键决策与权衡](#7-关键决策与权衡)
8. [实现计划](#8-实现计划)
9. [Go 依赖选型](#9-go-依赖选型)
10. [可观测性](#10-可观测性)
11. [与 Python 版的核心差异](#11-与-python-版的核心差异)
12. [新增包描述](#12-新增包描述)
13. [安全架构](#13-安全架构)
14. [Provider 回退链](#14-provider-回退链)
15. [配置参考](#15-配置参考)

---

## 1. 设计目标与原则

### 1.1 核心目标

将 Nexus Agent 从 Python 完整重写为 Go，实现：

| 目标 | 说明 |
|------|------|
| **单一二进制部署** | `go build` 产出独立可执行文件，零运行时依赖 |
| **内存效率** | 目标内存占用降至 Python 版的 1/5 ~ 1/10 |
| **并发原生** | goroutine 替换 asyncio + ThreadPoolExecutor |
| **启动速度** | 毫秒级启动（Python 版 2~5 秒） |
| **类型安全** | 编译期消灭运行时类型错误 |
| **交叉编译** | `GOOS=linux go build` 出所有平台二进制 |

### 1.2 设计原则

1. **接口优先**: 每个核心组件先定义接口，再实现
2. **无 CGo**: 全链路纯 Go（`modernc.org/sqlite`、`rod` 浏览器、原生 HTTP/SSE）
3. **context.Context 贯穿**: 所有可能阻塞的函数第一个参数是 `ctx`
4. **单一二进制**: SQL schema、默认配置、web 资源通过 `embed.FS` 嵌入
5. **显式错误**: 不 panic，返回 `(T, error)`；启动阶段例外
6. **结构化日志**: `log/slog`，带 session_id 上下文
7. **优雅关闭**: 信号量监听 → 拒绝新请求 → 排空进行中会话 → 清理资源
8. **全部中文注释**: 包文档、函数文档、行内注释全部使用中文

---

## 2. 总体架构

### 2.1 分层架构图

```
┌─────────────────────────────────────────────────────────────────┐
│                    入口层 (cmd/)                                  │
│   nexus (CLI)  │  nexus-gateway (消息网关)  │  nexus-acp      │
├─────────────────────────────────────────────────────────────────┤
│                    核心代理层 (internal/agent/)                    │
│   AIAgent —— 对话循环 ── 迭代预算 ── 故障转移 ── 流式回调         │
├─────────────────────────────────────────────────────────────────┤
│  LLM 层        │  工具系统      │  上下文层     │  记忆层  │ 技能层│
│  (internal/    │  (internal/   │  (internal/  │  (inter- │ (int- │
│   llm/)        │   tool/)      │   context/)  │   nal/   │ ernal/│
│   Provider     │  Tool         │  Builder     │  memory/ │ skill/│
│   接口         │  接口         │  Compressor  │  Provider│       │
│                │  Registry     │              │  接口    │       │
├─────────────────────────────────────────────────────────────────┤
│  网关 (internal/gateway/)   │  Cron 调度  │  状态层 (internal/    │
│  PlatformAdapter 接口       │  (internal/ │  state/) SQLite+FTS5 │
│  AgentCache (LRU+TTL)      │   cron/)    │                     │
├─────────────────────────────────────────────────────────────────┤
│             基础设施 (internal/config/, pkg/, mcp/)               │
│  Viper配置 │ 凭证池 │ 审批引擎 │ 沙箱环境 │ slog日志 │ HTTP客户端    │
└─────────────────────────────────────────────────────────────────┘
```

### 2.2 模块依赖图

依赖方向: **严格从上到下，禁止循环引用**

```
cmd/nexus ────────────────┐
cmd/nexus-gateway ────────┤
cmd/nexus-acp ────────────┤
                            ▼
                   internal/agent ──────────────┐
                        │                       │
          ┌─────────────┼───────────────┐       │
          ▼             ▼               ▼       │
   internal/llm   internal/tool   internal/context│
   internal/      internal/      internal/       │
   credential/    approval/      memory/         │
   pkg/httputil/  internal/      internal/       │
                  sandbox/       skill/          │
                                  │               │
                   internal/state ◄───────────────┘
                        │
                   internal/config
                        │
                   (stdlib + viper + modernc/sqlite)
```

### 2.3 项目目录结构

```
Go/
├── go.mod
├── go.sum
├── Taskfile.yml                    # 构建/测试/lint 任务
├── Dockerfile                      # 多阶段构建
├── README.md
│
├── cmd/
│   ├── hermes/                     # CLI 入口 (交互式终端)
│   │   └── main.go
│   ├── nexus-gateway/             # 消息网关入口
│   │   └── main.go
│   └── nexus-acp/                 # ACP 服务器入口
│       └── main.go
│
├── internal/
│   ├── agent/                      # 核心代理
│   │   ├── agent.go               # AIAgent 结构体
│   │   ├── conversation.go        # RunConversation 主循环
│   │   ├── options.go             # 函数式选项模式
│   │   ├── retry.go               # 重试逻辑
│   │   ├── fallback.go            # 故障转移
│   │   ├── stream.go              # 流式处理
│   │   └── agent_test.go
│   │
│   ├── llm/                        # LLM 提供者层
│   │   ├── provider.go            # Provider 接口定义
│   │   ├── types.go               # ChatRequest/ChatResponse/Message/StreamDelta
│   │   ├── transport.go           # Transport 接口 + 注册表
│   │   ├── client.go              # HTTP 客户端 (连接池/重试/代理)
│   │   ├── stream.go              # SSE 流解析
│   │   ├── openai.go              # OpenAI 兼容传输
│   │   ├── anthropic.go           # Anthropic Messages API 传输
│   │   ├── anthropic_thinking.go  # Anthropic 扩展思维配置
│   │   ├── gemini.go              # Google Gemini 传输
│   │   ├── bedrock.go             # AWS Bedrock 传输
│   │   ├── codex.go               # Codex Responses API 传输
│   │   ├── caching.go             # 提示词缓存控制
│   │   └── error.go               # 错误分类 (ErrorClassifier)
│   │
│   ├── tool/                       # 工具系统
│   │   ├── tool.go                # Tool 接口 + ToolSchema + ToolEntry
│   │   ├── registry.go            # ToolRegistry (注册/分发/查询)
│   │   ├── toolsets.go            # 工具集组合与解析
│   │   ├── discovery.go           # 工具自动发现
│   │   ├── terminal.go            # 终端命令执行
│   │   ├── file.go                # 文件操作 (读/写/搜索/编辑)
│   │   ├── web.go                 # 网页搜索与提取
│   │   ├── browser.go             # 浏览器自动化 (基于 rod)
│   │   ├── code.go                # 代码执行
│   │   ├── delegate.go            # 子代理委派
│   │   ├── memory.go              # 记忆管理
│   │   ├── skill.go               # 技能管理
│   │   ├── mcp.go                 # MCP 工具桥接
│   │   ├── todo.go                # 待办列表
│   │   ├── parallel.go            # 并行工具执行控制
│   │   └── registry_test.go
│   │
│   ├── gateway/                    # 消息网关
│   │   ├── runner.go              # GatewayRunner 主控
│   │   ├── session.go             # 会话管理 (SessionSource/SessionManager)
│   │   ├── cache.go               # AgentCache (LRU + TTL 驱逐)
│   │   ├── stream.go              # StreamConsumer (缓冲/限速/渐进编辑)
│   │   ├── delivery.go            # DeliveryManager (格式化/截断/分页)
│   │   ├── hooks.go               # 消息钩子 (预处理/后处理)
│   │   └── platforms/
│   │       ├── adapter.go         # PlatformAdapter 接口 + MessageEvent/SendResult
│   │       ├── telegram.go        # Telegram 适配器
│   │       ├── discord.go         # Discord 适配器
│   │       ├── slack.go           # Slack 适配器
│   │       ├── whatsapp.go        # WhatsApp 适配器
│   │       ├── wechat.go          # 微信公众号/客服
│   │       ├── feishu.go          # 飞书适配器
│   │       ├── dingtalk.go        # 钉钉适配器
│   │       ├── signal.go          # Signal 适配器
│   │       ├── matrix.go          # Matrix 适配器
│   │       ├── email.go           # 邮件适配器
│   │       ├── sms.go             # 短信适配器
│   │       ├── webhook.go         # Webhook 适配器
│   │       ├── api_server.go      # OpenAI 兼容 API 服务器
│   │       └── adapter_test.go
│   │
│   ├── state/                      # 状态持久化
│   │   ├── db.go                  # Store 结构体 (WAL/连接/迁移)
│   │   ├── migrate.go             # 版本化迁移框架
│   │   ├── sessions.go            # Session CRUD
│   │   ├── messages.go            # Message CRUD
│   │   ├── search.go              # FTS5 全文搜索
│   │   ├── prune.go               # 自动清理 (auto_prune)
│   │   └── schema.sql             # SQL 模式定义 (embed.FS)
│   │
│   ├── context/                    # 上下文工程
│   │   ├── builder.go             # SystemPromptBuilder (身份/平台提示/记忆)
│   │   ├── builder_skills.go      # 技能索引注入
│   │   ├── builder_context.go     # 上下文文件注入 (AGENTS.md/SOUL.md)
│   │   ├── compressor.go          # ContextCompressor (压缩编排)
│   │   ├── compressor_summarize.go# LLM 总结生成
│   │   ├── compressor_prune.go    # 工具输出修剪
│   │   └── prompt_cache.go        # Anthropic 缓存控制点
│   │
│   ├── memory/                     # 记忆系统
│   │   ├── provider.go            # MemoryProvider 接口
│   │   ├── builtin.go             # 内置文件存储 (MEMORY.md / USER.md)
│   │   ├── manager.go             # MemoryManager 编排
│   │   └── scrubber.go            # StreamingContextScrubber (流式清洗)
│   │
│   ├── skill/                      # 技能系统
│   │   ├── skill.go               # Skill 数据模型 (YAML frontmatter + Markdown)
│   │   ├── loader.go              # SkillLoader (SKILL.md 解析)
│   │   ├── manager.go             # SkillManager (CRUD)
│   │   ├── hub.go                 # SkillsHub (agentskills.io 集成)
│   │   ├── preprocess.go          # 模板变量替换 ($NEXUS_HOME 等)
│   │   └── index.go               # 技能索引缓存
│   │
│   ├── cron/                       # 定时调度
│   │   ├── scheduler.go           # CronScheduler (tick/文件锁)
│   │   ├── jobs.go                # JobManager (CRUD/持久化/到期判定)
│   │   ├── executor.go            # JobExecutor (Agent 执行/输出保存)
│   │   └── delivery.go            # 跨平台投递
│   │
│   ├── config/                     # 配置管理
│   │   ├── config.go              # 全部配置结构体 + Viper 加载器
│   │   └── defaults.yaml          # 默认配置 (embed.FS)
│   │
│   ├── credential/                 # 凭证管理
│   │   ├── pool.go                # CredentialPool (多凭证/轮换/故障转移)
│   │   └── source.go              # 凭证来源 (env/file/OAuth/Claude凭证)
│   │
│   ├── approval/                   # 命令审批
│   │   └── approval.go            # 危险检测/模式匹配/审批流水线
│   │
│   ├── sandbox/                    # 沙箱执行环境
│   │   ├── environment.go         # Environment 接口
│   │   ├── local.go               # 本地子进程 (os/exec)
│   │   ├── docker.go              # Docker exec
│   │   ├── ssh.go                 # SSH 远程执行
│   │   └── process.go             # ProcessHandle 接口 + PTY 管理
│   │
│   └── mcp/                        # MCP 协议
│       ├── client.go              # MCP 客户端 (SSE transport)
│       ├── server.go              # MCP 服务器 (暴露工具为 MCP 资源)
│       └── protocol.go            # JSON-RPC 2.0 协议类型
│
├── pkg/                            # 公共工具包
│   ├── jsonutil/
│   │   └── jsonutil.go            # JSON 安全构建/清理
│   ├── httputil/
│   │   └── client.go              # HTTP 客户端工具/代理/TLS
│   └── logutil/
│       └── logger.go              # slog 配置/会话上下文
│
├── skills/                         # 内置技能 (Markdown文件, embed.FS)
│   └── ...
│
├── docs/                           # 文档
│   ├── ARCHITECTURE.md             # 本架构文档
│   └── MODULES.md                  # 模块开发指南
│
└── testutil/                       # 测试工具
    └── fixtures.go                 # Mock LLM 服务器 / Mock 平台适配器
```

---

## 3. 模块详解

### 3.1 核心代理层 (`internal/agent/`)

**职责**: 对话循环编排，工具调用分发，状态更新，故障恢复

#### 核心结构体

```go
// AIAgent 是对话代理的核心结构体
// 管理一次会话的完整生命周期
type AIAgent struct {
    // ── LLM 配置 ──
    provider       llm.Provider           // LLM 提供者实例
    model          string                  // 模型名称
    maxTokens      int                     // 最大生成 token 数
    reasoningCfg   *ReasoningConfig        // 推理/思维链配置

    // ── 子系统 ──
    registry        *tool.Registry         // 工具注册中心
    memoryManager   *memory.Manager        // 记忆管理器
    skillManager    *skill.Manager         // 技能管理器
    contextBuilder  *context.Builder       // 系统提示词构建器
    compressor      *context.Compressor    // 上下文压缩器
    state           *state.Store           // 持久化存储
    credentialPool  *credential.Pool       // 凭证池
    approvalChecker *approval.Checker      // 命令审批
    sandboxEnv      sandbox.Environment    // 沙箱环境

    // ── 会话管理 ──
    sessionID       string                 // 会话唯一标识
    platform        string                 // 平台: "cli"/"telegram"/"discord"...
    userID          string                 // 用户 ID (网关会话)
    chatID          string                 // 聊天 ID (网关会话)

    // ── 回调函数 ──
    streamCallback  func(delta string)     // 文本增量回调
    toolCallback    func(name string, args map[string]any) // 工具进度回调
    statusCallback  func(msg string)       // 状态消息回调
    reasoningCallback func(reasoning string) // 推理过程回调

    // ── 内部状态 ──
    mu                sync.Mutex           // 并发保护
    iterationBudget   *IterationBudget     // 迭代预算 (默认 90 次)
    cachedSystemPrompt string              // 缓存的系统提示词
    messages          []llm.Message        // 当前对话消息列表
    todoStore         *TodoStore           // 待办列表内存存储
    startedAt         time.Time            // 会话开始时间
    lastActivityAt    time.Time            // 最后活动时间 (用于空闲超时驱逐)

    // ── 重试与故障转移 ──
    maxRetries        int                  // 最大重试次数
    fallbackModel     string               // 备选模型
    fallbackProvider  llm.Provider         // 备选提供者
}
```

#### 对话循环流程

```
RunConversation(ctx, userMessage, history, systemMessage)
    │
    ├── 1. 前置处理
    │   ├── 重置重试计数器
    │   ├── 构建/恢复系统提示词 (cachedSystemPrompt)
    │   ├── 拼装消息列表: [systemPrompt] + history + [userMessage]
    │   └── 预检查上下文压缩 (token估算 >= 阈值)
    │
    ├── 2. 主循环 (while budget.Consume())
    │   │
    │   ├── 2a. 记忆预取
    │   │   └── memoryManager.Prefetch(userQuery)
    │   │
    │   ├── 2b. LLM API 调用 (带内层重试循环)
    │   │   ├── 构建 API 参数 (buildAPIKwargs)
    │   │   ├── 发起请求 (provider.CreateChatCompletion 或 Stream)
    │   │   ├── 错误分类 (errorClassifier.Classify)
    │   │   ├── 恢复策略:
    │   │   │   ├── 上下文溢出 → 压缩后重试
    │   │   │   ├── 认证/计费 → 轮换凭证或激活备选
    │   │   │   ├── 速率限制 → 退避重试或轮换凭证
    │   │   │   ├── 格式错误 → 备选模型或中止
    │   │   │   └── 服务端错误 → 退避重试
    │   │   └── 成功: 追踪用量，缓存上下文长度
    │   │
    │   ├── 2c. 响应处理
    │   │   ├── stopReason == "end_turn" → 无工具调用，退出循环
    │   │   ├── stopReason == "max_tokens" → 继续生成 (最多3次)
    │   │   └── 有 toolCalls → 进入工具执行
    │   │
    │   ├── 2d. 工具执行 (executeToolCalls)
    │   │   ├── 并行度判断 (shouldParallelize)
    │   │   ├── 并行路径: goroutine 并发执行
    │   │   ├── 顺序路径: 逐个执行 (危险命令需审批)
    │   │   ├── 结果拼装为 tool-role 消息
    │   │   └── 触发 toolCallback 通知
    │   │
    │   └── 2e. 上下文压缩检查
    │       └── compressor.ShouldCompress() → Compress()
    │
    ├── 3. 后置处理
    │   ├── 保存消息到 state.Store
    │   ├── 同步记忆 (memoryManager.SyncTurn)
    │   ├── 触发 onSessionEnd 钩子
    │   └── 返回 TurnResult
```

#### TurnResult 结构

```go
type TurnResult struct {
    FinalResponse string          // 最终回复文本
    Messages      []llm.Message   // 完整消息历史
    APICalls      int             // API 调用次数
    ToolCalls     int             // 工具调用次数
    TotalTokens   int64           // 总 token 用量
    CachedTokens  int64           // 缓存命中 token 数
    CostUSD       float64         // 估算费用 (美元)
    Duration      time.Duration   // 总耗时
    Completed     bool            // 是否正常完成
    Error         error           // 终止错误 (如有)
}
```

### 3.2 LLM 提供者层 (`internal/llm/`)

**职责**: 多模型后端统一抽象，请求/响应标准化，SSE 流解析，错误分类

#### 核心接口

```go
// Provider 是 LLM 提供者的统一接口
// 所有后端 (OpenAI/Anthropic/Gemini/Bedrock) 必须实现此接口
type Provider interface {
    // 非流式聊天补全
    CreateChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
    // 流式聊天补全，返回文本增量通道
    CreateChatCompletionStream(ctx context.Context, req *ChatRequest) (<-chan *StreamDelta, error)
    // 列出可用模型
    ListModels(ctx context.Context) ([]ModelInfo, error)
    // 提供者标识名称
    Name() string
}
```

#### 统一消息格式

```go
// Message 统一的消息格式 (兼容 OpenAI 结构)
type Message struct {
    Role       string     `json:"role"`                 // system/user/assistant/tool
    Content    string     `json:"content,omitempty"`
    ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
    ToolCallID string     `json:"tool_call_id,omitempty"`
    Name       string     `json:"name,omitempty"`
}

// ChatRequest 统一的聊天补全请求
type ChatRequest struct {
    Model       string            `json:"model"`
    Messages    []Message         `json:"messages"`
    Tools       []ToolSchema      `json:"tools,omitempty"`
    MaxTokens   int               `json:"max_tokens,omitempty"`
    Temperature float64           `json:"temperature,omitempty"`
    Metadata    map[string]any    `json:"-"` // 提供者特定参数
}

// ChatResponse 统一的聊天补全响应
type ChatResponse struct {
    ID           string      `json:"id"`
    Model        string      `json:"model"`
    Content      string      `json:"content,omitempty"`
    ToolCalls    []ToolCall  `json:"tool_calls,omitempty"`
    StopReason   string      `json:"stop_reason"`    // end_turn/max_tokens/tool_use
    Usage        *TokenUsage `json:"usage,omitempty"`
    Reasoning    string      `json:"reasoning,omitempty"`  // 思维链/推理文本
    CachedPrompt bool        `json:"cached_prompt"`        // 是否命中提示缓存
}

// StreamDelta 流式增量
type StreamDelta struct {
    Content   string     `json:"content,omitempty"`
    ToolCalls []ToolCall `json:"tool_calls,omitempty"`
    Reasoning string     `json:"reasoning,omitempty"`
    Usage     *TokenUsage `json:"usage,omitempty"`
    Done      bool       `json:"done"`
    Error     error      `json:"-"`
}

// TokenUsage token 用量统计
type TokenUsage struct {
    PromptTokens     int `json:"prompt_tokens"`
    CompletionTokens int `json:"completion_tokens"`
    TotalTokens      int `json:"total_tokens"`
    CacheReadTokens  int `json:"cache_read_tokens,omitempty"`
    CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
}

// ToolCall 工具调用
type ToolCall struct {
    ID        string         `json:"id"`
    Name      string         `json:"name"`
    Arguments string         `json:"arguments"` // JSON string
    Extra     map[string]any `json:"extra,omitempty"`
}
```

#### 传输层设计

每种 API 模式实现 `Transport` 接口，负责格式转换:

```go
// Transport 负责特定 API 协议的请求构建和响应解析
type Transport interface {
    // API 模式标识: "chat_completions"/"anthropic_messages"/"bedrock_converse"/"codex_responses"
    APIMode() string
    // 构建 HTTP 请求 (含认证头/签名)
    BuildRequest(ctx context.Context, req *ChatRequest, creds credential.Credentials) (*http.Request, error)
    // 解析非流式响应
    ParseResponse(resp *http.Response) (*ChatResponse, error)
    // 解析流式响应
    ParseStream(ctx context.Context, body io.ReadCloser) (<-chan *StreamDelta, error)
    // 构建请求体 (已废弃签名，由 BuildRequest 内部调用)
    ConvertMessages(messages []Message) (any, error)
    ConvertTools(tools []ToolSchema) (any, error)
}
```

**四种传输实现:**

| 传输层 | APIMode | 对应后端 |
|--------|---------|----------|
| `OpenAITransport` | `chat_completions` | OpenAI / 兼容服务 (DeepSeek/Qwen/GLM/Moonshot) |
| `AnthropicTransport` | `anthropic_messages` | Anthropic Messages API / 兼容 (Kimi/MiniMax) |
| `GeminiTransport` | `gemini_api` | Google Gemini API |
| `BedrockTransport` | `bedrock_converse` | AWS Bedrock Converse API |

#### 错误分类

```go
// FailoverReason 故障转移原因枚举
type FailoverReason int

const (
    FailoverUnknown          FailoverReason = iota // 未知错误
    FailoverAuth                                   // 认证错误 (401)
    FailoverAuthPermanent                          // 永久认证错误
    FailoverBilling                                // 计费耗尽 (402)
    FailoverRateLimit                              // 速率限制 (429)
    FailoverOverloaded                             // 服务过载 (529)
    FailoverServerError                            // 服务端错误 (5xx)
    FailoverTimeout                                // 超时
    FailoverContextOverflow                        // 上下文溢出
    FailoverPayloadTooLarge                        // 请求体过大 (413)
    FailoverModelNotFound                          // 模型不存在 (404)
    FailoverFormatError                            // 响应格式错误
    FailoverThinkingSignature                      // 思维链签名冲突
)

// ClassifiedError 分类后的错误
type ClassifiedError struct {
    Reason              FailoverReason // 失败原因
    StatusCode          int            // HTTP 状态码 (0 如果无)
    Provider            string         // 提供者名
    Model               string         // 模型名
    Message             string         // 原始错误消息
    Retryable           bool           // 是否可重试
    ShouldCompress      bool           // 是否需要压缩上下文
    ShouldRotateCred    bool           // 是否需要轮换凭证
    ShouldFallback      bool           // 是否需要切换备选
    IsAnthropicOAuth    bool           // 是否 Anthropic OAuth 错误
}
```

### 3.3 工具系统 (`internal/tool/`)

**职责**: 工具注册/发现/分发，工具集组合，结果标准化

#### 核心接口

```go
// Tool 是所有工具必须实现的接口
type Tool interface {
    // Name 返回工具的唯一标识名 (用于模型调用)
    Name() string
    // Description 返回工具的简短描述
    Description() string
    // Schema 返回 OpenAI 格式的工具 JSON Schema
    Schema() *ToolSchema
    // Execute 执行工具，接收参数映射，返回 JSON 字符串
    // 无论成功或失败，始终返回 JSON 字符串
    Execute(ctx context.Context, args map[string]any) (string, error)
    // Toolset 返回该工具所属的工具集名称
    Toolset() string
    // IsAvailable 检测该工具在当前环境下是否可用
    // (检查 API key, 二进制文件存在性, 系统能力等)
    IsAvailable() bool
    // Emoji 返回工具的图标
    Emoji() string
    // MaxResultChars 返回结果最大字符数 (0 表示使用默认值)
    MaxResultChars() int
}

// ToolSchema 工具的 JSON Schema 定义
type ToolSchema struct {
    Name        string           `json:"name"`
    Description string           `json:"description,omitempty"`
    Parameters  *json.RawMessage `json:"parameters"` // JSON Schema 对象
}
```

#### 注册中心

```go
// Registry 工具注册中心，并发安全
type Registry struct {
    mu           sync.RWMutex
    tools        map[string]*ToolEntry          // name -> entry
    toolsets     map[string][]string            // toolset -> tool names
    aliases      map[string]string              // alias -> canonical toolset
    toolsetChecks map[string]func() bool        // toolset -> availability check
}

// ToolEntry 注册的工具条目
type ToolEntry struct {
    Tool      Tool   // 工具实例
    IsAsync   bool   // 是否需要异步执行包装
}

// 核心方法
func (r *Registry) Register(tool Tool)                          // 注册工具
func (r *Registry) Deregister(name string)                      // 注销工具
func (r *Registry) Dispatch(ctx context.Context, name string, args map[string]any) (string, error)  // 分发执行
func (r *Registry) GetDefinitions(toolNames []string) []ToolSchema     // 获取工具 Schema 列表
func (r *Registry) GetAvailableToolsets() map[string]ToolsetInfo       // 获取可用工具集
func (r *Registry) RegisterToolsetAlias(alias, canonical string)       // 注册工具集别名
func (r *Registry) DiscoverBuiltin() error                              // 发现内置工具
```

**注册模式** (init() 自注册):

```go
// internal/tool/terminal.go
package tool

import "nexus-agent/internal/tool"

func init() {
    tool.GetRegistry().Register(&TerminalTool{})
}

type TerminalTool struct { /* 实现 Tool 接口 */ }
```

#### 工具集组合

```go
// ToolsetConfig 工具集配置
type ToolsetConfig struct {
    Description string   `yaml:"description"` // 描述
    Tools       []string `yaml:"tools"`       // 直接包含的工具
    Includes    []string `yaml:"includes"`    // 包含的其他工具集
}

// ResolveToolset 递归解析工具集 (检测循环)
func ResolveToolset(name string, configs map[string]*ToolsetConfig, visited map[string]bool) ([]string, error)
```

#### 四大内置工具集

| 工具集 | 包含工具 | 用途 |
|--------|---------|------|
| `core` | terminal, file_read, file_write, web_search, web_extract | 基础工具 |
| `developer` | core + code_execute, git, file_edit | 开发场景 |
| `research` | core + browser, web_crawl, session_search | 研究场景 |
| `full_stack` | developer + research + delegate, memory, cron | 全功能 |

### 3.4 消息网关 (`internal/gateway/`)

**职责**: 多平台消息接入，会话管理，代理缓存，流式投递

#### PlazaAdapter 接口

```go
// PlatformAdapter 是消息平台适配器必须实现的接口
type PlatformAdapter interface {
    // Name 返回平台名称
    Name() string
    // PlatformType 返回平台类型枚举
    PlatformType() Platform
    // Connect 建立连接并开始监听消息
    Connect(ctx context.Context) (<-chan *MessageEvent, error)
    // Disconnect 优雅断开连接
    Disconnect(ctx context.Context) error
    // Send 发送文本消息
    Send(ctx context.Context, chatID string, content string, opts *SendOptions) (*SendResult, error)
    // EditMessage 编辑已发送消息 (流式更新)
    EditMessage(ctx context.Context, chatID string, messageID string, content string) (*SendResult, error)
    // DeleteMessage 删除消息
    DeleteMessage(ctx context.Context, chatID string, messageID string) error
    // SendTyping 发送输入中指示器
    SendTyping(ctx context.Context, chatID string) error
    // SendImage 发送图片
    SendImage(ctx context.Context, chatID string, imageURL string, caption string) (*SendResult, error)
    // SendVoice 发送语音
    SendVoice(ctx context.Context, chatID string, audioPath string) (*SendResult, error)
}

// MessageEvent 来自平台的消息事件
type MessageEvent struct {
    Text           string        // 消息文本
    MessageType    MessageType   // TEXT/PHOTO/VOICE/DOCUMENT/...
    Source         *SessionSource // 会话来源
    MessageID      string        // 平台消息 ID
    MediaURLs      []string      // 媒体文件 URL 列表
    ReplyToMsgID   string        // 被回复的消息 ID
    ReplyToText    string        // 被回复的消息文本
    RawMessage     any           // 平台原始消息对象
    Timestamp      time.Time     // 消息时间戳
}

// SendResult 发送结果
type SendResult struct {
    Success   bool   // 是否成功
    MessageID string // 平台消息 ID
    Error     string // 错误信息
    Retryable bool   // 是否可重试
}
```

#### 代理缓存设计

```go
// AgentCache 代理实例缓存 (LRU + TTL 驱逐)
type AgentCache struct {
    mu       sync.Mutex
    cache    *lru.Cache[string, *cacheEntry]  // LRU 缓存
    maxSize  int                               // 最大缓存数 (默认128)
    idleTTL  time.Duration                     // 空闲超时 (默认1小时)
}

type cacheEntry struct {
    agent       *agent.AIAgent  // 代理实例
    signature   string          // 配置签名 (模型+工具集+系统提示词)
    lastAccess  time.Time       // 最后访问时间
    inUse       bool            // 是否正在处理请求 (防止驱逐)
}

// GetOrCreate 获取或创建代理实例
func (c *AgentCache) GetOrCreate(sessionKey string, factory func() (*agent.AIAgent, string)) (*agent.AIAgent, error)

// SweepIdle 扫描并驱逐空闲超时的代理
func (c *AgentCache) SweepIdle(ctx context.Context)

// EnforceCap 强制驱逐 LRU 条目至容量上限
func (c *AgentCache) EnforceCap()
```

#### GatewayRunner 生命周期

```go
// GatewayRunner 网关运行器
type GatewayRunner struct {
    config      *config.GatewayConfig
    adapters    []PlatformAdapter
    agentCache  *AgentCache
    sessionMgr  *SessionManager
    cronSched   *cron.Scheduler
    state       *state.Store
    deliveryMgr *DeliveryManager
    hookReg     *HookRegistry
    wg          sync.WaitGroup
    running     atomic.Bool
}

func (g *GatewayRunner) Start(ctx context.Context) error {
    // 1. 恢复上次运行的状态 (挂起的会话)
    // 2. 连接所有启用的平台适配器
    // 3. 启动 cron 调度器
    // 4. 启动会话过期监控 goroutine
    // 5. 启动平台重连监控 goroutine
    // 6. 对每个平台: go g.handlePlatform(adapter)
    // 7. 阻塞等待 shutdown 信号
}

func (g *GatewayRunner) handlePlatform(ctx context.Context, adapter PlatformAdapter) {
    msgCh, _ := adapter.Connect(ctx)
    for msg := range msgCh {
        g.wg.Add(1)
        go func(m *MessageEvent) {
            defer g.wg.Done()
            g.processMessage(ctx, adapter, m)
        }(msg)
    }
}

func (g *GatewayRunner) processMessage(ctx context.Context, adapter PlatformAdapter, msg *MessageEvent) {
    // 1. 用户授权检查
    // 2. 识别内置命令 (/new, /reset, /stop, /model, etc.)
    // 3. 构建 SessionSource → sessionKey
    // 4. agent = cache.GetOrCreate(sessionKey, ...)
    // 5. 组装历史消息
    // 6. 设置流式回调 → StreamConsumer
    // 7. agent.RunConversation(ctx, msg.Text, history, "")
    // 8. 格式化并投递最终回复
    // 9. 保存消息到 state.Store
}
```

### 3.5 状态持久化 (`internal/state/`)

**职责**: SQLite 数据库管理，会话/消息 CRUD，FTS5 搜索，自动清理

#### 数据库模式

```sql
-- 模式版本表
CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER NOT NULL
);

-- 会话表
CREATE TABLE IF NOT EXISTS sessions (
    id                 TEXT PRIMARY KEY,
    source             TEXT NOT NULL,          -- 'cli'/'telegram'/'discord'/'cron' 等
    user_id            TEXT,
    model              TEXT,                    -- 模型名称
    model_config       TEXT,                    -- JSON 编码的模型配置
    system_prompt      TEXT,                    -- 完整系统提示词快照
    parent_session_id  TEXT,                    -- 压缩链: FK → sessions(id)
    started_at         REAL NOT NULL,           -- Unix 时间戳
    ended_at           REAL,                    -- NULL = 活跃
    end_reason         TEXT,                    -- 'compression'/'branched'/'cron_complete' 等
    title              TEXT,                    -- 会话标题
    message_count      INTEGER DEFAULT 0,
    tool_call_count    INTEGER DEFAULT 0,
    input_tokens       INTEGER DEFAULT 0,
    output_tokens      INTEGER DEFAULT 0,
    cache_read_tokens  INTEGER DEFAULT 0,
    cache_write_tokens INTEGER DEFAULT 0,
    reasoning_tokens   INTEGER DEFAULT 0,
    estimated_cost_usd REAL,
    api_call_count     INTEGER DEFAULT 0
);

-- 消息表
CREATE TABLE IF NOT EXISTS messages (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id      TEXT NOT NULL REFERENCES sessions(id),
    role            TEXT NOT NULL,              -- 'system'/'user'/'assistant'/'tool'
    content         TEXT,
    tool_call_id    TEXT,                       -- 工具响应关联
    tool_calls      TEXT,                       -- JSON: 工具调用数组
    tool_name       TEXT,                       -- 工具名称 (用于 FTS 索引)
    timestamp       REAL NOT NULL,              -- Unix 时间戳
    token_count     INTEGER,
    finish_reason   TEXT,
    reasoning       TEXT                        -- 思维链/推理文本
);

-- FTS5 虚拟表 (默认 tokenizer, 拉丁语系)
CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
    content, tool_name, tool_calls
);

-- FTS5 虚拟表 (trigram tokenizer, 中日韩语系)
CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts_trigram USING fts5(
    content, tool_name, tool_calls, tokenize='trigram'
);

-- 索引
CREATE INDEX IF NOT EXISTS idx_sessions_source ON sessions(source);
CREATE INDEX IF NOT EXISTS idx_sessions_parent ON sessions(parent_session_id);
CREATE INDEX IF NOT EXISTS idx_sessions_started ON sessions(started_at DESC);
CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, timestamp);
CREATE UNIQUE INDEX IF NOT EXISTS idx_sessions_title_unique ON sessions(title) WHERE title IS NOT NULL;

-- FTS 同步触发器
CREATE TRIGGER IF NOT EXISTS messages_fts_insert AFTER INSERT ON messages BEGIN
    INSERT INTO messages_fts(rowid, content) VALUES (new.id, 
        COALESCE(new.content, '') || ' ' || COALESCE(new.tool_name, '') || ' ' || COALESCE(new.tool_calls, ''));
END;
-- (DELETE 和 UPDATE 触发器同理)
```

#### Store 核心方法

```go
type Store struct {
    db *sql.DB
    mu sync.RWMutex
}

// 会话操作
func (s *Store) CreateSession(ctx context.Context, session *Session) error
func (s *Store) GetSession(ctx context.Context, id string) (*Session, error)
func (s *Store) UpdateSession(ctx context.Context, session *Session) error
func (s *Store) ListSessions(ctx context.Context, filter *SessionFilter) ([]*Session, error)
func (s *Store) EndSession(ctx context.Context, id string, reason string) error

// 消息操作
func (s *Store) InsertMessage(ctx context.Context, msg *MessageRecord) error
func (s *Store) InsertMessagesBatch(ctx context.Context, msgs []*MessageRecord) error
func (s *Store) GetMessages(ctx context.Context, sessionID string, limit, offset int) ([]*MessageRecord, error)

// FTS 搜索
func (s *Store) SearchMessages(ctx context.Context, query string, limit int) ([]*SearchResult, error)
func (s *Store) SearchRecentSessions(ctx context.Context, limit int) ([]*Session, error)

// 维护
func (s *Store) AutoPrune(ctx context.Context, maxAgeDays int) (int, error)
func (s *Store) CheckpointWAL(ctx context.Context) error
func (s *Store) Close() error
```

### 3.6 记忆系统 (`internal/memory/`)

```go
// MemoryProvider 是记忆提供者接口
type MemoryProvider interface {
    // Name 返回提供者名称
    Name() string
    // Initialize 初始化 (连接/创建资源)
    Initialize(ctx context.Context, sessionID string) error
    // SystemPromptBlock 返回注入到系统提示词的静态块
    SystemPromptBlock() string
    // Prefetch 为即将到来的对话回合召回相关记忆
    Prefetch(ctx context.Context, query string) (string, error)
    // QueuePrefetch 在回合结束后异步预取 (为下一回合准备)
    QueuePrefetch(ctx context.Context, query string)
    // SyncTurn 持久化已完成的回合
    SyncTurn(ctx context.Context, userContent, assistantContent string) error
    // GetToolSchemas 返回此提供者暴露的工具 Schema
    GetToolSchemas() []ToolSchema
    // HandleToolCall 处理记忆相关的工具调用
    HandleToolCall(ctx context.Context, toolName string, args map[string]any) (string, error)
    // Shutdown 关闭 (刷盘/断开连接)
    Shutdown(ctx context.Context) error
    // ── 可选钩子 ──
    OnTurnStart(ctx context.Context, turnNum int, message string) error
    OnSessionEnd(ctx context.Context, messages []Message) error
    OnPreCompress(ctx context.Context, messages []Message) error
    OnDelegation(ctx context.Context, task, result string, childSessionID string) error
}
```

### 3.7 技能系统 (`internal/skill/`)

```go
// Skill 技能数据模型
type Skill struct {
    Name        string            // 技能名称 (max 64 chars, lowercase/hyphens)
    Description string            // 技能描述 (max 1024 chars)
    Version     string            // 语义版本
    License     string            // SPDX 标识
    Platforms   []string          // 兼容平台 (空=全平台)
    Body        string            // SKILL.md 正文 (Markdown)
    Category    string            // 组织分类目录
    Path        string            // 磁盘路径
    Fields      map[string]any    // 其他 frontmatter 字段
}
```

### 3.8 上下文工程 (`internal/context/`)

```go
// Builder 系统提示词构建器
type Builder struct {
    identity        string            // SOUL.md 身份定义
    platform        string            // 平台标识
    memoryManager   *memory.Manager   // 记忆管理器
    skillManager    *skill.Manager    // 技能管理器
}

func (b *Builder) Build(ctx context.Context, opts *BuildOptions) (string, error)
// 组装顺序:
//   1. Agent 身份 (SOUL.md)
//   2. 工具使用行为指导
//   3. 用户/网关系统消息
//   4. 持久记忆 (builtin + external)
//   5. 技能索引
//   6. 上下文文件 (AGENTS.md / CLAUDE.md)
//   7. 时间戳, Session ID, 模型/提供者信息
//   8. 环境提示 (WSL, OS 检测)
//   9. 平台特定格式提示

// Compressor 上下文压缩器
type Compressor struct {
    auxProvider    llm.Provider   // 辅助 LLM (用于总结)
    protectFirstN  int            // 头保护消息数 (默认 3)
    tailTokenBudget int           // 尾保护 token 预算 (默认 20000)
}

func (c *Compressor) ShouldCompress(totalTokens int) bool
func (c *Compressor) Compress(ctx context.Context, messages []Message, focusTopic string) ([]Message, error)
// 压缩算法:
//   1. 工具输出修剪 (替换旧工具结果为1行摘要)
//   2. 边界确定 (头保护 N 条 + 尾保护 token 预算)
//   3. LLM 总结中间对话
//   4. 组装: head + summary + tail
//   5. 孤儿 tool_call/tool_result 清理
```

### 3.9 Cron 调度器 (`internal/cron/`)

```go
// Scheduler cron 调度器
type Scheduler struct {
    store      *state.Store
    executor   *JobExecutor
    lockFile   string        // 文件锁路径 (~/.nexus/cron/.tick.lock)
    interval   time.Duration // 检测间隔 (默认 60s)
}

// Job cron 作业
type Job struct {
    ID             string    // 12字符 hex UUID
    Name           string    // 友好名称
    Prompt         string    // 执行的提示词
    Skills         []string  // 预加载的技能
    Model          string    // 模型覆盖
    Provider       string    // 提供者覆盖
    Schedule       Schedule  // 调度配置
    Enabled        bool
    State          JobState  // scheduled/paused/completed/error
    CreatedAt      time.Time
    NextRunAt      time.Time
    LastRunAt      time.Time
    LastStatus     string    // "ok" / "error"
    LastError      string
    DeliverTo      []string  // 投递目标
    Workdir        string    // 工作目录
    EnabledToolsets []string // 工具集限制
}

// ScheduleKind 调度类型
type ScheduleKind int

const (
    ScheduleOnce     ScheduleKind = iota // 一次性 (延迟)
    ScheduleInterval                     // 间隔重复
    ScheduleCron                         // cron 表达式
)
```

### 3.10 沙箱环境 (`internal/sandbox/`)

```go
// Environment 终端执行环境接口
type Environment interface {
    // Execute 执行命令
    Execute(ctx context.Context, command string, opts *ExecuteOptions) (*ExecuteResult, error)
    // ExecuteBackground 后台执行 (返回 ProcessHandle)
    ExecuteBackground(ctx context.Context, command string, opts *ExecuteOptions) (ProcessHandle, error)
    // UpdateCWD 更新当前工作目录
    UpdateCWD(cwd string)
    // CWD 返回当前工作目录
    CWD() string
    // Cleanup 清理资源
    Cleanup() error
}

// ProcessHandle 进程句柄
type ProcessHandle interface {
    Poll() (*int, error)          // 检查进程是否结束，返回退出码
    Kill() error                  // 强制终止
    Wait(ctx context.Context) (int, error) // 等待结束
    Stdout() io.Reader            // 标准输出
    Stderr() io.Reader            // 标准错误
}

// ExecuteResult 执行结果
type ExecuteResult struct {
    Stdout   string // 标准输出
    Stderr   string // 标准错误
    ExitCode int    // 退出码
    Duration time.Duration
}

// ExecuteOptions 执行选项
type ExecuteOptions struct {
    CWD       string            // 工作目录
    Timeout   time.Duration     // 超时时间
    Env       map[string]string // 环境变量
    StdinData string            // 标准输入数据
    Login     bool              // 是否以登录 shell 运行
}
```

---

## 4. 数据流设计

### 4.1 对话循环数据流

```
用户消息
    │
    ▼
┌──────────────────────────────────────────────────────┐
│              AIAgent.RunConversation()               │
│                                                      │
│  ① 构建系统提示词                                     │
│     context.Builder.BuildSystemPrompt()               │
│     ├── SOUL.md 身份                                  │
│     ├── memory.Manager.SystemPromptBlock()            │
│     ├── skill.Manager.GetActiveSkillsIndex()          │
│     ├── 平台格式提示                                   │
│     └── 上下文文件 (AGENTS.md)                         │
│                                                      │
│  ② 拼装消息                                          │
│     messages = [systemPrompt] + history + [userMsg]   │
│     + token 预算预检查                                 │
│                                                      │
│  ③ 主循环 (while iterationBudget > 0)                 │
│     ┌────────────────────────────────────┐           │
│     │ ③a. 记忆预取                        │           │
│     │     memory.Prefetch(query)          │           │
│     ├────────────────────────────────────┤           │
│     │ ③b. LLM API 调用                   │           │
│     │     provider.CreateChatCompletion() │           │
│     │     ├── 构建请求 (Transport)         │           │
│     │     ├── HTTP 发送 (带重试/故障转移)   │           │
│     │     └── 解析响应 (Transport)         │           │
│     ├────────────────────────────────────┤           │
│     │ ③c. 响应分发                        │           │
│     │     ├── "stop": 返回最终回复         │           │
│     │     ├── "length": 继续生成 (max 3x)  │           │
│     │     └── "tool_calls": 执行工具       │           │
│     ├────────────────────────────────────┤           │
│     │ ③d. 工具执行                        │           │
│     │     registry.Dispatch(name, args)   │           │
│     │     ├── 并行度检查                   │           │
│     │     ├── Parallel: goroutine 组      │           │
│     │     ├── Sequential: 逐个执行         │           │
│     │     └── 结果追加到 messages          │           │
│     ├────────────────────────────────────┤           │
│     │ ③e. 上下文压缩检查                   │           │
│     │     compressor.ShouldCompress()     │           │
│     │     └── Compress() 如果超过阈值      │           │
│     └────────────────────────────────────┘           │
│                                                      │
│  ④ 清理与持久化                                       │
│     ├── memory.SyncTurn(user, assistant)              │
│     ├── state.InsertMessage()                         │
│     └── 返回 TurnResult                               │
└──────────────────────────────────────────────────────┘
```

### 4.2 网关消息路由数据流

```
[Telegram/Discord/Slack/微信/飞书/...]
    │  Webhook / WebSocket / Long Poll / HTTP API
    ▼
┌───────────────────────────────────────────┐
│       PlatformAdapter.Connect() → msgCh   │
└───────────────┬───────────────────────────┘
                │ <-chan *MessageEvent
                ▼
┌───────────────────────────────────────────┐
│       GatewayRunner.handlePlatform()      │
│                                           │
│  ① 解析消息来源                             │
│     SessionSource{platform, chatID,       │
│                   userID, threadID, ...}   │
│                                           │
│  ② 会话键生成                               │
│     key = "agent:main:{platform}:         │
│            {chat_type}:{chat_id}"          │
│                                           │
│  ③ 缓存查找/创建                            │
│     agent = cache.GetOrCreate(key)        │
│     ├── 命中: 复用 + moveToFront(LRU)      │
│     └── 未命中: 新建 + cache.Put()         │
│                                           │
│  ④ 异步执行 (goroutine)                    │
│     go func() {                           │
│       agent.RunConversation(ctx, msg)     │
│     }()                                   │
│                                           │
│  ⑤ 流式投递                               │
│     agent.streamCallback =                │
│       streamConsumer.OnDelta              │
│     ├── 缓冲累积 (阈值 40 chars)           │
│     ├── 1s 间隔 editMessage()              │
│     └── 最终 sendMessage()                │
└───────────────────────────────────────────┘
```

### 4.3 工具执行数据流

```
LLM Response (tool_calls)
    │
    ▼
┌─────────────────────────────────────────┐
│  AIAgent.executeToolCalls(toolCalls)    │
│                                         │
│  ① 并行度判断                             │
│     shouldParallelize(toolCallNames)    │
│     ├── 安全工具 (无共享状态): 可并行      │
│     └── 危险工具 (共享状态): 必须顺序      │
│                                         │
│  ② 并行执行路径                           │
│     var wg sync.WaitGroup               │
│     results := make([]string, n)        │
│     for i, tc := range toolCalls {      │
│         wg.Add(1)                       │
│         go func(idx int, tc ToolCall) { │
│             defer wg.Done()             │
│             result, _ = registry.       │
│                 Dispatch(ctx,           │
│                     tc.Name, tc.Args)   │
│             results[idx] = result       │
│         }(i, tc)                        │
│     }                                   │
│     wg.Wait()                           │
│                                         │
│  ③ 顺序执行路径                           │
│     for _, tc := range toolCalls {      │
│         approval.Check(tc) // 如需审批   │
│         result = registry.Dispatch(...)  │
│         messages = append(messages,      │
│             Message{Role: "tool",        │
│                     Content: result})    │
│     }                                   │
│                                         │
│  ④ Registry.Dispatch() 内部              │
│     ├── 查找 tool entry                 │
│     ├── 检查 IsAvailable()               │
│     ├── 执行 tool.Execute(ctx, args)    │
│     └── 返回 JSON string                │
└─────────────────────────────────────────┘
```

---

## 5. 并发模型

### 5.1 整个系统的 goroutine 拓扑

```
main
 ├── GatewayRunner.Start()
 │    ├── cron.Scheduler.Run()              [1 goroutine]
 │    ├── sessionExpiryWatcher()             [1 goroutine]
 │    ├── platformReconnectWatcher()         [1 goroutine]
 │    ├── handlePlatform(telegram)           [1 goroutine]
 │    │    └── for msg := range msgCh
 │    │        go processMessage(msg)        [N goroutines, N=并发消息数]
 │    │            └── agent.RunConversation()
 │    │                └── executeToolCalls()
 │    │                    └── go Dispatch() [M goroutines, M=并行工具数]
 │    ├── handlePlatform(discord)            [1 goroutine]
 │    ├── handlePlatform(slack)              [1 goroutine]
 │    └── ...                                [其他平台]
 │
 └── 主 goroutine: <-shutdownSignal
```

### 5.2 并发安全策略

| 组件 | 并发策略 | 实现 |
|------|---------|------|
| ToolRegistry | `sync.RWMutex` | 注册时写锁，查询时读锁 |
| AgentCache | `sync.Mutex` + LRU | 所有操作串行，驱逐异步 |
| SessionManager | `sync.Mutex` + 文件锁 | 会话键生成和状态更新串行 |
| CredentialPool | `sync.RWMutex` | 凭证选择和轮换需要写锁 |
| AIAgent.messages | `sync.Mutex` | 整个对话循环串行 (一个会话一个goroutine) |
| Store (SQLite) | WAL 模式 + 应用层重试 | SQLite 自己的并发控制 + BEGIN IMMEDIATE 重试 |
| Cron 锁 | 文件锁 (os.File + unix.Flock) | 跨进程互斥 |

---

## 6. 配置系统

### 6.1 配置层级 (优先级从高到低)

```
1. 环境变量 (NEXUS_MODEL, NEXUS_API_KEY, ...)
2. 命令行参数 (--model, --provider, ...)
3. config.yaml (~/.nexus/config.yaml)
4. 默认值 (internal/config/defaults.yaml, embed.FS)
```

### 6.2 核心配置结构体

```go
type Config struct {
    // 代理配置
    Agent AgentConfig `yaml:"agent"`

    // LLM 提供者配置
    Providers map[string]ProviderConfig `yaml:"providers"`

    // 模型配置
    Models map[string]ModelConfig `yaml:"models"`

    // 网关配置
    Gateway GatewayConfig `yaml:"gateway"`

    // 工具配置
    Tools ToolsConfig `yaml:"tools"`

    // 记忆配置
    Memory MemoryConfig `yaml:"memory"`

    // 技能配置
    Skills SkillsConfig `yaml:"skills"`

    // Cron 配置
    Cron CronConfig `yaml:"cron"`

    // 日志配置
    Logging LoggingConfig `yaml:"logging"`

    // 审批配置
    Approval ApprovalConfig `yaml:"approval"`

    // 沙箱配置
    Sandbox SandboxConfig `yaml:"sandbox"`

    // MCP 配置
    MCP MCPConfig `yaml:"mcp"`

    // 凭证配置
    Credentials CredentialConfig `yaml:"credentials"`
}
```

---

## 7. 关键决策与权衡

### 7.1 直接 HTTP vs SDK 依赖

**决策**: 直接使用 `net/http` 实现 LLM API 调用，不依赖官方 SDK。

**理由**:
- 完整控制请求/响应管道 (自定义重试、代理、TLS)
- 零 SDK 传递依赖 (Python 版 openai SDK 有 ~30 个传递依赖)
- API 升级时可自由适配，不等待 SDK 发布
- 统一的内部消息格式，不绑定任何提供者特定结构

**代价**: 需维护 ~200 行/提供者的请求构建和响应解析逻辑

### 7.2 modernc.org/sqlite vs CGo SQLite

**决策**: 使用 `modernc.org/sqlite` (纯 Go SQLite 实现)。

**理由**:
- 无 CGo: 交叉编译只需 `GOOS=linux go build`
- 单一二进制: 无动态链接库
- FTS5 完整支持
- 对 OLTP 类工作负载性能可接受

**代价**: 纯 Go 版本性能约为 C 版本的 80%，但对 I/O 密集的代理操作可忽略

### 7.3 init() 注册 vs 显式注册

**决策**: 工具通过 `init()` 函数自注册到全局注册中心。

**理由**:
- 新增工具只需创建实现 `Tool` 接口的文件
- 编译时自动发现 (Go 编译器调用所有 `init()`)
- 与 Python 项目模式一致 (每个 `tools/*.py` 文件自注册)

**代价**: 所有工具都编译到二进制中 (可通过构建标签控制)

### 7.4 Channel vs Callback 用于流式输出

**决策**: AIAgent 通过 Go channel (`<-chan *StreamDelta`) 发送文本增量。

**理由**:
- Go 惯用的生产者-消费者模式
- StreamConsumer 可以 `range` 遍历通道
- 天然支持 `select` 多路复用 (超时、取消)
- 易于测试 (mock channel)

**代价**: 需要带缓冲的 channel 防止写入阻塞

### 7.5 技能系统设计转变

**决策**: 技能从 Python 代码改为**声明式 YAML + 参数化 Shell 脚本**。

**理由**:
- 跨语言兼容 (Python 技能在 Go 版本无法直接运行)
- 声明式格式更安全 (无需执行不受信代码)
- 更容易审计和分享

**格式**:
```markdown
---
name: my-skill
description: 我的自定义技能
version: 1.0.0
env_vars:
  - name: MY_API_KEY
    prompt: 请输入 API Key
scripts:
  - name: fetch-data
    command: curl -H "Authorization: Bearer ${MY_API_KEY}" https://api.example.com/data
---

# 技能说明
...Markdown 正文...
```

---

## 8. 实现计划

### Phase 1: 基础设施 (Week 1-2)

```
□ go.mod, 项目结构搭建
□ internal/config/ — 配置加载 (Viper + embed 默认配置)
□ pkg/logutil/ — slog 日志系统
□ internal/state/ — SQLite + FTS5, schema, 迁移框架
□ internal/llm/types.go — 核心类型定义
□ 所有核心接口定义 (Provider, Tool, PlatformAdapter, Environment, MemoryProvider)
```

### Phase 2: LLM 层 (Week 2-3)

```
□ internal/llm/client.go — HTTP 客户端
□ internal/llm/stream.go — SSE 解析器
□ internal/llm/transport.go — 传输层注册表
□ internal/llm/openai.go — OpenAI 兼容传输
□ internal/llm/anthropic.go — Anthropic Messages 传输
□ internal/llm/anthropic_thinking.go — 扩展思维配置
□ internal/llm/gemini.go — Gemini 传输
□ internal/llm/bedrock.go — Bedrock 传输
□ internal/llm/error.go — 错误分类器
□ internal/credential/ — 凭证管理
```

### Phase 3: 工具系统 (Week 3-4)

```
□ internal/tool/registry.go — 工具注册中心
□ internal/tool/toolsets.go — 工具集组合
□ internal/sandbox/ — 沙箱环境 (local/docker/ssh)
□ internal/approval/ — 命令安全审批
□ 核心工具实现: terminal, file, web, browser, code
□ 高级工具实现: delegate, memory, skill, mcp
□ 工具测试
```

### Phase 4: 代理核心 (Week 4-5)

```
□ internal/context/ — 系统提示词构建 + 上下文压缩
□ internal/memory/ — 记忆系统 (builtin + manager)
□ internal/skill/ — 技能系统 (loader + manager + hub)
□ internal/agent/ — AIAgent + 对话循环 + 重试/故障转移
□ 代理集成测试
```

### Phase 5: 网关 (Week 5-6)

```
□ internal/gateway/platforms/adapter.go — 平台接口
□ internal/gateway/cache.go — Agent 缓存
□ internal/gateway/stream.go — 流式消费者
□ internal/gateway/runner.go — 网关运行器
□ 平台适配器: telegram, discord, slack (第一批)
□ 平台适配器: whatsapp, wechat, feishu, dingtalk (第二批)
□ internal/cron/ — Cron 调度器
```

### Phase 6: 打磨 (Week 6-7)

```
□ cmd/nexus/main.go — CLI 入口
□ cmd/nexus-gateway/main.go — 网关入口
□ cmd/nexus-acp/main.go — ACP 入口
□ internal/mcp/ — MCP 协议
□ Dockerfile — 多阶段构建
□ 集成测试 + 文档
```

---

## 9. Go 依赖选型

### 9.1 核心依赖

| 依赖 | 用途 | 选择理由 |
|------|------|---------|
| `modernc.org/sqlite` | SQLite (纯 Go) | 无 CGo，支持 FTS5，交叉编译友好 |
| `github.com/spf13/viper` | YAML 配置 | 支持嵌套、环境变量覆盖、多文件合并 |
| `gopkg.in/yaml.v3` | YAML 解析 | Go 标准 YAML 库 |
| `github.com/go-rod/rod` | 浏览器自动化 | 纯 Go，比 playwright-go 更活跃 |

### 9.2 可选依赖

| 依赖 | 用途 | 备注 |
|------|------|------|
| `github.com/gorilla/websocket` | WebSocket | 网关 WebSocket 平台适配器 |
| `github.com/aws/aws-sdk-go-v2` | AWS SigV4 | 仅 Bedrock 传输需要 |
| `github.com/robfig/cron/v3` | Cron 解析 | 可选，也可自实现 |
| `github.com/stretchr/testify` | 测试 | testify + httptest |

### 9.3 明确不使用的依赖

| 避免 | 原因 |
|------|------|
| `github.com/sashabaranov/go-openai` | 直接 HTTP 实现，不需要 SDK |
| `github.com/anthropics/anthropic-sdk-go` | 直接 HTTP 实现 |
| `mattn/go-sqlite3` | 需要 CGo，破坏交叉编译 |
| `github.com/playwright-community/playwright-go` | rod 更轻量，纯 Go |

---

## 10. 可观测性

### 10.1 结构化日志

```go
// 使用 slog 标准库
slog.Info("conversation turn",
    "session_id", sessionID,
    "model", model,
    "provider", provider,
    "platform", platform,
    "history_len", len(messages),
    "iteration", iterationNum,
)

slog.Error("api call failed",
    "session_id", sessionID,
    "status_code", classified.StatusCode,
    "failover_reason", classified.Reason.String(),
    "retryable", classified.Retryable,
    "err", err,
)
```

### 10.2 指标

```go
type Metrics struct {
    APICallCount        atomic.Int64   // API 调用总数
    APICallDuration     atomic.Int64   // API 调用总耗时 (ns)
    ToolCallCount       atomic.Int64   // 工具调用总数
    ToolErrorCount      atomic.Int64   // 工具错误总数
    CompressionCount    atomic.Int64   // 上下文压缩次数
    ActiveSessions      atomic.Int64   // 活跃会话数
    CacheHitCount       atomic.Int64   // Agent 缓存命中数
    CacheMissCount      atomic.Int64   // Agent 缓存未命中数
    CredentialRotations atomic.Int64   // 凭证轮换次数
}
```

### 10.3 健康检查

```go
// /health 端点返回
type HealthStatus struct {
    Status    string            `json:"status"`    // "ok" / "degraded" / "down"
    Version   string            `json:"version"`
    Uptime    string            `json:"uptime"`
    Providers map[string]string `json:"providers"` // provider -> status
    Sessions  int               `json:"active_sessions"`
}
```

---

## 11. 与 Python 版的核心差异

| 方面 | Python 版 | Go 版 |
|------|----------|-------|
| LLM 调用 | 依赖 openai / anthropic SDK | 直接 net/http |
| 并发模型 | asyncio + ThreadPoolExecutor | goroutine + channel |
| 工具注册 | `importlib` 动态导入 | `init()` 编译时自注册 |
| 技能 | Python 代码 | 声明式 YAML + Shell 脚本 |
| 数据库 | sqlite3 (C 扩展) | modernc/sqlite (纯 Go) |
| 浏览器 | Playwright (Python) | rod (纯 Go) |
| 部署 | uv + 虚拟环境 | 单一静态二进制 |
| 内存占用 | ~500MB (128 会话) | 目标 ~50-100MB (128 会话) |
| 启动时间 | 2~5 秒 | 目标 <100ms |
| 类型安全 | 运行时 (mypy 可选) | 编译时 |

---

## 12. 新增包描述

### 12.1 `internal/hooks/` — Hook 拦截链

**职责**: 提供可复用的 Shell Hook 系统，允许用户通过 shell 脚本拦截和控制工具调用行为。

#### 核心接口

```go
// Hook 是单个 hook 的抽象接口。
// 实现者必须提供名称、事件类型、匹配逻辑和执行逻辑。
type Hook interface {
    Name() string                                                  // hook 唯一名称
    Event() string                                                 // 监听事件类型
    Match(toolName string) bool                                    // 是否匹配工具名
    Execute(ctx context.Context, event *HookEvent) (*HookResponse, error) // 执行 hook
}

// Manager 是 hook 管理器的抽象接口。
// 负责 hook 注册、匹配和链式执行。
type Manager interface {
    Register(hook Hook) error                                                          // 注册 hook
    ExecutePreHooks(ctx context.Context, toolName string, input map[string]any) (*HookResponse, bool, error)  // pre hook 链
    ExecutePostHooks(ctx context.Context, toolName string, input map[string]any, output string) error          // post hook 链
}
```

#### 事件与响应

```go
const (
    EventPreToolCall  = "pre_tool_call"  // 工具调用前触发
    EventPostToolCall = "post_tool_call" // 工具调用后触发
)

// HookEvent 发送给 hook 脚本的事件 (JSON stdin)
type HookEvent struct {
    EventName  string         `json:"event_name"`
    ToolName   string         `json:"tool_name"`
    ToolInput  map[string]any `json:"tool_input"`
    ToolOutput string         `json:"tool_output,omitempty"` // 仅 post 时填充
    SessionID  string         `json:"session_id"`
    CWD        string         `json:"cwd"`
}

// HookResponse hook 脚本的响应 (JSON stdout)
type HookResponse struct {
    Decision string `json:"decision"` // allow / block / modify
    Reason   string `json:"reason"`   // 阻止原因
    Message  string `json:"message"`  // 替换消息 (modify 时)
}
```

#### Hook 链语义

- **pre_tool_call**: 按注册顺序执行，首个返回 `block` 的 hook 终止链并阻止工具调用
- **post_tool_call**: 所有匹配的 hook 都会执行，不会被中断
- hook 执行失败时记录警告并跳过，不终止链
- Shell 命令通过 JSON stdin/stdout wire protocol 通信

#### Shell 执行与持久化 Allowlist

```go
// ShellHook 实现 Hook 接口的 shell 脚本实现
type ShellHook struct {
    name       string         // hook 名称 (用于日志和 allowlist)
    event      string         // 监听的事件类型
    command    string         // shell 脚本路径
    matcher    *regexp.Regexp // 工具名匹配正则 (nil = 匹配所有)
    timeoutSec int            // 超时秒数 (默认 60, 最大 300)
}

// Allowlist 管理 hook 命令的允许列表
type Allowlist struct {
    entries   map[string]bool // 允许的命令集合
    dir       string          // 持久化目录 (~/.nexus/hooks/)
    acceptAll bool            // 自动接受所有 hook
}
```

#### 配置规格

```go
// HookSpec 定义一个 Shell Hook 的配置规格
type HookSpec struct {
    Event      string `yaml:"event"`   // 事件类型: pre_tool_call / post_tool_call
    Command    string `yaml:"command"` // hook 脚本路径
    Matcher    string `yaml:"matcher"` // 工具名匹配正则 (空 = 匹配所有)
    TimeoutSec int    `yaml:"timeout"` // 超时秒数 (默认 60, 最大 300)
}
```

---

### 12.2 `internal/permissions/` — 五级权限管理

**职责**: 统一管理所有工具调用的权限控制，替代分散在各工具中的审批逻辑。

#### 权限级别

```go
type Level int

const (
    LevelAutoAllow Level = 0 // 自动放行: 只读操作、安全查询
    LevelAutoDeny  Level = 1 // 自动拒绝: 硬封锁，始终拒绝
    LevelAskOnce   Level = 2 // 询问一次: 首次确认，会话内记住
    LevelAskAlways Level = 3 // 每次询问: 高风险写操作
    LevelEscalate  Level = 4 // 升级到人工: 网关模式下转发审核
)
```

#### 策略引擎

```go
// Rule 表示一条权限规则
type Rule struct {
    ToolPattern string   `yaml:"tool"`                  // 工具名 glob 模式 (* 和 ?)
    ArgPatterns []string `yaml:"args,omitempty"`         // 参数匹配 (AND 逻辑)
    Level       Level    `yaml:"level"`                  // 权限级别
    Reason      string   `yaml:"reason,omitempty"`       // 规则说明
}

// 参数匹配格式:
//   "key=value"    — 精确匹配
//   "key~=regex"   — 正则匹配
//   "key!=value"   — 不等于
//   "key"          — 参数存在性检查

// Policy 权限策略引擎，规则顺序匹配，首个命中生效
type Policy struct {
    Name    string `yaml:"name"`
    Rules   []Rule `yaml:"rules"`    // 按优先级排序
    Default Level  `yaml:"default"`  // 无规则命中时的默认级别
}
```

#### Checker — 核心检查器

```go
// Checker 整合策略引擎、会话记忆和 approval.Checker
type Checker struct {
    policy           *Policy            // 当前策略
    approval         *approval.Checker  // 原有审批检查器 (终端命令)
    sessionDecisions map[string]Level   // 会话级决策缓存
}

// Check 权限系统的主要入口点
func (c *Checker) Check(toolName string, args map[string]any) Decision {
    // 1. 策略引擎评估
    // 2. 会话记忆检查 (ask_once 级别)
    // 3. 终端命令: 与原有审批引擎联动 (取更严格的决策)
}
```

#### 配置加载

```yaml
# ~/.nexus/permissions.yaml (用户级)
# .nexus/permissions.yaml (项目级, 优先级更高)

version: 1
default: ask_always
rules:
  - tool: "web_search"
    level: auto_allow
    reason: "网页搜索为只读操作"
  - tool: "file_*"
    args: ["path~=*.env*"]
    level: auto_deny
    reason: "禁止修改环境变量文件"
  - tool: "terminal"
    args: ["command~=rm -rf"]
    level: auto_deny
    reason: "禁止执行破坏性命令"

# 支持预设 (profiles)
profiles:
  safe:
    default: ask_always
    rules: []
  dev:
    default: ask_once
    rules:
      - tool: "terminal"
        level: ask_once
```

合并策略: 项目级规则插入到用户级规则之前 (更高优先级)。

---

### 12.3 `internal/worker/` — Worker 状态机

**职责**: 管理长时间运行操作的生命周期，用于 delegate 任务和异步操作。

#### 状态定义

```go
type State string

const (
    StatePending   State = "pending"   // 等待执行
    StateRunning   State = "running"   // 正在执行
    StatePaused    State = "paused"    // 已暂停，可恢复
    StateCompleted State = "completed" // 成功完成 (终态)
    StateFailed    State = "failed"    // 执行失败 (终态)
    StateCancelled State = "cancelled" // 已取消 (终态)
)

// 状态转换规则 (合法目标状态)
var validTransitions = map[State][]State{
    StatePending:   {StateRunning, StateCancelled},
    StateRunning:   {StatePaused, StateCompleted, StateFailed, StateCancelled},
    StatePaused:    {StateRunning, StateCancelled},
    StateCompleted: {}, // 终态，不可转换
    StateFailed:    {}, // 终态，不可转换
    StateCancelled: {}, // 终态，不可转换
}
```

#### Manager

```go
// Manager 管理多个 Worker 实例的生命周期
type Manager struct {
    workers map[string]*Worker // 并发安全 (sync.RWMutex)
}

func (m *Manager) Submit(ctx context.Context, taskName string, fn func(ctx context.Context) (any, error)) *Worker
// 提交任务，fn 在独立 goroutine 中执行:
//   - 正常返回 → Completed
//   - 返回错误 → Failed
//   - ctx 取消 → Cancelled

func (m *Manager) Cancel(id string) error       // 取消指定任务
func (m *Manager) GetStatus(id string) *Worker   // 查询任务状态
func (m *Manager) ListActive() []*Worker         // 列出所有活跃任务
func (m *Manager) ListAll() []*Worker            // 列出所有任务
func (m *Manager) Remove(id string) error        // 移除终态任务
```

#### Worker 核心

```go
type Worker struct {
    ID        string
    TaskName  string
    state     State
    startedAt time.Time
    updatedAt time.Time
    err       error         // Failed 状态下的错误
    result    any           // Completed 状态下的结果
    cancel    context.CancelFunc
    done      chan struct{} // 终态时关闭
}

func (w *Worker) Transition(to State) error    // 状态转换 (含合法性校验)
func (w *Worker) SetResult(result any) error   // 设置结果并转为 Completed
func (w *Worker) SetError(err error) error     // 设置错误并转为 Failed
func (w *Worker) Cancel()                      // 请求取消
func (w *Worker) Wait()                        // 阻塞直到终态
func (w *Worker) Done() <-chan struct{}         // 终态通知 channel
```

---

### 12.4 `internal/i18n/` — 国际化

**职责**: 提供 Nexus Agent 的国际化支持，YAML 格式 locale 文件加载，点分键查找。

#### 核心接口

```go
// Translator 翻译器接口
type Translator interface {
    T(key string, args ...any) string // 翻译并格式化
    Locale() string                   // 当前 locale
    SetLocale(locale string) error    // 切换 locale
}

// 全局便捷函数 (需先调用 Init)
func Init(locale string)          // 初始化全局翻译器
func T(key string, args ...any) string // 全局翻译
```

#### 回退链

翻译查找顺序: **精确 locale → 语言前缀 → en → raw key**

```
zh-CN → zh → en → 返回原始 key
en-US → en → en → 返回原始 key
ja    → ja → en → 返回原始 key (如果 ja locale 未加载)
```

#### Locale 文件

```go
//go:embed locales/*.yaml
var localeFS embed.FS // 嵌入所有 locale YAML 文件
```

当前内置 locale: `en`、`zh`。通过 `embed.FS` 打包到二进制中。

#### YAML 格式

```yaml
# locales/zh.yaml
app.name: "Nexus Agent"
app.version: "版本: %s"
error.not_found: "未找到: %s"
permission.auto_allow: "自动放行"
permission.ask_once: "询问一次"
```

---

### 12.5 `internal/auth/` — 认证

**职责**: 提供第三方 OAuth 认证流程，当前支持 Google OAuth 2.0 PKCE。

#### GoogleOAuth PKCE 流程

```go
type GoogleOAuth struct {
    clientID    string   // Google OAuth 客户端 ID
    redirectURI string   // 重定向 URI (动态分配端口)
    scopes      []string // OAuth 作用域
    tokenFile   string   // token 持久化路径 (~/.nexus/credentials/google.json)
}

// 流程:
//  1. 生成 PKCE code_verifier (64 字符) + code_challenge (S256)
//  2. 启动本地 HTTP server 监听回调 (随机端口)
//  3. 用系统浏览器打开 Google 授权页面
//  4. 等待用户授权后回调携带 authorization code
//  5. 用 code + code_verifier 换取 access_token
//  6. 持久化 token 到文件 (0600 权限)
func (g *GoogleOAuth) StartFlow(ctx context.Context) (*Token, error)
```

#### Token 管理

```go
type Token struct {
    AccessToken  string    `json:"access_token"`
    RefreshToken string    `json:"refresh_token"`
    Expiry       time.Time `json:"expiry"`
    TokenType    string    `json:"token_type"`
}

func (t *Token) Valid() bool                                    // 是否有效 (提前 60s 刷新)
func (g *GoogleOAuth) RefreshToken(ctx context.Context, token *Token) (*Token, error) // 自动刷新
func (g *GoogleOAuth) LoadToken() (*Token, error)               // 从文件加载
func (g *GoogleOAuth) SaveToken(token *Token) error             // 持久化到文件
```

---

### 12.6 `internal/llm/` — 新增提供者

#### Copilot ACP (`copilot_acp.go`)

GitHub Copilot 集成，复用 OpenAI Chat Completions 传输层。

```go
// CopilotProvider 实现 GitHub Copilot ACP 提供者
type CopilotProvider struct {
    transport  *OpenAITransport // 复用 OpenAI 传输层
    httpClient *http.Client
    endpoint   string // 默认 https://api.githubcopilot.com
    token      string // Copilot 访问令牌
    model      string // 默认 gpt-4o
}

// 认证头: Authorization: Bearer <token>
// 额外头: Editor-Version, Copilot-Integration-Id
// 默认模型: gpt-4o, gpt-4o-mini, o1-mini, claude-3.5-sonnet, claude-3.5-haiku
```

#### LM Studio (`lmstudio.go`)

本地推理传输层，支持 `reasoning_content` 字段用于推理模型的思维链输出。

```go
// LMStudioTransport 实现 LM Studio OpenAI 兼容传输层
type LMStudioTransport struct {
    httpClient *http.Client
    baseURL    string // 默认 http://localhost:1234/v1
}

// 特殊处理:
//   - 检测 reasoning_content 字段 (LM Studio 推理模型特有)
//   - 本地服务通常不需要 API Key
//   - 支持 GET /v1/models 获取已加载模型列表
```

#### Models.dev (`models_dev.go`)

模型元数据库客户端，提供模型元数据查询能力。

```go
// ModelsDevClient 模型元数据客户端
type ModelsDevClient struct {
    cache     map[string]*ModelDevInfo // 模型 ID → 元数据
    cacheTTL  time.Duration            // 缓存 TTL (默认 1 小时)
}

// ModelDevInfo 模型元数据
type ModelDevInfo struct {
    ID            string  // 模型唯一标识
    Provider      string  // 提供者名称
    ContextWindow int     // 上下文窗口大小
    MaxOutput     int     // 最大输出 token 数
    Vision        bool    // 是否支持视觉
    Reasoning     bool    // 是否支持推理
    InputPrice    float64 // 输入价格 (每百万 token)
    OutputPrice   float64 // 输出价格 (每百万 token)
}

// API 不可用时自动回退到内置硬编码列表 (27+ 模型)
// 覆盖: Anthropic (6), OpenAI (8), Google (5), DeepSeek (2), Mistral (3)
```

---

### 12.7 `internal/agent/` — 新增模块

#### ThinkScrubber (`think_scrubber.go`)

流式思考标签清理，从输出中分离思考内容。

```go
// ThinkScrubber 从流式输出中分离思考内容
type ThinkScrubber struct {
    provider     string       // 提供者名称
    openTag      string       // 开始标签
    closeTag     string       // 结束标签
    state        scrubState   // 状态机状态
    thinkContent strings.Builder // 捕获的思考内容
    onThink      func(string)    // 思考内容增量回调
}

// 支持的标签格式:
//   anthropic: <think> ... </think>
//   deepseek:  <|thinking|> ... <|/thinking|>
//   generic:   <scratchpad> ... </scratchpad>

// 状态机: stateIdle → stateInTag → stateInContent → stateOutTag → stateIdle
func (s *ThinkScrubber) Scrub(delta string) string  // 处理增量，返回用户可见文本
func (s *ThinkScrubber) ThinkContent() string        // 获取完整思考内容
```

#### ImageRouter (`image_routing.go`)

智能视觉模型路由，根据消息内容自动选择支持视觉的模型。

```go
// ImageRouter 根据消息内容选择合适的模型
type ImageRouter struct {
    visionModels  map[string]bool // 支持 vision 的模型集合
    fallbackModel string          // 回退模型 (默认 claude-sonnet-4-20250514)
}

// 规则:
//   - 无图像 → 返回 currentModel
//   - 有图像 + 当前模型支持 vision → 返回 currentModel
//   - 有图像 + 当前模型不支持 vision → 返回 fallbackModel
func (r *ImageRouter) RouteModel(messages []llm.Message, currentModel string) string

// 图像检测支持:
//   - base64 数据 URI (data:image/...;base64,...)
//   - 图像 URL (http(s)://... 以图片扩展名结尾)
//   - OpenAI 多模态内容块 (type:"image_url")
//   - Anthropic 图像块 (type:"image")
```

#### RecoveryEngine (`recovery.go`)

自动恢复配方引擎，将错误分类结果映射到具体恢复动作。

```go
// RecoveryEngine 错误恢复引擎
type RecoveryEngine struct {
    recipes []RecoveryRecipe // 按优先级排序的配方
}

// 内置 10 条默认配方:
//  1. 上下文溢出 → compress_and_retry
//  2. 请求体过大 → truncate_and_retry
//  3. 速率限制 → wait_and_retry (解析 Retry-After)
//  4. 认证失败 → rotate_credential
//  5. 计费耗尽 → rotate_credential
//  6. 模型不存在 → fallback_model
//  7. 服务过载 → wait_and_retry (10s)
//  8. 服务器错误 → wait_and_retry (5s)
//  9. 超时 → wait_and_retry (3s)
// 10. 格式错误 → abort

func (e *RecoveryEngine) ClassifyAndRecover(err error) RecoveryAction
func (e *RecoveryEngine) AddRecipe(recipe RecoveryRecipe) // 添加自定义配方
```

#### FileSafetyChecker (`file_safety.go`)

敏感文件写保护，在工具执行层面提供第二道防线。

```go
// FileSafetyChecker 文件写入安全检查器
type FileSafetyChecker struct {
    protectedPaths      []string // 受保护路径 glob 模式
    protectedExtensions []string // 受保护文件扩展名
    maxWriteSize        int64    // 单次写入最大字节数 (默认 10MB)
}

// 默认保护规则 (15 条):
//   路径: .env, .env.*, .ssh/*, .gnupg/*, .aws/credentials, .kube/config,
//         node_modules/**, .git/objects/**
//   扩展名: .pem, .key, .p12, .pfx, .cert, .crt, .keystore

func (fs *FileSafetyChecker) CheckWrite(path string, contentSize int64) (allowed bool, reason string)
```

#### ToolCallGuardrails (`guardrails.go`)

工具调用循环守卫，防止 LLM 陷入无限循环。

```go
// ToolCallGuardrails 工具调用安全护栏
type ToolCallGuardrails struct {
    history                  []ToolCallRecord // 滑动窗口历史
    consecutiveDuplicates    int              // 连续重复计数
    maxConsecutiveDuplicates int              // 精确重复阈值 (默认 3)
    maxToolCallsInWindow     int              // 窗口内同工具最大次数 (默认 10)
    windowSize               int              // 滑动窗口大小 (默认 20)
}

// 检测两种异常模式:
//   1. 精确重复: 相同 toolName + 相同 args 连续出现 N 次
//   2. 工具固着: 同一 toolName 在滑动窗口内出现 M 次

func (g *ToolCallGuardrails) Check(toolName string, args map[string]any) (allowed bool, reason string)
func (g *ToolCallGuardrails) Record(toolName string, args map[string]any) // 记录调用
func (g *ToolCallGuardrails) Reset()                                      // 每轮新消息时重置
```

---

## 13. 安全架构

### 13.1 多层安全防护

```
用户输入 → LLM → 工具调用
                    ↓
            ┌─ Guardrails (循环守卫)
            │   滑动窗口 + 连续重复检测
            ├─ Permissions (五级权限)
            │   规则顺序匹配 + 会话记忆
            ├─ Approval (命令审批)
            │   危险模式匹配 + 人工确认
            ├─ File Safety (文件保护)
            │   15 条 glob 规则 + 大小限制
            ├─ Path Security (路径遍历防护)
            │   防止 ../.. 路径逃逸
            ├─ URL Safety (SSRF 防护)
            │   内网地址检测
            ├─ Injection Detection (注入检测)
            │   命令注入模式识别
            └─ Hooks (Pre/Post 拦截)
                Shell 脚本自定义拦截
                    ↓
              工具执行 → 结果返回 LLM
```

### 13.2 权限决策流

```
工具调用请求
    │
    ▼
┌─────────────────────────────────┐
│  Guardrails.Check()             │
│  ├── 精确重复检测 (3次阈值)      │
│  └── 工具固着检测 (10次/20窗口)  │
│  → 拒绝则终止                   │
└────────────┬────────────────────┘
             │ 允许
             ▼
┌─────────────────────────────────┐
│  Hooks.ExecutePreHooks()        │
│  ├── 按注册顺序执行              │
│  └── 首个 block 终止链           │
│  → block 则终止                 │
└────────────┬────────────────────┘
             │ allow/modify
             ▼
┌─────────────────────────────────┐
│  Permissions.Check()            │
│  ├── 策略引擎评估 (glob+参数)    │
│  ├── 会话记忆 (ask_once)        │
│  └── 审批引擎联动 (terminal)     │
│  → auto_deny 则终止             │
│  → ask_once/ask_always 需确认   │
│  → auto_allow 则放行            │
└────────────┬────────────────────┘
             │ 需要确认
             ▼
┌─────────────────────────────────┐
│  用户确认 / 人工审批             │
│  → 记住决策 (ask_once)          │
│  → 拒绝则终止                   │
└────────────┬────────────────────┘
             │ 确认放行
             ▼
┌─────────────────────────────────┐
│  FileSafetyChecker.CheckWrite() │  (仅文件写入工具)
│  ├── 扩展名检查                  │
│  ├── 路径 glob 匹配             │
│  └── 大小限制 (10MB)            │
└────────────┬────────────────────┘
             │ 允许
             ▼
        工具执行
```

---

## 14. Provider 回退链

### 14.1 多层故障转移

```
主提供者重试 (3次)
    │ 成功 → 返回结果
    ↓ 失败
ProviderRouter (优先级/健康检查)
    │ 按优先级遍历所有提供者
    │ 跳过不健康的提供者
    │ 成功 → 返回结果
    ↓ 全部失败
FallbackChain (按优先级遍历)
    │ 配置文件中定义的回退链
    │ 成功 → 返回结果
    ↓ 失败
旧版 fallbackProvider (单一备选)
    │ 成功 → 返回结果
    ↓ 失败
返回最终错误
```

### 14.2 ProviderRouter 详细设计

```go
// ProviderRouter 基于优先级的多提供者路由
type ProviderRouter struct {
    entries        []*ProviderEntry // 按优先级排序
    healthInterval time.Duration    // 健康检查间隔 (默认 5 分钟)
    healthTimeout  time.Duration    // 健康检查超时 (默认 30 秒)
}

// ProviderEntry 带优先级的 LLM 提供者
type ProviderEntry struct {
    Provider llm.Provider // 提供者实例
    Model    string       // 模型名称
    Priority int          // 优先级 (数字越小越优先)
    Healthy  bool         // 健康状态
    LastErr  time.Time    // 最后错误时间
}

// 健康检查: 周期性对不健康提供者执行 ListModels 探测
// 错误分类: 上下文溢出/格式错误不触发降级 (需压缩/修正而非切换)
```

### 14.3 恢复配方引擎

```go
// RecoveryEngine 将错误分类映射到恢复动作
type RecoveryAction struct {
    Strategy string        // 恢复策略
    WaitTime time.Duration // 等待时间
    Message  string        // 恢复说明
}

// 六种恢复策略:
//   compress_and_retry   — 压缩上下文后重试
//   wait_and_retry       — 等待指定时间后重试
//   rotate_credential    — 轮换凭证后重试
//   fallback_model       — 切换到备选模型
//   truncate_and_retry   — 截断输入后重试
//   abort                — 终止操作
```

---

## 15. 配置参考

### 15.1 新增配置项

```yaml
agent:
  proxy: "${HTTPS_PROXY}"           # HTTP 代理
  fallback_chain:                    # Provider 回退链
    - provider: "openai"
      model: "gpt-4o"
      priority: 1
    - provider: "deepseek"
      model: "deepseek-chat"
      priority: 2
    - provider: "lmstudio"
      model: "local-model"
      priority: 3

# 权限配置 (permissions.yaml)
permissions:
  version: 1
  default: ask_always
  rules:
    - tool: "web_search"
      level: auto_allow
    - tool: "terminal"
      args: ["command~=rm -rf"]
      level: auto_deny
  profiles:
    safe:
      default: ask_always
    dev:
      default: ask_once

# Hook 配置 (~/.nexus/hooks/*.yaml)
hooks:
  - event: pre_tool_call
    command: "/path/to/hook-script.sh"
    matcher: "terminal"
    timeout: 60

# 国际化
i18n:
  locale: "zh"                       # 当前语言 (en, zh)
```

### 15.2 新增 LLM 提供者配置

```yaml
providers:
  copilot:
    type: "copilot"
    token: "${COPILOT_TOKEN}"
    endpoint: "https://api.githubcopilot.com"
    model: "gpt-4o"

  lmstudio:
    type: "lmstudio"
    base_url: "http://localhost:1234/v1"
    model: "local-model"
    # LM Studio 本地服务通常不需要 API Key
```

---

> 本文档为 Nexus Agent Go 版架构设计，随实现推进持续更新。
