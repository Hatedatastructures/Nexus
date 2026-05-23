package mcp

import (
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// ───────────────────────────── OAuth 管理器 ─────────────────────────────

// OAuthManager 管理 OAuth 2.1 令牌的生命周期。
// 包括自动刷新、缓存、跨进程磁盘监视等。
// 线程安全：所有公开方法都是 goroutine-safe。
type OAuthManager struct {
	cfg   *OAuthConfig       // OAuth 配置
	store *TokenStore        // 持久化存储
	mu    sync.RWMutex       // 并发保护
	token *OAuthToken        // 内存中的令牌缓存
}

// NewOAuthManager 创建一个新的 OAuth 管理器实例。
// config 是 OAuth 配置，storePath 是令牌文件存储路径（为空则用默认路径）。
func NewOAuthManager(config *OAuthConfig, storePath string) (*OAuthManager, error) {
	if config == nil {
		return nil, fmt.Errorf("OAuth 配置不能为空")
	}

	store, err := NewTokenStore(storePath)
	if err != nil {
		return nil, fmt.Errorf("创建令牌存储失败: %w", err)
	}

	mgr := &OAuthManager{
		cfg:   config,
		store: store,
	}

	// 尝试从磁盘加载已缓存的令牌
	if err := mgr.loadFromDisk(); err != nil {
		slog.Debug("failed to load token from disk (first use or token deleted)", "err", err)
	}

	return mgr, nil
}

// loadFromDisk 从持久化存储加载令牌到内存。
func (m *OAuthManager) loadFromDisk() error {
	token, err := m.store.LoadToken()
	if err != nil {
		return err
	}

	m.mu.Lock()
	m.token = token
	m.mu.Unlock()

	if token.IsExpired() {
		slog.Info("loaded OAuth token is expired, will attempt refresh")
	} else {
		slog.Debug("loaded valid OAuth token from disk",
			"expires_at", token.ExpiresAt,
		)
	}
	return nil
}

// ───────────────────────────── 公开 API ─────────────────────────────

// GetValidToken 获取一个有效的访问令牌。
// 如果当前令牌已过期但有刷新令牌，则自动刷新。
// 如果当前令牌有效（含预留 30 秒缓冲），直接返回。
// 返回的令牌可直接用于 Bearer 认证。
func (m *OAuthManager) GetValidToken() (string, error) {
	m.mu.RLock()
	token := m.token
	m.mu.RUnlock()

	if token == nil || token.AccessToken == "" {
		return "", fmt.Errorf("没有可用的 OAuth 令牌，请先完成授权流程")
	}

	// 检查令牌是否过期（含 30 秒缓冲）
	if m.isTokenExpiringSoon(token) {
		// 需要刷新
		slog.Info("OAuth token expiring soon, auto-refreshing...")

		if token.RefreshToken == "" {
			return "", fmt.Errorf("令牌已过期且没有刷新令牌，请重新授权")
		}

		// 执行刷新
		newToken, err := m.refreshToken()
		if err != nil {
			return "", fmt.Errorf("刷新令牌失败: %w", err)
		}
		return newToken.AccessToken, nil
	}

	return token.AccessToken, nil
}

// IsTokenExpired 检查当前令牌是否已过期。
// 返回 (是否过期, 是否有令牌)。
func (m *OAuthManager) IsTokenExpired() (expired, exists bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	token := m.token
	if token == nil || token.AccessToken == "" {
		return false, false
	}
	return token.IsExpired(), true
}

// refreshToken 使用刷新令牌获取新的访问令牌。
// 刷新成功后自动更新内存缓存和持久化存储。
func (m *OAuthManager) refreshToken() (*OAuthToken, error) {
	m.mu.RLock()
	currentToken := m.token
	m.mu.RUnlock()

	if currentToken == nil || currentToken.RefreshToken == "" {
		return nil, fmt.Errorf("没有可用的刷新令牌")
	}

	slog.Info("refreshing OAuth access token...")

	newToken, err := RefreshToken(m.cfg, currentToken.RefreshToken)
	if err != nil {
		slog.Error("OAuth token refresh failed", "err", err)
		return nil, err
	}

	// 如果服务端没有返回新的刷新令牌，保留旧的
	if newToken.RefreshToken == "" {
		newToken.RefreshToken = currentToken.RefreshToken
	}

	// 更新内存缓存
	m.mu.Lock()
	m.token = newToken
	m.mu.Unlock()

	// 持久化到磁盘
	if err := m.store.SaveToken(newToken); err != nil {
		slog.Warn("failed to persist refreshed token", "err", err)
	}

	slog.Info("OAuth token refreshed successfully",
		"expires_at", newToken.ExpiresAt,
	)

	return newToken, nil
}

// RefreshToken 公开方法：使用刷新令牌获取新的访问令牌（代理到内部 refreshToken）。
func (m *OAuthManager) RefreshToken() (*OAuthToken, error) {
	return m.refreshToken()
}

// SetToken 手动设置令牌（用于授权流程完成后存入）。
func (m *OAuthManager) SetToken(token *OAuthToken) error {
	if token == nil {
		return fmt.Errorf("令牌不能为空")
	}

	m.mu.Lock()
	m.token = token
	m.mu.Unlock()

	// 持久化到磁盘
	if err := m.store.SaveToken(token); err != nil {
		return fmt.Errorf("保存令牌失败: %w", err)
	}

	slog.Info("OAuth token set and persisted",
		"expires_at", token.ExpiresAt,
	)
	return nil
}

// ClearToken 清除当前令牌（内存 + 磁盘）。
func (m *OAuthManager) ClearToken() error {
	m.mu.Lock()
	m.token = nil
	m.mu.Unlock()

	if err := m.store.DeleteToken(); err != nil {
		slog.Warn("failed to delete disk token", "err", err)
	}

	slog.Info("OAuth token cleared")
	return nil
}

// GetTokenInfo 返回当前令牌的摘要信息（不暴露完整令牌）。
func (m *OAuthManager) GetTokenInfo() map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.token == nil || m.token.AccessToken == "" {
		return nil
	}

	info := map[string]any{
		"has_access_token":  true,
		"has_refresh_token": m.token.RefreshToken != "",
		"token_type":        m.token.TokenType,
		"scope":             m.token.Scope,
		"expires_at":        m.token.ExpiresAt,
		"is_expired":        m.token.IsExpired(),
		"expires_in":        m.calculateRemainingTTL(),
	}
	return info
}

