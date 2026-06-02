// Package gateway 提供网关运行器。
// GatewayRunner 是消息网关的主控器，负责:
//   - 连接所有启用的平台适配器
//   - 消息路由到 AIAgent
//   - 流式响应投递
//   - 会话生命周期管理
package gateway

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"nexus-agent/internal/agent"
	"nexus-agent/internal/config"
	"nexus-agent/internal/cron"
	ctxbuilder "nexus-agent/internal/context"
	"nexus-agent/internal/gateway/platforms"
	"nexus-agent/internal/llm"
	"nexus-agent/internal/memory"
	"nexus-agent/internal/skill"
	"nexus-agent/internal/state"
	"nexus-agent/internal/tool"
)

// ───────────────────────────── 网关运行器 ─────────────────────────────

// GatewayRunner 是消息网关的中央编排器。
// 管理平台适配器、代理缓存、会话路由和流式投递。
type GatewayRunner struct {
	config      *config.GatewayConfig
	adapters    []platforms.PlatformAdapter // 所有已启用的平台适配器
	agentCache  *AgentCache                 // 代理实例缓存
	sessionMgr  *SessionManager             // 会话管理
	deliveryMgr *DeliveryManager            // 消息投递管理
	hookReg     *HookRegistry               // 消息钩子
	agentConfig *config.AgentConfig         // 代理配置 (窄配置，保留向后兼容)
	fullConfig  *config.Config              // 完整配置 (用于构建 Builder/Provider/Memory/Skill)
	state       *state.Store                // 持久化存储
	cronSched   *cron.Scheduler             // 定时调度器
	wg          sync.WaitGroup              // goroutine 计数器
	shutdownCh  chan struct{}               // 关闭信号通道
	stopOnce    sync.Once                   // 确保 Stop() 只执行一次，防止二次 close panic
	msgSem      chan struct{}               // 消息处理并发信号量
}

// NewGatewayRunner 创建网关运行器。
// cfg 为网关配置，fullCfg 为完整配置 (用于构建 Builder/Provider)，state 为持久化存储，cronSched 为定时调度器。
func NewGatewayRunner(cfg *config.GatewayConfig, fullCfg *config.Config, state *state.Store, cronSched *cron.Scheduler) *GatewayRunner {
	// 初始化缓存
	cacheSize := cfg.Cache.MaxSize
	if cacheSize <= 0 {
		cacheSize = 128
	}
	idleTTL := cfg.Cache.IdleTTL
	if idleTTL <= 0 {
		idleTTL = time.Hour
	}

	// 初始化消息处理并发信号量
	maxConcurrent := 10
	if v := os.Getenv("NEXUS_MAX_CONCURRENT_MESSAGES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxConcurrent = n
		}
	}

	return &GatewayRunner{
		config:      cfg,
		agentCache:  NewAgentCache(cacheSize, idleTTL),
		sessionMgr:  NewSessionManager(),
		deliveryMgr: NewDeliveryManager(4096),
		hookReg:     NewHookRegistry(),
		agentConfig: &fullCfg.Agent,
		fullConfig:  fullCfg,
		state:       state,
		cronSched:   cronSched,
		shutdownCh:  make(chan struct{}),
		msgSem:      make(chan struct{}, maxConcurrent),
	}
}

// RegisterAdapter 注册一个平台适配器。
// 在 Start 之前调用。
func (g *GatewayRunner) RegisterAdapter(adapter platforms.PlatformAdapter) {
	g.adapters = append(g.adapters, adapter)
	slog.Info("registered platform adapter",
		"name", adapter.Name(),
		"platform", string(adapter.PlatformType()),
	)
}

// RegisterFromRegistry 从全局适配器注册中心创建并注册所有适配器。
// 仅注册配置中启用的平台，对支持 ConfigurableAdapter 接口的适配器注入配置。
func (g *GatewayRunner) RegisterFromRegistry(gwCfg *config.GatewayConfig) {
	registry := platforms.GetRegistry()

	for _, entry := range gwCfg.Platforms {
		if !entry.Enabled {
			slog.Info("skipping disabled platform", "platform", entry.Platform)
			continue
		}

		platform := platforms.Platform(entry.Platform)
		adapter, err := registry.Create(platform)
		if err != nil {
			slog.Warn("platform adapter not registered, skipping",
				"platform", entry.Platform,
				"err", err,
			)
			continue
		}

		// 对支持配置注入的适配器，注入平台配置参数
		if configurable, ok := adapter.(platforms.ConfigurableAdapter); ok {
			settings := make(map[string]any)
			// 复制 settings
			for k, v := range entry.Settings {
				settings[k] = v
			}
			// Token 字段以 "token" 键传入
			if entry.Token != "" {
				settings["token"] = entry.Token
			}

			if err := configurable.Configure(settings); err != nil {
				slog.Warn("platform adapter configuration failed, skipping",
					"platform", entry.Platform,
					"err", err,
				)
				continue
			}
		}

		g.RegisterAdapter(adapter)
	}
}

