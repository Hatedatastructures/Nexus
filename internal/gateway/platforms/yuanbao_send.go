package platforms

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// ───────────────────────────── 辅助函数 ─────────────────────────────

// getSignToken 获取认证 token。
func (a *YuanbaoAdapter) getSignToken(ctx context.Context) (string, string, error) {
	body := map[string]any{
		"app_id":      a.appID,
		"app_secret":  a.appSecret,
		"instance_id": a.instanceID,
	}

	resp, err := a.callAPI(ctx, "/api/sign-token", body)
	if err != nil {
		return "", "", err
	}

	errcode := getInt(resp, "errcode", 0)
	if errcode != 0 {
		errmsg := getString(resp, "errmsg", "认证失败")
		return "", "", fmt.Errorf("认证失败: %s (errcode=%d)", errmsg, errcode)
	}

	token := getString(resp, "token", "")
	botID := getString(resp, "bot_id", "")

	if token == "" {
		return "", "", fmt.Errorf("token 未返回")
	}

	return token, botID, nil
}

// heartbeatLoop 发送心跳。
func (a *YuanbaoAdapter) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(yuanbaoHeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.mu.Lock()
			running := a.running
			conn := a.conn
			a.mu.Unlock()

			if !running || conn == nil {
				return
			}

			pingReq := map[string]any{
				"cmd":    yuanbaoCmdPing,
				"seq_no": a.nextSeqNo(),
			}

			a.writeMu.Lock()
			if err := conn.WriteJSON(pingReq); err != nil {
				slog.Debug("[Yuanbao] heartbeat send failed", "err", err)
			}
			a.writeMu.Unlock()
		}
	}
}

func (a *YuanbaoAdapter) nextSeqNo() int64 {
	a.mu.Lock()
	a.seqNo++
	seq := a.seqNo
	a.mu.Unlock()
	return seq
}

// generateCryptoID 生成加密安全的随机 ID。
func generateCryptoID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", b)
}

// callAPI 调用 HTTP API (复用 httpClient)。
func (a *YuanbaoAdapter) callAPI(ctx context.Context, endpoint string, body map[string]any) (map[string]any, error) {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化请求体失败: %w", err)
	}

	url := a.apiDomain + endpoint
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.appID+":"+a.appSecret)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("API 错误 (HTTP %d)", resp.StatusCode)
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	return result, nil
}

// ───────────────────────────── 发送消息 ─────────────────────────────

// Send 发送文本消息。
func (a *YuanbaoAdapter) Send(ctx context.Context, chatID string, content string, opts *SendOptions) (*SendResult, error) {
	if chatID == "" {
		return &SendResult{Success: false, Error: "chat_id 是必填项"}, nil
	}

	a.mu.Lock()
	conn := a.conn
	a.mu.Unlock()

	if conn == nil {
		return &SendResult{Success: false, Error: "WebSocket 未连接"}, nil
	}

	seqNo := a.nextSeqNo()
	chatType := "c2c"
	if strings.HasPrefix(chatID, "group:") {
		chatType = "group"
		chatID = strings.TrimPrefix(chatID, "group:")
	}

	req := map[string]any{
		"cmd":       yuanbaoCmdT06,
		"seq_no":    seqNo,
		"chat_id":   chatID,
		"chat_type": chatType,
		"msg_type":  "text",
		"text": map[string]any{
			"content": content,
		},
	}

	// 注册 pending response
	respCh := make(chan map[string]any, 1)
	seqNoStr := fmt.Sprintf("%d", seqNo)
	a.mu.Lock()
	if len(a.pendingResps) > yuanbaoPendingMaxSize {
		for k := range a.pendingResps {
			delete(a.pendingResps, k)
			if len(a.pendingResps) <= yuanbaoPendingMaxSize/2 {
				break
			}
		}
	}
	a.pendingResps[seqNoStr] = respCh
	a.mu.Unlock()

	a.writeMu.Lock()
	if err := conn.WriteJSON(req); err != nil {
		a.writeMu.Unlock()
		a.mu.Lock()
		delete(a.pendingResps, seqNoStr)
		a.mu.Unlock()
		return &SendResult{Success: false, Error: fmt.Sprintf("发送失败: %v", err)}, nil
	}
	a.writeMu.Unlock()

	select {
	case resp := <-respCh:
		errcode := getInt(resp, "errcode", 0)
		if errcode != 0 {
			errmsg := getString(resp, "errmsg", "发送失败")
			return &SendResult{Success: false, Error: fmt.Sprintf("%s (errcode=%d)", errmsg, errcode)}, nil
		}
		msgID := getString(resp, "msg_id", "")
		return &SendResult{Success: true, MessageID: msgID}, nil

	case <-time.After(yuanbaoSendTimeout):
		a.mu.Lock()
		delete(a.pendingResps, seqNoStr)
		a.mu.Unlock()
		return &SendResult{Success: false, Error: "发送超时"}, nil
	}
}

