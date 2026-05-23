// Package mcp 提供 MCP Client 实现。
// 用于与其他 MCP 服务器通过 stdin/stdout 进行通信。
package mcp

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ───────────────────────────── OAuth 配置 ─────────────────────────────

// OAuthConfig 包含 MCP 服务器 OAuth 2.1 认证所需的配置项。
type OAuthConfig struct {
	ClientID     string   // 客户端 ID
	ClientSecret string   // 客户端密钥（仅机密客户端需要）
	AuthURL      string   // 授权端点（/authorize）
	TokenURL     string   // Token 端点（/token）
	RedirectURI  string   // 回调 URI（如 http://127.0.0.1:PORT/callback）
	Scopes       []string // 请求的 scope 列表
}

// ───────────────────────────── PKCE 工具函数 ─────────────────────────────

// generateCodeVerifier 生成 PKCE code_verifier（随机 43-128 字符）。
// 使用 crypto/rand 确保密码学安全，返回 64 字符的 Base64URL 无填充字符串。
func generateCodeVerifier() (string, error) {
	b := make([]byte, 48) // 48 字节 Base64URL 编码后约 64 字符
	_, err := rand.Read(b)
	if err != nil {
		return "", fmt.Errorf("生成 code_verifier 失败: %w", err)
	}
	// 使用 Base64URL 无填充编码
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// generateCodeChallenge 根据 code_verifier 生成 S256 code_challenge。
// 计算 SHA256 哈希后进行 Base64URL 无填充编码。
func generateCodeChallenge(verifier string) string {
	hash := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

// BuildAuthorizationURL 构建 OAuth 2.1 授权 URL。
// 自动生成 PKCE code_verifier 和 code_challenge，返回 (URL, verifier, error)。
func BuildAuthorizationURL(cfg *OAuthConfig, state string) (authURL, verifier string, err error) {
	verifier, err = generateCodeVerifier()
	if err != nil {
		return "", "", err
	}

	challenge := generateCodeChallenge(verifier)

	u, err := url.Parse(cfg.AuthURL)
	if err != nil {
		return "", "", fmt.Errorf("解析授权 URL 失败: %w", err)
	}

	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", cfg.ClientID)
	q.Set("redirect_uri", cfg.RedirectURI)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")

	if len(cfg.Scopes) > 0 {
		q.Set("scope", strings.Join(cfg.Scopes, " "))
	}

	u.RawQuery = q.Encode()
	slog.Debug("OAuth authorization URL built",
		"auth_url", u.String(),
		"state", state,
	)
	return u.String(), verifier, nil
}

// ───────────────────────────── Token 类型 ─────────────────────────────

// OAuthToken 表示从 OAuth Token 端点获取的令牌响应。
type OAuthToken struct {
	AccessToken  string `json:"access_token"`            // 访问令牌
	TokenType    string `json:"token_type"`              // 令牌类型（通常为 "Bearer"）
	ExpiresIn    int64  `json:"expires_in"`              // 有效期（秒）
	RefreshToken string `json:"refresh_token,omitempty"` // 刷新令牌
	Scope        string `json:"scope,omitempty"`         // 已授权 scope
	// 内部字段：绝对过期时间戳（Unix 秒），用于进程重启后判断令牌是否过期
	ExpiresAt int64 `json:"expires_at"`
}

// IsExpired 检查令牌是否已过期。
func (t *OAuthToken) IsExpired() bool {
	if t.ExpiresAt <= 0 {
		// 没有记录绝对过期时间，认为未过期（保守策略）
		return false
	}
	return time.Now().Unix() >= t.ExpiresAt
}

// ───────────────────────────── Token 交换 ─────────────────────────────

// ExchangeCodeForToken 用授权码交换访问令牌。
// code 是授权回调中收到的 code，verifier 是原始 code_verifier。
func ExchangeCodeForToken(cfg *OAuthConfig, code, verifier string) (*OAuthToken, error) {
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", code)
	data.Set("redirect_uri", cfg.RedirectURI)
	data.Set("code_verifier", verifier)

	if cfg.ClientSecret != "" {
		data.Set("client_secret", cfg.ClientSecret)
	}

	return doTokenRequest(cfg.TokenURL, data, cfg.ClientID)
}

// RefreshToken 使用刷新令牌获取新的访问令牌。
func RefreshToken(cfg *OAuthConfig, refreshToken string) (*OAuthToken, error) {
	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", refreshToken)

	if cfg.ClientSecret != "" {
		data.Set("client_secret", cfg.ClientSecret)
	}

	return doTokenRequest(cfg.TokenURL, data, cfg.ClientID)
}

// doTokenRequest 执行通用的 Token HTTP 请求。
func doTokenRequest(tokenURL string, data url.Values, clientID string) (*OAuthToken, error) {
	req, err := http.NewRequest("POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("创建 Token 请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	// 公共客户端使用 Authorization 头传递 client_id
	if clientID != "" {
		req.SetBasicAuth(clientID, "")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Token 请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 Token 响应失败: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		slog.Error("Token endpoint returned error",
			"status", resp.StatusCode,
			"body", string(body),
		)
		return nil, fmt.Errorf("Token 端点返回 HTTP %d: %s", resp.StatusCode, string(body))
	}

	// 解析标准 OAuthToken 响应
	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
		RefreshToken string `json:"refresh_token"`
		Scope        string `json:"scope"`
		Error        string `json:"error"`
		ErrorDesc    string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("解析 Token 响应 JSON 失败: %w", err)
	}

	if tokenResp.Error != "" {
		return nil, fmt.Errorf("OAuth 错误: %s (%s)", tokenResp.Error, tokenResp.ErrorDesc)
	}

	token := &OAuthToken{
		AccessToken:  tokenResp.AccessToken,
		TokenType:    tokenResp.TokenType,
		ExpiresIn:    tokenResp.ExpiresIn,
		RefreshToken: tokenResp.RefreshToken,
		Scope:        tokenResp.Scope,
	}

	// 记录绝对过期时间
	if tokenResp.ExpiresIn > 0 {
		token.ExpiresAt = time.Now().Unix() + tokenResp.ExpiresIn
	}

	slog.Info("Token exchange succeeded",
		"token_type", token.TokenType,
		"expires_in", tokenResp.ExpiresIn,
		"has_refresh", tokenResp.RefreshToken != "",
	)

	return token, nil
}

// ───────────────────────────── OAuth 授权流程（交互式） ─────────────────────────────

// OAuthFlowResult 保存交互式 OAuth 授权流程的中间结果。
type OAuthFlowResult struct {
	AuthURL   string // 用户需要打开的授权 URL
	Verifier  string // 内部 code_verifier，用于后续 Token 交换
	State     string // 随机 state 参数
}

// StartOAuthFlow 启动 OAuth 2.1 PKCE 授权流程，返回授权 URL 和中间状态。
// 用户需要在浏览器中打开 AuthURL 完成授权，然后回调会携带 code 和 state。
func StartOAuthFlow(cfg *OAuthConfig) (*OAuthFlowResult, error) {
	state, err := generateCodeVerifier() // 复用随机字符串生成器
	if err != nil {
		return nil, fmt.Errorf("生成 state 失败: %w", err)
	}

	authURL, verifier, err := BuildAuthorizationURL(cfg, state)
	if err != nil {
		return nil, err
	}

	return &OAuthFlowResult{
		AuthURL:  authURL,
		Verifier: verifier,
		State:    state,
	}, nil
}

// CompleteOAuthFlow 用授权回调中的 code 完成 Token 交换。
// 返回完整的 OAuthToken。
func CompleteOAuthFlow(cfg *OAuthConfig, code, state, verifier string) (*OAuthToken, error) {
	token, err := ExchangeCodeForToken(cfg, code, verifier)
	if err != nil {
		return nil, err
	}

	slog.Info("OAuth authorization flow completed",
		"access_token_prefix", token.AccessToken[:min(10, len(token.AccessToken))],
		"expires_at", token.ExpiresAt,
	)
	return token, nil
}
