// Package gateway 提供网关运行器。
// GatewayRunner 是消息网关的主控器，负责:
//   - 连接所有启用的平台适配器
//   - 消息路由到 AIAgent
//   - 流式响应投递
//   - 会话生命周期管理
package gateway

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"

	"nexus-agent/internal/config"
	"nexus-agent/internal/cron"
	"nexus-agent/internal/gateway/platforms"
	"nexus-agent/internal/state"
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
	registry := platforms.NewAdapterRegistry()
	platforms.RegisterAllAdapters(registry)

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

