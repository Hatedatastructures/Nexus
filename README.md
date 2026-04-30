# Nexus Agent

Nexus Agent 是一个自进化 AI 代理运行时 (Go 版)，支持多 LLM 后端、丰富的工具生态、多平台消息网关和 MCP 协议集成。

## ✨ 核心特性

- **多 LLM 支持** — OpenAI、Anthropic、Gemini、AWS Bedrock 及 OpenAI 兼容后端
- **28+ 内置工具** — 文件操作、终端执行、浏览器自动化、代码执行、网页搜索、记忆管理、技能系统等
- **多平台网关** — Telegram、Discord、Slack、WhatsApp、微信、飞书、钉钉
- **MCP 协议** — 支持调用外部 MCP 服务器上的工具
- **子代理委派** — 复杂任务拆分并行处理
- **上下文压缩** — 自动修剪、智能摘要、anti-thrash 保护
- **Prompt 注入防护** — 系统提示词安全扫描
- **凭证池** — 多 API Key 轮换、故障自动降级

## 🚀 快速开始

### 1. 编译

```bash
go build -o nexus.exe ./cmd/nexus
go build -o nexus-gateway.exe ./cmd/nexus-gateway
go build -o nexus-acp.exe ./cmd/nexus-acp
```

### 2. 配置

在 `~/.nexus/config.yaml` 或当前目录创建 `config.yaml`：

```yaml
agent:
  model: qwen3.6-plus
  provider: qwen
  max_tokens: 128000
  max_iterations: 30

providers:
  qwen:
    base_url: https://dashscope.aliyuncs.com/compatible-mode
    api_key: sk-your-api-key
    api_mode: chat_completions

logging:
  level: warn
  format: text
```

### 3. 运行

```bash
./nexus.exe chat        # 交互式对话
./nexus.exe skill       # 查看工具列表
./nexus.exe memory      # 查看记忆
./nexus.exe config show # 查看配置
```

## 📁 项目结构

```
Nexus/
├── cmd/
│   ├── nexus/          # CLI 入口
│   ├── nexus-gateway/  # 消息网关入口
│   └── nexus-acp/      # MCP 服务器入口
├── internal/
│   ├── agent/          # 代理核心 (对话循环、重试、压缩)
│   ├── context/        # 上下文工程 (提示词构建、压缩、注入扫描)
│   ├── llm/            # LLM 提供者 (OpenAI/Anthropic/Gemini/Bedrock)
│   ├── tool/           # 工具系统 (28+ 工具)
│   ├── sandbox/        # 沙箱环境 (local/docker/ssh)
│   ├── memory/         # 记忆系统 (内置存储、PII 清洗)
│   ├── skill/          # 技能系统 (加载、索引、预处理)
│   ├── state/          # 状态存储 (SQLite + FTS5)
│   ├── cron/           # 定时调度
│   ├── gateway/        # 消息网关 (Runner + 7 个平台适配器)
│   ├── approval/       # 命令审批
│   ├── credential/     # 凭证池
│   ├── config/         # 配置加载
│   └── mcp/            # MCP 协议 (Server + Client + OAuth)
├── skills/             # 内置技能 (5 个 SKILL.md)
├── docs/
│   └── ARCHITECTURE.md # 架构设计文档
└── go.mod
```

## 🛠️ 28 个工具

| 类别 | 工具 |
|------|------|
| 文件 | `file_read` `file_write` `file_edit` `file_search` `patch` |
| 终端 | `terminal` `code_execute` |
| 浏览器 | `browser_navigate` `browser_click` `browser_type` `browser_screenshot` `browser_cdp` `browser_handle_dialog` `browser_supervise` |
| 网页 | `web_search` `web_extract` |
| 高级 | `vision_analyze` `image_generation` `text_to_speech` `transcribe_audio` `mcp_tool` |
| 代理 | `delegate_task` |
| 记忆 | `memory` |
| 搜索 | `session_search` |
| 技能 | `skills_list` `skill_view` |
| 管理 | `todo` |
| 通信 | `send_message` |

## 📖 文档

- [架构设计文档](docs/ARCHITECTURE.md)

## 🔗 与 Claude Code 集成

在 `claude_desktop_config.json` 中添加 MCP Server：

```json
{
  "mcpServers": {
    "nexus-tools": {
      "command": "nexus-acp",
      "args": []
    }
  }
}
```

## 📄 许可证

MIT
