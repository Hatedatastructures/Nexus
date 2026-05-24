// Package auth 提供第三方 OAuth 认证流程。
// 当前支持 Google OAuth 2.0 PKCE 浏览器授权。
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// ───────────────────────────── 常量 ─────────────────────────────

const (
	// maxOAuthResponseSize limits response body reads from external API calls to 10 MB.
	maxOAuthResponseSize = 10 << 20 // 10 MB

	// googleAuthEndpoint Google 授权端点。
	googleAuthEndpoint = "https://accounts.google.com/o/oauth2/v2/auth"

	// googleTokenEndpoint Google token 端点。
	googleTokenEndpoint = "https://oauth2.googleapis.com/token"

	// codeVerifierLen PKCE code_verifier 长度 (43-128 字符，RFC 7636)。
	codeVerifierLen = 64

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

// ───────────────────────────── PKCE 辅助函数 ─────────────────────────────

// generateCodeVerifier 生成 PKCE code_verifier。
// 长度为 43-128 字符，仅包含 [A-Z] / [a-z] / [0-9] / "-" / "." / "_" / "~"。
func generateCodeVerifier() (string, error) {
	// 使用 32 字节随机数，base64url 编码后为 43 字符
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成随机数失败: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// generateCodeChallenge 从 code_verifier 计算 PKCE code_challenge。
// 使用 S256 方法: BASE64URL(SHA256(code_verifier))。
func generateCodeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
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
		return nil, fmt.Errorf("生成 PKCE code_verifier 失败: %w", err)
	}
	codeChallenge := generateCodeChallenge(codeVerifier)

	// 2. 启动本地回调 server，随机分配端口
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("启动本地监听失败: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d%s", port, callbackPath)
	g.redirectURI = redirectURI

	// 3. 构建授权 URL
	state, err := generateCodeVerifier() // 复用生成随机 state
	if err != nil {
		listener.Close()
		return nil, fmt.Errorf("生成 state 参数失败: %w", err)
	}

	authURL := g.buildAuthURL(codeChallenge, state)

	// 4. 创建 channel 接收回调结果
	type callbackResult struct {
		code  string
		err   error
	}
	resultCh := make(chan callbackResult, 1)

	// 5. 启动 HTTP server 处理回调
	mux := http.NewServeMux()
	mux.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) {
		// 验证 state 参数
		returnedState := r.URL.Query().Get("state")
		if subtle.ConstantTimeCompare([]byte(returnedState), []byte(state)) != 1 {
			http.Error(w, "state 参数不匹配，可能存在 CSRF 攻击", http.StatusBadRequest)
			resultCh <- callbackResult{err: fmt.Errorf("state 参数不匹配")}
			return
		}

		// 检查错误响应
		if errParam := r.URL.Query().Get("error"); errParam != "" {
			desc := r.URL.Query().Get("error_description")
			http.Error(w, fmt.Sprintf("授权失败: %s - %s", errParam, desc), http.StatusBadRequest)
			resultCh <- callbackResult{err: fmt.Errorf("OAuth 错误: %s - %s", errParam, desc)}
			return
		}

		// 提取 authorization code
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "缺少 authorization code", http.StatusBadRequest)
			resultCh <- callbackResult{err: fmt.Errorf("回调中缺少 authorization code")}
			return
		}

		// 返回成功页面
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!DOCTYPE html><html><head><title>Nexus OAuth</title></head><body>
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
		server.Shutdown(context.Background())
		return nil, ctx.Err()
	case result := <-resultCh:
		if result.err != nil {
			server.Shutdown(context.Background())
			return nil, result.err
		}
		code = result.code
	}

	// 8. 关闭 server
	shutdownCtx, cancel := context.WithTimeout(context.Background(), serverShutdownTimeout)
	defer cancel()
	server.Shutdown(shutdownCtx)

	// 9. 用 authorization code 换取 token
	token, err := g.exchangeCode(ctx, code, codeVerifier)
	if err != nil {
		return nil, fmt.Errorf("用 authorization code 换取 token 失败: %w", err)
	}

	// 10. 持久化 token
	if err := g.SaveToken(token); err != nil {
		slog.Warn("failed to save token", "error", err)
	}

	slog.Info("Google OAuth authorization succeeded")
	return token, nil
}

