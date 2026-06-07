// Package auth 提供第三方 OAuth 认证流程。
// 当前支持 Google OAuth 2.0 PKCE 浏览器授权。
package auth

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	pkgerrors "nexus-agent/internal/errors"
)

// ───────────────────────────── 常量 ─────────────────────────────

const (
	// maxOAuthResponseSize limits response body reads from external API calls to 10 MB.
	maxOAuthResponseSize = 10 << 20 // 10 MB

	// googleAuthEndpoint Google 授权端点。
	googleAuthEndpoint = "https://accounts.google.com/o/oauth2/v2/auth"

	// googleTokenEndpoint Google token 端点。
	googleTokenEndpoint = "https://oauth2.googleapis.com/token"

	// callbackPath 本地 HTTP server 回调路径。
	callbackPath = "/oauth/callback"

	// credentialDir 凭证存储目录 (相对于用户主目录)。
	credentialDir = ".nexus/credentials"

	// credentialFile 凭证文件名。
	credentialFile = "google.json"

	// serverShutdownTimeout 关闭本地回调 server 的等待时间。
	serverShutdownTimeout = 3 * time.Second
)

// ───────────────────────────── Token 结构 ─────────────────────────────

// Token 表示 OAuth 访问令牌及其元数据。
type Token struct {
	AccessToken  string    `json:"access_token"`  // 访问令牌
	RefreshToken string    `json:"refresh_token"` // 刷新令牌 (长期有效)
	Expiry       time.Time `json:"expiry"`        // 过期时间
	TokenType    string    `json:"token_type"`    // 令牌类型，通常为 "Bearer"
}

// Valid 判断 token 是否仍然有效（未过期且提前 60 秒刷新）。
func (t *Token) Valid() bool {
	if t == nil || t.AccessToken == "" {
		return false
	}
	// 提前 60 秒视为过期，避免边界情况
	return time.Now().Before(t.Expiry.Add(-60 * time.Second))
}

// ───────────────────────────── GoogleOAuth 结构 ─────────────────────────────

// GoogleOAuth 实现 Google OAuth 2.0 PKCE 授权流程。
//
// 使用流程:
//  1. 调用 NewGoogleOAuth 创建客户端。
//  2. 调用 StartFlow 启动浏览器授权。
//  3. 成功后通过 LoadToken / SaveToken 管理 token 持久化。
//  4. Token 过期时调用 RefreshToken 刷新。
type GoogleOAuth struct {
	clientID    string   // Google OAuth 客户端 ID
	redirectURI string   // 重定向 URI (动态分配)
	scopes      []string // 请求的 OAuth 作用域
	tokenFile   string   // token 持久化文件路径
	mu          sync.Mutex
}

// NewGoogleOAuth 创建 Google OAuth PKCE 客户端。
// 默认请求 email 和 profile 作用域。
func NewGoogleOAuth(clientID string) *GoogleOAuth {
	homeDir, _ := os.UserHomeDir()
	tokenPath := filepath.Join(homeDir, credentialDir, credentialFile)

	return &GoogleOAuth{
		clientID: clientID,
		scopes: []string{
			"openid",
			"https://www.googleapis.com/auth/userinfo.email",
			"https://www.googleapis.com/auth/userinfo.profile",
		},
		tokenFile: tokenPath,
	}
}

// WithScopes 设置自定义 OAuth 作用域（用于链式调用）。
func (g *GoogleOAuth) WithScopes(scopes ...string) *GoogleOAuth {
	g.scopes = scopes
	return g
}

// WithTokenFile 设置自定义 token 存储路径。
func (g *GoogleOAuth) WithTokenFile(path string) *GoogleOAuth {
	g.tokenFile = path
	return g
}

// ───────────────────────────── 授权流程 ─────────────────────────────

