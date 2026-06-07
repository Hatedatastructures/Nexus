// Package auth 提供第三方 OAuth 认证辅助函数。
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	pkgerrors "nexus-agent/internal/errors"
)

// ───────────────────────────── PKCE 辅助函数 ─────────────────────────────

// generateCodeVerifier 生成 PKCE code_verifier。
// 长度为 43-128 字符，仅包含 [A-Z] / [a-z] / [0-9] / "-" / "." / "_" / "~"。
func generateCodeVerifier() (string, error) {
	// 使用 32 字节随机数，base64url 编码后为 43 字符
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", pkgerrors.Wrap(pkgerrors.AuthOAuth, "生成随机数失败", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// generateCodeChallenge 从 code_verifier 计算 PKCE code_challenge。
// 使用 S256 方法: BASE64URL(SHA256(code_verifier))。
func generateCodeChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// ───────────────────────────── 授权流程辅助 ─────────────────────────────

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

// ───────────────────────────── Token 持久化 ─────────────────────────────

// loadTokenFile 内部加载实现（调用者需持锁）。
func (g *GoogleOAuth) loadTokenFile() (*Token, error) {
	data, err := os.ReadFile(g.tokenFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // 文件不存在，非错误
		}
		return nil, pkgerrors.Wrap(pkgerrors.AuthOAuth, "读取 token 文件失败", err)
	}

	var token Token
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.AuthOAuth, "解析 token 文件失败", err)
	}

	return &token, nil
}

// saveTokenFile 内部保存实现（调用者需持锁）。
func (g *GoogleOAuth) saveTokenFile(token *Token) error {
	if token == nil {
		return pkgerrors.New(pkgerrors.AuthFailed, "token 为空，无法保存")
	}

	// 确保目录存在
	dir := filepath.Dir(g.tokenFile)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return pkgerrors.Wrap(pkgerrors.AuthOAuth, "创建凭证目录失败", err)
	}

	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return pkgerrors.Wrap(pkgerrors.AuthOAuth, "序列化 token 失败", err)
	}

	// 写入临时文件后重写，确保原子性
	tmpFile := g.tokenFile + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0600); err != nil {
		return pkgerrors.Wrap(pkgerrors.AuthOAuth, "写入 token 文件失败", err)
	}

	if err := os.Rename(tmpFile, g.tokenFile); err != nil {
		_ = os.Remove(tmpFile) // 清理临时文件
		return pkgerrors.Wrap(pkgerrors.AuthOAuth, "重命名 token 文件失败", err)
	}

	return nil
}

// ───────────────────────────── HTTP 请求 ─────────────────────────────

// doTokenRequest 执行 OAuth token 端点请求。
func (g *GoogleOAuth) doTokenRequest(ctx context.Context, data url.Values) (*Token, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, googleTokenEndpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.AuthOAuth, "创建 token 请求失败", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.AuthOAuth, "token 请求失败", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxOAuthResponseSize))
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.AuthOAuth, "读取 token 响应失败", err)
	}

	if resp.StatusCode == 401 {
		return nil, pkgerrors.New(pkgerrors.AuthFailed, "OAuth 认证失败 (HTTP 401): token 已失效或被撤销。请重新运行 OAuth 授权流程以获取新凭证")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, pkgerrors.New(pkgerrors.AuthOAuth, fmt.Sprintf("token 端点返回 HTTP %d", resp.StatusCode))
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
		return nil, pkgerrors.Wrap(pkgerrors.AuthOAuth, "解析 token 响应失败", err)
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
		return pkgerrors.New(pkgerrors.AuthOAuth, fmt.Sprintf("不支持的操作系统: %s", runtime.GOOS))
	}
}