// Start 启动网关。
// 连接所有平台适配器，启动消息处理循环和后台维护任务。
func (g *GatewayRunner) Start(ctx context.Context) error {
	slog.Info("starting gateway runner",
		"adapters", len(g.adapters),
	)

	// 启动 cron 调度器 (阻塞运行，在独立 goroutine 中启动)
	if g.cronSched != nil {
		g.wg.Add(1)
		go func() {
			defer g.wg.Done()
			if err := g.cronSched.Run(ctx); err != nil && err != context.Canceled {
				slog.Warn("cron scheduler run failed", "err", err)
			}
		}()
	}

	// 启动缓存清理 goroutine (每 5 分钟扫描一次)
	g.wg.Add(1)
	go g.cacheCleaner(ctx)

	// 连接所有平台
	for _, adapter := range g.adapters {
		msgCh, err := adapter.Connect(ctx)
		if err != nil {
			slog.Error("failed to connect platform adapter",
				"name", adapter.Name(),
				"err", err,
			)
			continue
		}

		// 为每个平台启动消息处理 goroutine
		g.wg.Add(1)
		go g.handlePlatform(ctx, adapter, msgCh)
	}

	return nil
}

// Stop 优雅关闭网关。
// 断开所有平台连接，停止后台任务。
// 使用 sync.Once 确保 close(g.shutdownCh) 只执行一次，
// 防止信号处理器和 context 取消同时调用 Stop 时二次 close 导致 panic。
func (g *GatewayRunner) Stop(ctx context.Context) error {
	g.stopOnce.Do(func() {
		slog.Info("stopping gateway runner")
		// 发送关闭信号
		close(g.shutdownCh)
	})

	// 停止 cron 调度器
	if g.cronSched != nil {
		g.cronSched.Stop()
	}

	// 断开所有平台连接
	for _, adapter := range g.adapters {
		if err := adapter.Disconnect(ctx); err != nil {
			slog.Warn("failed to disconnect platform adapter",
				"name", adapter.Name(),
				"err", err,
			)
		}
	}

	// 等待所有 goroutine 退出 (带超时)
	done := make(chan struct{})
	go func() {
		g.wg.Wait()
		close(done)
	}()

	select {
	case <-ctx.Done():
		slog.Warn("gateway stop timed out waiting for goroutines")
	case <-done:
		slog.Info("gateway stopped gracefully")
	}

	return nil
}

// ───────────────────────────── 内部方法 ─────────────────────────────

// handlePlatform 处理单个平台的消息循环。
// 从 msgCh 接收消息，为每条消息启动处理 goroutine。
func (g *GatewayRunner) handlePlatform(ctx context.Context, adapter platforms.PlatformAdapter, msgCh <-chan *platforms.MessageEvent) {
	defer g.wg.Done()

	slog.Info("platform handler started",
		"name", adapter.Name(),
	)

	for {
		select {
		case <-ctx.Done():
			return
		case <-g.shutdownCh:
			return
		case msg, ok := <-msgCh:
			if !ok {
				slog.Info("platform message channel closed",
					"name", adapter.Name(),
				)
				return
			}

			// 每个消息在独立 goroutine 中处理 (受信号量控制并发)
			select {
			case g.msgSem <- struct{}{}:
			case <-ctx.Done():
				return
			case <-g.shutdownCh:
				return
			}
			g.wg.Add(1)
			go func(m *platforms.MessageEvent) {
				defer g.wg.Done()
				defer func() { <-g.msgSem }()
				if err := g.processMessage(ctx, adapter, m); err != nil {
					slog.Error("failed to process message",
						"platform", adapter.Name(),
						"chat_id", m.Source.ChatID,
						"user_id", m.Source.UserID,
						"err", err,
					)
				}
			}(msg)
		}
	}
}