// StartFlow 启动 Google OAuth 浏览器授权流程。
//
// 流程:
//  1. 生成 PKCE code_verifier + code_challenge。
//  2. 启动本地 HTTP server 监听回调。
//  3. 用系统浏览器打开 Google 授权页面。
//  4. 等待用户授权后回调携带 authorization code。
//  5. 用 code + code_verifier 换取 access_token。
func (g *GoogleOAuth) StartFlow(ctx context.Context) (*Token, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	// 1. 生成 PKCE 参数
	codeVerifier, err := generateCodeVerifier()
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.AuthOAuth, "生成 PKCE code_verifier 失败", err)
	}
	codeChallenge := generateCodeChallenge(codeVerifier)

	// 2. 启动本地回调 server，随机分配端口
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.AuthOAuth, "启动本地监听失败", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d%s", port, callbackPath)
	g.redirectURI = redirectURI

	// 3. 构建授权 URL
	state, err := generateCodeVerifier() // 复用生成随机 state
	if err != nil {
		_ = listener.Close()
		return nil, pkgerrors.Wrap(pkgerrors.AuthOAuth, "生成 state 参数失败", err)
	}

	authURL := g.buildAuthURL(codeChallenge, state)

	// 4. 创建 channel 接收回调结果
	type callbackResult struct {
		code string
		err  error
	}
	resultCh := make(chan callbackResult, 1)

	// 5. 启动 HTTP server 处理回调
	mux := http.NewServeMux()
	mux.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) {
		// 验证 state 参数
		returnedState := r.URL.Query().Get("state")
		if subtle.ConstantTimeCompare([]byte(returnedState), []byte(state)) != 1 {
			http.Error(w, "state 参数不匹配，可能存在 CSRF 攻击", http.StatusBadRequest)
			resultCh <- callbackResult{err: pkgerrors.New(pkgerrors.AuthOAuth, "state 参数不匹配")}
			return
		}

		// 检查错误响应
		if errParam := r.URL.Query().Get("error"); errParam != "" {
			desc := r.URL.Query().Get("error_description")
			http.Error(w, fmt.Sprintf("授权失败: %s - %s", errParam, desc), http.StatusBadRequest)
			resultCh <- callbackResult{err: pkgerrors.New(pkgerrors.AuthOAuth, fmt.Sprintf("OAuth 错误: %s - %s", errParam, desc))}
			return
		}

		// 提取 authorization code
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "缺少 authorization code", http.StatusBadRequest)
			resultCh <- callbackResult{err: pkgerrors.New(pkgerrors.AuthOAuth, "回调中缺少 authorization code")}
			return
		}

		// 返回成功页面
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(w, `<!DOCTYPE html><html><head><title>Nexus OAuth</title></head><body>
<h2>授权成功</h2><p>您现在可以关闭此页面并返回 Nexus。</p></body></html>`)

		resultCh <- callbackResult{code: code}
	})

	server := &http.Server{Handler: mux}

	// 在 goroutine 中启动 server
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			slog.Debug("OAuth callback server stopped", "error", err)
		}
	}()

	// 6. 打开浏览器
	slog.Info("opening browser for Google authorization...", "url", authURL)
	if err := openBrowser(authURL); err != nil {
		slog.Warn("unable to auto-open browser, please manually open the following URL", "url", authURL, "error", err)
	}

	// 7. 等待回调结果或 context 取消
	var code string
	select {
	case <-ctx.Done():
		_ = server.Shutdown(context.Background())
		return nil, ctx.Err()
	case result := <-resultCh:
		if result.err != nil {
			_ = server.Shutdown(context.Background())
			return nil, result.err
		}
		code = result.code
	}

	// 8. 关闭 server
	shutdownCtx, cancel := context.WithTimeout(context.Background(), serverShutdownTimeout)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)

	// 9. 用 authorization code 换取 token
	token, err := g.exchangeCode(ctx, code, codeVerifier)
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.AuthOAuth, "用 authorization code 换取 token 失败", err)
	}

	// 10. 持久化 token
	if err := g.SaveToken(token); err != nil {
		slog.Warn("failed to save token", "error", err)
	}

	slog.Info("Google OAuth authorization succeeded")
	return token, nil
}

// ───────────────────────────── Token 刷新 ─────────────────────────────

// RefreshToken 使用 refresh_token 获取新的 access_token。
// 如果响应中包含新的 refresh_token，则更新；否则保留原有的。
func (g *GoogleOAuth) RefreshToken(ctx context.Context, token *Token) (*Token, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if token == nil || token.RefreshToken == "" {
		return nil, pkgerrors.New(pkgerrors.AuthFailed, "token 或 refresh_token 为空，无法刷新")
	}

	data := url.Values{
		"client_id":     {g.clientID},
		"refresh_token": {token.RefreshToken},
		"grant_type":    {"refresh_token"},
	}

	newToken, err := g.doTokenRequest(ctx, data)
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.AuthOAuth, "刷新 token 失败", err)
	}

	// Google 刷新时可能不返回新的 refresh_token，保留原有的
	if newToken.RefreshToken == "" {
		newToken.RefreshToken = token.RefreshToken
	}

	// 持久化
	if err := g.saveTokenFile(newToken); err != nil {
		slog.Warn("failed to save refreshed token", "error", err)
	}

	return newToken, nil
}

// ───────────────────────────── Token 持久化 (公开方法) ─────────────────────────────

// LoadToken 从文件加载已保存的 token。
// 如果文件不存在，返回 nil (非错误)。
func (g *GoogleOAuth) LoadToken() (*Token, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.loadTokenFile()
}

// SaveToken 将 token 保存到文件。
// 自动创建目录，文件权限设为 0600（仅所有者可读写）。
func (g *GoogleOAuth) SaveToken(token *Token) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.saveTokenFile(token)
}