// SendImage 发送图片。
func (a *YuanbaoAdapter) SendImage(ctx context.Context, chatID string, imageURL string, caption string, opts *SendOptions) (*SendResult, error) {
	text := caption
	if text == "" {
		text = imageURL
	} else {
		text = text + "\n" + imageURL
	}
	return a.Send(ctx, chatID, text, opts)
}

// SendTyping 发送正在输入指示。
func (a *YuanbaoAdapter) SendTyping(ctx context.Context, chatID string) error {
	return nil
}

// ───────────────────────────── 群操作 ─────────────────────────────

// QueryGroupInfo 查询群信息。
func (a *YuanbaoAdapter) QueryGroupInfo(ctx context.Context, groupCode string) (*YuanbaoGroupInfo, error) {
	// 检查缓存
	a.mu.Lock()
	cached, exists := a.groupInfoCache[groupCode]
	if exists && time.Now().Before(cached.expiresAt) {
		info := cached.info
		a.mu.Unlock()
		return info, nil
	}
	a.mu.Unlock()

	// 调用 API
	body := map[string]any{
		"group_code": groupCode,
	}

	resp, err := a.callAPI(ctx, "/api/query-group-info", body)
	if err != nil {
		return nil, err
	}

	errcode := getInt(resp, "errcode", 0)
	if errcode != 0 {
		return nil, fmt.Errorf("查询失败 (errcode=%d)", errcode)
	}

	info := &YuanbaoGroupInfo{
		GroupCode:   groupCode,
		GroupName:   getString(resp, "group_name", ""),
		MemberCount: getInt(resp, "member_count", 0),
		OwnerID:     getString(resp, "owner_id", ""),
	}

	// 缓存 (带 TTL + 有界驱逐)
	a.mu.Lock()
	if len(a.groupInfoCache) > yuanbaoCacheMaxSize {
		for k, v := range a.groupInfoCache {
			if time.Now().After(v.expiresAt) {
				delete(a.groupInfoCache, k)
			}
		}
	}
	a.groupInfoCache[groupCode] = &yuanbaoCacheEntry{
		info:      info,
		expiresAt: time.Now().Add(10 * time.Minute),
	}
	a.mu.Unlock()

	return info, nil
}

// GetGroupMemberList 获取群成员列表。
func (a *YuanbaoAdapter) GetGroupMemberList(ctx context.Context, groupCode string) ([]YuanbaoMember, error) {
	body := map[string]any{
		"group_code": groupCode,
	}

	resp, err := a.callAPI(ctx, "/api/get-group-member-list", body)
	if err != nil {
		return nil, err
	}

	errcode := getInt(resp, "errcode", 0)
	if errcode != 0 {
		return nil, fmt.Errorf("查询失败 (errcode=%d)", errcode)
	}

	membersRaw := getListAny(resp, "members")
	var members []YuanbaoMember
	for _, m := range membersRaw {
		if mMap, ok := m.(map[string]any); ok {
			members = append(members, YuanbaoMember{
				UserID:   getString(mMap, "user_id", ""),
				Nickname: getString(mMap, "nickname", getString(mMap, "nick_name", "")),
				UserType: getInt(mMap, "user_type", getInt(mMap, "role", 0)),
			})
		}
	}

	return members, nil
}

// ───────────────────────────── 接口实现 ─────────────────────────────

func (a *YuanbaoAdapter) Name() string            { return "Yuanbao" }
func (a *YuanbaoAdapter) PlatformType() Platform  { return PlatformYuanbao }
func (a *YuanbaoAdapter) MaxMessageLength() int   { return 4000 }
func (a *YuanbaoAdapter) SupportsStreaming() bool { return true }

func (a *YuanbaoAdapter) EditMessage(ctx context.Context, chatID string, messageID string, content string) (*SendResult, error) {
	return &SendResult{Success: false, Error: "元宝不支持编辑消息"}, nil
}

func (a *YuanbaoAdapter) DeleteMessage(ctx context.Context, chatID string, messageID string) error {
	return fmt.Errorf("元宝不支持删除消息")
}

func (a *YuanbaoAdapter) SendVoice(ctx context.Context, chatID string, audioPath string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "元宝语音发送需要媒体上传"}, nil
}

func (a *YuanbaoAdapter) SendVideo(ctx context.Context, chatID string, videoPath string, caption string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "元宝视频发送需要媒体上传"}, nil
}

func (a *YuanbaoAdapter) SendDocument(ctx context.Context, chatID string, filePath string, caption string, opts *SendOptions) (*SendResult, error) {
	return &SendResult{Success: false, Error: "元宝文件发送需要媒体上传"}, nil
}