// processMessage 处理单条入站消息。
// 完整的消息处理流水线: 钩子 → 会话 → 缓存 → AIAgent → 流式投递。
func (g *GatewayRunner) processMessage(ctx context.Context, adapter platforms.PlatformAdapter, msg *platforms.MessageEvent) error {
	// 1. 执行投递前钩子
	event, err := g.hookReg.Run(ctx, HookPreDispatch, msg)
	if err != nil {
		slog.Warn("pre-dispatch hook failed", "err", err)
		return err
	}
	if event == nil {
		// 钩子中止了消息处理
		return nil
	}
	msg = event

	// 2. 分析消息来源 → sessionKey
	source := msg.Source
	if source == nil {
		slog.Warn("message has no source, skipping")
		return nil
	}
	sessionKey := platforms.BuildSessionKey(source)

	// 3. 查找/创建会话
	session := g.sessionMgr.GetOrCreate(source)
	slog.Debug("message session",
		"key", sessionKey,
		"agent_id", session.AgentID,
		"reset_count", session.ResetCount,
	)

	// 4. 获取或创建代理实例
	sessionAgent, err := g.agentCache.GetOrCreate(sessionKey, func() (*agent.AIAgent, string) {
		platform := string(adapter.PlatformType())

		// 构建记忆管理器
		var memMgr *memory.Manager
		if h, err := os.UserHomeDir(); err == nil {
			builtinProvider := memory.NewBuiltinProvider(filepath.Join(h, ".nexus"))
			memMgr = memory.NewManager(builtinProvider)
		}

		// 构建技能管理器
		var skillMgr *skill.Manager
		if h, err := os.UserHomeDir(); err == nil {
			skillsDir := filepath.Join(h, ".nexus", "skills")
			skillMgr = skill.NewManager(skillsDir, g.fullConfig.Skills.Disabled)
		}

		ctxBuilder := ctxbuilder.NewBuilder("", platform, memMgr, skillMgr)
		a := agent.NewAgent(
			agent.WithConfigProvider(g.fullConfig),
			agent.WithToolRegistry(tool.GetRegistry()),
			agent.WithContextBuilder(ctxBuilder),
			agent.WithMemoryManager(memMgr),
		)
		return a, g.fullConfig.Agent.Model
	})
	if err != nil {
		slog.Error("failed to get agent from cache", "err", err)
		return err
	}
	defer g.agentCache.ReleaseInUse(sessionKey)

	// 5. 创建流式消费者
	streamCfg := g.config.Stream
	consumer := NewStreamConsumer(
		adapter,
		source.ChatID,
		streamCfg.BufferSize,
		streamCfg.EditInterval,
	)

	// 6. 设置流式回调并启动消费者
	sessionAgent.SetStreamCallback(consumer.OnDelta)
	consumerCtx, consumerCancel := context.WithCancel(ctx)
	defer consumerCancel()

	go consumer.Run(consumerCtx)

	// 7. 发送输入中指示器
	_ = adapter.SendTyping(ctx, source.ChatID)

	// 8. 执行对话
	// 加载历史消息 (最近 50 条)
	history, _ := g.loadMessageHistory(ctx, session.AgentID, 50)

	result, err := sessionAgent.RunConversation(ctx, msg.Text, history, "")
	if err != nil {
		slog.Error("conversation failed",
			"session_key", sessionKey,
			"err", err,
		)
		// 对话失败，向用户发送错误提示
		_, _ = adapter.Send(ctx, source.ChatID, "抱歉，处理您的消息时遇到了错误。请稍后重试。", nil)
		return err
	}

	// 9. 注: assistant 消息已由 agent.RunConversation 内部的 state.InsertMessage 处理，
	// 此处不再重复插入，避免双重记录。

	// 10. 等待流式消费完成 (如果已有回调投递，则此处仅为同步点)
	if result.FinalResponse != "" {
		// 如果流式回调已投递了内容，Finish 仅作为同步点
		consumer.Finish(ctx)
	} else {
		// 非流式路径: 最终回复为空时不发送空消息（原实现在 FinalResponse
		// 为空时也会调用 Send 发送空字符串，某些平台上会显示空消息）
		slog.Warn("conversation completed but no final response",
			"session_key", sessionKey,
		)
	}

	// 11. 执行投递后钩子
	finalEvent := &platforms.MessageEvent{
		Text:        result.FinalResponse,
		MessageType: platforms.MsgText,
		Source:      source,
		Timestamp:   time.Now(),
	}
	_, _ = g.hookReg.Run(ctx, HookPostDelivery, finalEvent)

	slog.Debug("message processed",
		"platform", adapter.Name(),
		"session_key", sessionKey,
	)

	return nil
}

// cacheCleaner 定期清理缓存中的空闲代理。
// 每 5 分钟扫描一次，驱逐空闲超时的条目。
func (g *GatewayRunner) cacheCleaner(ctx context.Context) {
	defer g.wg.Done()

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-g.shutdownCh:
			return
		case <-ticker.C:
			_ = g.agentCache.SweepIdle(ctx)
			g.agentCache.EnforceCap()
		}
	}
}

// loadMessageHistory 从状态存储中加载会话的最近消息。
func (g *GatewayRunner) loadMessageHistory(ctx context.Context, sessionID string, limit int) ([]llm.Message, error) {
	if g.state == nil || sessionID == "" {
		return nil, nil
	}

	records, err := g.state.GetMessages(ctx, sessionID, limit, 0)
	if err != nil {
		return nil, err
	}

	messages := make([]llm.Message, 0, len(records))
	for _, r := range records {
		msg := llm.Message{
			Role:    llm.MessageRole(r.Role),
			Content: r.Content,
		}
		// 恢复工具调用信息 (从 ToolCalls 字段反序列化为 []llm.ToolCall)
		if r.ToolCalls != "" {
			var toolCalls []llm.ToolCall
			if err := json.Unmarshal([]byte(r.ToolCalls), &toolCalls); err != nil {
				slog.Warn("failed to deserialize tool_calls from message history",
					"session_id", sessionID,
					"tool_calls_json", r.ToolCalls[:min(80, len(r.ToolCalls))],
					"err", err,
				)
			} else {
				msg.ToolCalls = toolCalls
			}
		}
		if r.ToolCallID != "" {
			msg.ToolCallID = r.ToolCallID
		}
		messages = append(messages, msg)
	}

	return messages, nil
}
