package platforms

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// ───────────────────────────── 钉钉 API 请求与签名 ─────────────────────────────

// doAPI 发送钉钉 API 请求。
func (d *DingTalkAdapter) doAPI(ctx context.Context, method string, path string, body any) (*SendResult, error) {
	token, err := d.getAccessToken(ctx)
	if err != nil {
		return &SendResult{Success: false, Error: "failed to get token: " + err.Error()}, err
	}

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return &SendResult{Success: false, Error: err.Error()}, err
		}
		bodyReader = bytes.NewReader(data)
	}

	// 添加钉钉 API 要求的请求头
	timestamp := fmt.Sprintf("%d", time.Now().UnixMilli())
	sign := d.calcSign(timestamp)

	req, err := http.NewRequestWithContext(ctx, method, "https://api.dingtalk.com"+path, bodyReader)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, err
	}
	req.Header.Set("x-acs-dingtalk-access-token", token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-acs-dingtalk-sdk-version", "1.0.0")
	req.Header.Set("x-acs-timestamp", timestamp)
	req.Header.Set("x-acs-signature", sign)

	resp, err := d.client.Do(req)
	if err != nil {
		return &SendResult{Success: false, Error: err.Error(), Retryable: true}, err
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseSize))
	if err != nil {
		return &SendResult{Success: false, Error: err.Error()}, err
	}

	var result struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		MsgID   string `json:"processQueryKey"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return &SendResult{Success: false, Error: err.Error()}, err
	}

	if result.Code != "" && result.Code != "0" {
		return &SendResult{
			Success:   false,
			Error:     fmt.Sprintf("dingtalk error %s: %s", result.Code, result.Message),
			Retryable: result.Code == "401" || result.Code == "90018",
		}, nil
	}

	return &SendResult{Success: true, MessageID: result.MsgID}, nil
}

// getAccessToken 获取 access_token (带缓存)。
func (d *DingTalkAdapter) getAccessToken(ctx context.Context) (string, error) {
	d.tokenMu.Lock()
	if d.accessToken != "" && time.Now().Before(d.tokenExpiry) {
		token := d.accessToken
		d.tokenMu.Unlock()
		return token, nil
	}
	d.tokenMu.Unlock()

	// HTTP 调用在锁外执行，避免阻塞其他 API 调用
	payload := map[string]string{
		"appKey":    d.appKey,
		"appSecret": d.appSecret,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("序列化请求失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://api.dingtalk.com/v1.0/oauth2/accessToken", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponseSize))
	if err != nil {
		return "", err
	}

	var result struct {
		AccessToken string `json:"accessToken"`
		ExpireIn    int    `json:"expireIn"` // 秒
		Code        string `json:"code"`
		Message     string `json:"message"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", err
	}
	if result.Code != "" && result.Code != "0" {
		slog.Warn("[DingTalk] get access_token failed", "code", result.Code, "message", result.Message)
		return "", fmt.Errorf("get access_token failed (code=%s)", result.Code)
	}

	d.tokenMu.Lock()
	d.accessToken = result.AccessToken
	buffer := 60
	if result.ExpireIn < buffer {
		buffer = result.ExpireIn / 2
	}
	d.tokenExpiry = time.Now().Add(time.Duration(result.ExpireIn-buffer) * time.Second)
	token := d.accessToken
	d.tokenMu.Unlock()

	return token, nil
}

// calcSign 计算钉钉 API 请求签名。
func (d *DingTalkAdapter) calcSign(timestamp string) string {
	h := hmac.New(sha256.New, []byte(d.appSecret))
	h.Write([]byte(timestamp + "\n" + d.appSecret))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// escapeJSONDing 为钉钉消息 JSON 转义内容。
func escapeJSONDing(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	for _, c := range s {
		switch c {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		case '\b':
			sb.WriteString(`\b`)
		case '\f':
			sb.WriteString(`\f`)
		default:
			if c < 0x20 {
				fmt.Fprintf(&sb, `\u%04x`, c)
			} else {
				sb.WriteRune(c)
			}
		}
	}
	return sb.String()
}