// ───────────────────────────── 内部方法 ─────────────────────────────

// isTokenExpiringSoon 检查令牌是否已过期或即将过期（30 秒缓冲）。
func (m *OAuthManager) isTokenExpiringSoon(token *OAuthToken) bool {
	if token.ExpiresAt <= 0 {
		return false
	}
	// 30 秒缓冲，避免在边界处反复
	return time.Now().Unix()+30 >= token.ExpiresAt
}

// calculateRemainingTTL 计算令牌剩余有效期（秒）。
func (m *OAuthManager) calculateRemainingTTL() int64 {
	if m.token == nil || m.token.ExpiresAt <= 0 {
		return 0
	}
	ttl := m.token.ExpiresAt - time.Now().Unix()
	if ttl < 0 {
		return 0
	}
	return ttl
}

// ───────────────────────────── 模块级单例 ─────────────────────────────

var (
	defaultManager     *OAuthManager
	defaultManagerMu   sync.Mutex
)

// GetDefaultManager 返回进程级的 OAuth 管理器单例。
// 首次调用时用 config 初始化，后续调用返回同一实例。
func GetDefaultManager(config *OAuthConfig, storePath string) (*OAuthManager, error) {
	defaultManagerMu.Lock()
	defer defaultManagerMu.Unlock()

	if defaultManager != nil {
		return defaultManager, nil
	}

	mgr, err := NewOAuthManager(config, storePath)
	if err != nil {
		return nil, err
	}

	defaultManager = mgr
	return mgr, nil
}

// ResetDefaultManager 重置单例（仅用于测试）。
func ResetDefaultManager() {
	defaultManagerMu.Lock()
	defer defaultManagerMu.Unlock()
	defaultManager = nil
}
