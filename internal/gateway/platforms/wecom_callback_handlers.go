package platforms

import (
	"context"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// ───────────────────────────── 回调处理 ─────────────────────────────

func (a *WeComCallbackAdapter) handleCallback(w http.ResponseWriter, r *http.Request) {
	// 处理 URL 验证 (GET 请求)
	if r.Method == http.MethodGet {
		a.handleVerification(w, r)
		return
	}

	// 处理消息 (POST 请求)
	if r.Method == http.MethodPost {
		a.handleMessage(w, r)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (a *WeComCallbackAdapter) handleVerification(w http.ResponseWriter, r *http.Request) {
	msgSignature := r.URL.Query().Get("msg_signature")
	timestamp := r.URL.Query().Get("timestamp")
	nonce := r.URL.Query().Get("nonce")
	echoStr := r.URL.Query().Get("echostr")

	// 验证签名
	expectedSignature := a.generateSignature(timestamp, nonce, echoStr)
	if subtle.ConstantTimeCompare([]byte(msgSignature), []byte(expectedSignature)) != 1 {
		http.Error(w, "签名验证失败", http.StatusForbidden)
		return
	}

	// 返回 echostr
	w.Header().Set("Content-Type", "text/plain")
	_, _ = fmt.Fprint(w, echoStr)
}

func (a *WeComCallbackAdapter) handleMessage(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, wecomCallbackMaxBodySize))
	if err != nil {
		http.Error(w, "读取请求失败", http.StatusBadRequest)
		return
	}

	// 验证 POST 回调签名 — 必须提供签名参数
	msgSignature := r.URL.Query().Get("msg_signature")
	timestamp := r.URL.Query().Get("timestamp")
	nonce := r.URL.Query().Get("nonce")
	if msgSignature == "" || timestamp == "" || nonce == "" {
		http.Error(w, "缺少签名参数", http.StatusBadRequest)
		return
	}
	expected := a.generateSignature(timestamp, nonce, string(body))
	if subtle.ConstantTimeCompare([]byte(msgSignature), []byte(expected)) != 1 {
		http.Error(w, "签名验证失败", http.StatusForbidden)
		return
	}

	var msg WeComXMLMessage
	if err := xml.Unmarshal(body, &msg); err != nil {
		http.Error(w, "解析 XML 失败", http.StatusBadRequest)
		return
	}

	// 只处理文本消息
	if msg.MsgType != "text" {
		w.Header().Set("Content-Type", "text/xml")
		_, _ = fmt.Fprint(w, `<xml></xml>`)
		return
	}

	// 发送消息到 Agent
	msgEvent := &MessageEvent{
		Text:        msg.Content,
		MessageType: MsgText,
		MessageID:   msg.MsgID,
		Source: &SessionSource{
			Platform: PlatformWeCom,
			ChatID:   a.corpID + ":" + msg.FromUserName,
			UserID:   msg.FromUserName,
			ChatType: "dm",
		},
	}

	a.mu.RLock()
	select {
	case a.msgCh <- msgEvent:
	default:
		slog.Warn("[WeComCallback] message channel full, dropping message")
	}
	a.mu.RUnlock()

	// 返回空响应（使用主动发送 API 回复）
	w.Header().Set("Content-Type", "text/xml")
	_, _ = fmt.Fprint(w, `<xml></xml>`)
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

func (a *WeComCallbackAdapter) generateSignature(timestamp, nonce, echostr string) string {
	// 排序参数
	params := []string{a.token, timestamp, nonce, echostr}
	sort.Strings(params)

	// 拼接并 SHA1
	str := strings.Join(params, "")
	hash := sha1.Sum([]byte(str))
	return fmt.Sprintf("%x", hash)
}

func (a *WeComCallbackAdapter) getAccessToken(ctx context.Context) (string, error) {
	a.tokenMu.Lock()
	if a.accessToken != "" && time.Now().Before(a.tokenExpiry) {
		token := a.accessToken
		a.tokenMu.Unlock()
		return token, nil
	}
	a.tokenMu.Unlock()

	secret := a.corpSecret
	if secret == "" {
		return "", fmt.Errorf("未配置 corp_secret (WECOM_CORP_SECRET)")
	}

	u, _ := url.Parse("https://qyapi.weixin.qq.com/cgi-bin/gettoken")
	q := u.Query()
	q.Set("corpid", a.corpID)
	q.Set("corpsecret", secret)
	u.RawQuery = q.Encode()
	apiURL := u.String()

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return "", err
	}

	if result.ErrCode != 0 {
		return "", fmt.Errorf("企业微信 API 错误 %d: %s", result.ErrCode, result.ErrMsg)
	}

	if result.AccessToken == "" {
		return "", fmt.Errorf("无法获取 access_token")
	}

	a.tokenMu.Lock()
	a.accessToken = result.AccessToken
	buffer := 60
	if result.ExpiresIn < buffer {
		buffer = result.ExpiresIn / 2
	}
	a.tokenExpiry = time.Now().Add(time.Duration(result.ExpiresIn-buffer) * time.Second)
	token := a.accessToken
	a.tokenMu.Unlock()

	return token, nil
}