// buildAuthURL 构建 Google OAuth 授权 URL。
func (g *GoogleOAuth) buildAuthURL(codeChallenge, state string) string {
	params := url.Values{
		"client_id":             {g.clientID},
		"redirect_uri":          {g.redirectURI},
		"response_type":         {"code"},
		"scope":                 {strings.Join(g.scopes, " ")},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
		"access_type":           {"offline"}, // 请求 refresh_token
		"prompt":                {"consent"}, // 强制显示同意页面以获取 refresh_token
	}
	return googleAuthEndpoint + "?" + params.Encode()
}

// exchangeCode 用 authorization code + PKCE code_verifier 换取 token。
func (g *GoogleOAuth) exchangeCode(ctx context.Context, code, codeVerifier string) (*Token, error) {
	data := url.Values{
		"client_id":     {g.clientID},
		"code":          {code},
		"code_verifier": {codeVerifier},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {g.redirectURI},
	}

	return g.doTokenRequest(ctx, data)
}

// ───────────────────────────── Token 刷新 ─────────────────────────────

// RefreshToken 使用 refresh_token 获取新的 access_token。
// 如果响应中包含新的 refresh_token，则更新；否则保留原有的。
func (g *GoogleOAuth) RefreshToken(ctx context.Context, token *Token) (*Token, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if token == nil || token.RefreshToken == "" {
		return nil, fmt.Errorf("token 或 refresh_token 为空，无法刷新")
	}

	data := url.Values{
		"client_id":     {g.clientID},
		"refresh_token": {token.RefreshToken},
		"grant_type":    {"refresh_token"},
	}

	newToken, err := g.doTokenRequest(ctx, data)
	if err != nil {
		return nil, fmt.Errorf("刷新 token 失败: %w", err)
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

// ───────────────────────────── Token 持久化 ─────────────────────────────

// LoadToken 从文件加载已保存的 token。
// 如果文件不存在，返回 nil (非错误)。
func (g *GoogleOAuth) LoadToken() (*Token, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.loadTokenFile()
}

// loadTokenFile 内部加载实现（调用者需持锁）。
func (g *GoogleOAuth) loadTokenFile() (*Token, error) {
	data, err := os.ReadFile(g.tokenFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // 文件不存在，非错误
		}
		return nil, fmt.Errorf("读取 token 文件失败: %w", err)
	}

	var token Token
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, fmt.Errorf("解析 token 文件失败: %w", err)
	}

	return &token, nil
}

// SaveToken 将 token 保存到文件。
// 自动创建目录，文件权限设为 0600（仅所有者可读写）。
func (g *GoogleOAuth) SaveToken(token *Token) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.saveTokenFile(token)
}

// saveTokenFile 内部保存实现（调用者需持锁）。
func (g *GoogleOAuth) saveTokenFile(token *Token) error {
	if token == nil {
		return fmt.Errorf("token 为空，无法保存")
	}

	// 确保目录存在
	dir := filepath.Dir(g.tokenFile)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("创建凭证目录失败: %w", err)
	}

	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 token 失败: %w", err)
	}

	// 写入临时文件后重写，确保原子性
	tmpFile := g.tokenFile + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0600); err != nil {
		return fmt.Errorf("写入 token 文件失败: %w", err)
	}

	if err := os.Rename(tmpFile, g.tokenFile); err != nil {
		os.Remove(tmpFile) // 清理临时文件
		return fmt.Errorf("重命名 token 文件失败: %w", err)
	}

	return nil
}

// ───────────────────────────── HTTP 请求 ─────────────────────────────

// doTokenRequest 执行 OAuth token 端点请求。
func (g *GoogleOAuth) doTokenRequest(ctx context.Context, data url.Values) (*Token, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, googleTokenEndpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("创建 token 请求失败: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token 请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxOAuthResponseSize))
	if err != nil {
		return nil, fmt.Errorf("读取 token 响应失败: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("token 端点返回 HTTP %d: %s", resp.StatusCode, string(body))
	}

	// 解析 Google token 响应
	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"` // 秒数
		TokenType    string `json:"token_type"`
		Scope        string `json:"scope"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("解析 token 响应失败: %w", err)
	}

	token := &Token{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		Expiry:       time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second),
		TokenType:    tokenResp.TokenType,
	}

	if token.TokenType == "" {
		token.TokenType = "Bearer"
	}

	return token, nil
}

// ───────────────────────────── 浏览器打开 ─────────────────────────────

// openBrowser 用系统默认浏览器打开指定 URL。
// 支持 Windows / macOS / Linux。
func openBrowser(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	case "linux":
		return exec.Command("xdg-open", url).Start()
	default:
		return fmt.Errorf("不支持的操作系统: %s", runtime.GOOS)
	}
}
