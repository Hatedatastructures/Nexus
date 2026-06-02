// Package tool 提供 Discord 扩展功能工具。
// 通过 REST API 实现 Discord 服务器管理操作。
package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

// ───────────────────────────── 常量 ─────────────────────────────

const (
	discordAPIBaseURL    = "https://discord.com/api/v10"
	discordRequestTimeout = 15 * time.Second
	discordMaxResultChars = 50000
)

// 频道类型映射
var discordChannelTypeNames = map[int]string{
	0:  "text",
	1:  "dm",
	2:  "voice",
	3:  "group_dm",
	4:  "category",
	5:  "announcement",
	10: "announcement_thread",
	11: "public_thread",
	12: "private_thread",
	13: "stage_voice",
	14: "directory",
	15: "forum",
}

// Discord 工具动作
var discordActions = []string{
	"list_guilds",
	"server_info",
	"list_channels",
	"channel_info",
	"list_roles",
	"member_info",
	"search_members",
	"fetch_messages",
	"list_pins",
	"pin_message",
	"unpin_message",
	"create_thread",
	"add_role",
	"remove_role",
}

// ───────────────────────────── DiscordExtTool ─────────────────────────────

// DiscordExtTool Discord 扩展功能工具。
type DiscordExtTool struct {
	token     string
	apiURL    string
	httpClient *http.Client
}

// NewDiscordExtTool 创建 Discord 扩展工具。
func NewDiscordExtTool() *DiscordExtTool {
	token := os.Getenv("DISCORD_BOT_TOKEN")

	apiURL := os.Getenv("DISCORD_API_URL")
	if apiURL == "" {
		apiURL = discordAPIBaseURL
	}

	return &DiscordExtTool{
		token:     token,
		apiURL:    apiURL,
		httpClient: &http.Client{Timeout: discordRequestTimeout},
	}
}

// ───────────────────────────── 工具接口 ─────────────────────────────

// Name 返回工具名称。
func (t *DiscordExtTool) Name() string { return "discord_ext" }

// Description 返回工具描述。
func (t *DiscordExtTool) Description() string {
	return "Discord 服务器管理功能。支持频道管理、角色管理、成员查询、消息操作。"
}

// Schema 返回工具 Schema。
func (t *DiscordExtTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"description": "操作类型",
					"enum":        discordActions,
				},
				"guild_id": map[string]any{
					"type":        "string",
					"description": "服务器 ID",
				},
				"channel_id": map[string]any{
					"type":        "string",
					"description": "频道 ID",
				},
				"user_id": map[string]any{
					"type":        "string",
					"description": "用户 ID",
				},
				"role_id": map[string]any{
					"type":        "string",
					"description": "角色 ID",
				},
				"message_id": map[string]any{
					"type":        "string",
					"description": "消息 ID",
				},
				"query": map[string]any{
					"type":        "string",
					"description": "搜索关键词",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "返回数量限制",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "频道/线程名称",
				},
			},
			"required": []string{"action"},
		},
	}
}

// Toolset 返回工具集名称。
func (t *DiscordExtTool) Toolset() string { return "discord" }

// IsAvailable 检查工具是否可用。
func (t *DiscordExtTool) IsAvailable() bool {
	return os.Getenv("DISCORD_BOT_TOKEN") != ""
}

// Emoji 返回工具图标。
func (t *DiscordExtTool) Emoji() string { return "🤖" }

// MaxResultChars 返回最大结果字符数。
func (t *DiscordExtTool) MaxResultChars() int { return discordMaxResultChars }

// Execute 执行 Discord 工具。
func (t *DiscordExtTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	if t.token == "" {
		return "", fmt.Errorf("DISCORD_BOT_TOKEN 未配置")
	}

	action := getString(args, "action", "")
	if action == "" {
		return "", fmt.Errorf("action 参数是必填项")
	}

	switch action {
	case "list_guilds":
		return t.listGuilds(ctx)
	case "server_info":
		return t.serverInfo(ctx, args)
	case "list_channels":
		return t.listChannels(ctx, args)
	case "channel_info":
		return t.channelInfo(ctx, args)
	case "list_roles":
		return t.listRoles(ctx, args)
	case "member_info":
		return t.memberInfo(ctx, args)
	case "search_members":
		return t.searchMembers(ctx, args)
	case "fetch_messages":
		return t.fetchMessages(ctx, args)
	case "list_pins":
		return t.listPins(ctx, args)
	case "pin_message":
		return t.pinMessage(ctx, args)
	case "unpin_message":
		return t.unpinMessage(ctx, args)
	case "create_thread":
		return t.createThread(ctx, args)
	case "add_role":
		return t.addRole(ctx, args)
	case "remove_role":
		return t.removeRole(ctx, args)
	default:
		return "", fmt.Errorf("未知操作: %s", action)
	}
}

// ───────────────────────────── 操作实现 ─────────────────────────────

// listGuilds 列出服务器。
func (t *DiscordExtTool) listGuilds(ctx context.Context) (string, error) {
	resp, err := t.callAPI(ctx, "GET", "/users/@me/guilds", nil)
	if err != nil {
		return "", err
	}

	guilds, ok := resp["guilds"].([]any)
	if !ok {
		// resp 可能直接是数组
		if respList, ok := resp["raw"].([]any); ok {
			guilds = respList
		} else {
			guilds = []any{}
		}
	}

	var simplified []map[string]any
	for _, guild := range guilds {
		if guildMap, ok := guild.(map[string]any); ok {
			simplified = append(simplified, map[string]any{
				"id":   getString(guildMap, "id", ""),
				"name": getString(guildMap, "name", ""),
			})
		}
	}

	return jsonResult(map[string]any{
		"success": true,
		"count":   len(simplified),
		"guilds":  simplified,
	})
}

// serverInfo 获取服务器信息。
func (t *DiscordExtTool) serverInfo(ctx context.Context, args map[string]any) (string, error) {
	guildID := getString(args, "guild_id", "")
	if guildID == "" {
		return "", fmt.Errorf("guild_id 参数是必填项")
	}

	resp, err := t.callAPI(ctx, "GET", "/guilds/"+guildID, nil)
	if err != nil {
		return "", err
	}

	return jsonResult(map[string]any{
		"success": true,
		"guild":   resp,
	})
}

// listChannels 列出频道。
func (t *DiscordExtTool) listChannels(ctx context.Context, args map[string]any) (string, error) {
	guildID := getString(args, "guild_id", "")
	if guildID == "" {
		return "", fmt.Errorf("guild_id 参数是必填项")
	}

	resp, err := t.callAPI(ctx, "GET", "/guilds/"+guildID+"/channels", nil)
	if err != nil {
		return "", err
	}

	// 从 resp 中提取数组
	var channels []any
	if rawList, ok := resp["raw"].([]any); ok {
		channels = rawList
	} else {
		channels = getListAnyFromMap(resp, "channels")
	}

	var simplified []map[string]any
	for _, channel := range channels {
		if channelMap, ok := channel.(map[string]any); ok {
			channelType := getInt(channelMap, "type", 0)
			typeName := discordChannelTypeNames[channelType]
			if typeName == "" {
				typeName = "unknown"
			}

			simplified = append(simplified, map[string]any{
				"id":        getString(channelMap, "id", ""),
				"name":      getString(channelMap, "name", ""),
				"type":      typeName,
				"position":  getInt(channelMap, "position", 0),
				"parent_id": getString(channelMap, "parent_id", ""),
			})
		}
	}

	return jsonResult(map[string]any{
		"success":  true,
		"count":    len(simplified),
		"channels": simplified,
	})
}

// channelInfo 获取频道信息。
func (t *DiscordExtTool) channelInfo(ctx context.Context, args map[string]any) (string, error) {
	channelID := getString(args, "channel_id", "")
	if channelID == "" {
		return "", fmt.Errorf("channel_id 参数是必填项")
	}

	resp, err := t.callAPI(ctx, "GET", "/channels/"+channelID, nil)
	if err != nil {
		return "", err
	}

	return jsonResult(map[string]any{
		"success": true,
		"channel": resp,
	})
}

// listRoles 列出角色。
func (t *DiscordExtTool) listRoles(ctx context.Context, args map[string]any) (string, error) {
	guildID := getString(args, "guild_id", "")
	if guildID == "" {
		return "", fmt.Errorf("guild_id 参数是必填项")
	}

	resp, err := t.callAPI(ctx, "GET", "/guilds/"+guildID+"/roles", nil)
	if err != nil {
		return "", err
	}

	// 从 resp 中提取数组
	var roles []any
	if rawList, ok := resp["raw"].([]any); ok {
		roles = rawList
	} else {
		roles = getListAnyFromMap(resp, "roles")
	}

	var simplified []map[string]any
	for _, role := range roles {
		if roleMap, ok := role.(map[string]any); ok {
			simplified = append(simplified, map[string]any{
				"id":       getString(roleMap, "id", ""),
				"name":     getString(roleMap, "name", ""),
				"color":    getInt(roleMap, "color", 0),
				"position": getInt(roleMap, "position", 0),
			})
		}
	}

	return jsonResult(map[string]any{
		"success": true,
		"count":   len(simplified),
		"roles":   simplified,
	})
}

// memberInfo 获取成员信息。
func (t *DiscordExtTool) memberInfo(ctx context.Context, args map[string]any) (string, error) {
	guildID := getString(args, "guild_id", "")
	userID := getString(args, "user_id", "")

	if guildID == "" || userID == "" {
		return "", fmt.Errorf("guild_id 和 user_id 参数是必填项")
	}

	resp, err := t.callAPI(ctx, "GET", "/guilds/"+guildID+"/members/"+userID, nil)
	if err != nil {
		return "", err
	}

	return jsonResult(map[string]any{
		"success": true,
		"member":  resp,
	})
}

// searchMembers 搜索成员。
func (t *DiscordExtTool) searchMembers(ctx context.Context, args map[string]any) (string, error) {
	guildID := getString(args, "guild_id", "")
	query := getString(args, "query", "")
	limit := getInt(args, "limit", 10)

	if guildID == "" {
		return "", fmt.Errorf("guild_id 参数是必填项")
	}

	if query == "" {
		return "", fmt.Errorf("query 参数是必填项")
	}

	endpoint := fmt.Sprintf("/guilds/%s/members/search?query=%s&limit=%d",
		url.PathEscape(guildID), url.QueryEscape(query), limit)

	resp, err := t.callAPI(ctx, "GET", endpoint, nil)
	if err != nil {
		return "", err
	}

	// 从 resp 中提取数组
	var members []any
	if rawList, ok := resp["raw"].([]any); ok {
		members = rawList
	} else {
		members = getListAnyFromMap(resp, "members")
	}

	var simplified []map[string]any
	for _, member := range members {
		if memberMap, ok := member.(map[string]any); ok {
			user := getMap(memberMap, "user")
			simplified = append(simplified, map[string]any{
				"user_id":  getString(user, "id", ""),
				"username": getString(user, "username", ""),
				"nickname": getString(memberMap, "nick", ""),
			})
		}
	}

	return jsonResult(map[string]any{
		"success": true,
		"count":   len(simplified),
		"members": simplified,
	})
}

// fetchMessages 获取消息。
func (t *DiscordExtTool) fetchMessages(ctx context.Context, args map[string]any) (string, error) {
	channelID := getString(args, "channel_id", "")
	limit := getInt(args, "limit", 50)

	if channelID == "" {
		return "", fmt.Errorf("channel_id 参数是必填项")
	}

	endpoint := fmt.Sprintf("/channels/%s/messages?limit=%d", channelID, limit)

	resp, err := t.callAPI(ctx, "GET", endpoint, nil)
	if err != nil {
		return "", err
	}

	// 从 resp 中提取数组
	var messages []any
	if rawList, ok := resp["raw"].([]any); ok {
		messages = rawList
	} else {
		messages = getListAnyFromMap(resp, "messages")
	}

	var simplified []map[string]any
	for _, msg := range messages {
		if msgMap, ok := msg.(map[string]any); ok {
			author := getMap(msgMap, "author")
			simplified = append(simplified, map[string]any{
				"id":        getString(msgMap, "id", ""),
				"content":   getString(msgMap, "content", ""),
				"author_id": getString(author, "id", ""),
				"author":    getString(author, "username", ""),
				"timestamp": getString(msgMap, "timestamp", ""),
			})
		}
	}

	return jsonResult(map[string]any{
		"success":  true,
		"count":    len(simplified),
		"messages": simplified,
	})
}

// listPins 列出置顶消息。
func (t *DiscordExtTool) listPins(ctx context.Context, args map[string]any) (string, error) {
	channelID := getString(args, "channel_id", "")
	if channelID == "" {
		return "", fmt.Errorf("channel_id 参数是必填项")
	}

	resp, err := t.callAPI(ctx, "GET", "/channels/"+channelID+"/pins", nil)
	if err != nil {
		return "", err
	}

	// 从 resp 中提取数组
	var pins []any
	if rawList, ok := resp["raw"].([]any); ok {
		pins = rawList
	} else {
		pins = getListAnyFromMap(resp, "pins")
	}

	return jsonResult(map[string]any{
		"success": true,
		"count":   len(pins),
		"pins":    pins,
	})
}

// pinMessage 置顶消息。
func (t *DiscordExtTool) pinMessage(ctx context.Context, args map[string]any) (string, error) {
	channelID := getString(args, "channel_id", "")
	messageID := getString(args, "message_id", "")

	if channelID == "" || messageID == "" {
		return "", fmt.Errorf("channel_id 和 message_id 参数是必填项")
	}

	_, err := t.callAPI(ctx, "PUT", "/channels/"+channelID+"/pins/"+messageID, nil)
	if err != nil {
		return "", err
	}

	return jsonResult(map[string]any{
		"success": true,
		"message": "消息已置顶",
	})
}

// unpinMessage 取消置顶。
func (t *DiscordExtTool) unpinMessage(ctx context.Context, args map[string]any) (string, error) {
	channelID := getString(args, "channel_id", "")
	messageID := getString(args, "message_id", "")

	if channelID == "" || messageID == "" {
		return "", fmt.Errorf("channel_id 和 message_id 参数是必填项")
	}

	_, err := t.callAPI(ctx, "DELETE", "/channels/"+channelID+"/pins/"+messageID, nil)
	if err != nil {
		return "", err
	}

	return jsonResult(map[string]any{
		"success": true,
		"message": "消息已取消置顶",
	})
}

// createThread 创建线程。
func (t *DiscordExtTool) createThread(ctx context.Context, args map[string]any) (string, error) {
	channelID := getString(args, "channel_id", "")
	name := getString(args, "name", "")
	messageID := getString(args, "message_id", "")

	if channelID == "" || name == "" {
		return "", fmt.Errorf("channel_id 和 name 参数是必填项")
	}

	var endpoint string
	var body map[string]any

	if messageID != "" {
		// 从消息创建线程
		endpoint = "/channels/" + channelID + "/messages/" + messageID + "/threads"
		body = map[string]any{"name": name}
	} else {
		// 创建新线程
		endpoint = "/channels/" + channelID + "/threads"
		body = map[string]any{
			"name": name,
			"type": 11, // public_thread
		}
	}

	resp, err := t.callAPI(ctx, "POST", endpoint, body)
	if err != nil {
		return "", err
	}

	return jsonResult(map[string]any{
		"success": true,
		"thread":  resp,
	})
}

// addRole 添加角色。
func (t *DiscordExtTool) addRole(ctx context.Context, args map[string]any) (string, error) {
	guildID := getString(args, "guild_id", "")
	userID := getString(args, "user_id", "")
	roleID := getString(args, "role_id", "")

	if guildID == "" || userID == "" || roleID == "" {
		return "", fmt.Errorf("guild_id, user_id 和 role_id 参数是必填项")
	}

	_, err := t.callAPI(ctx, "PUT", "/guilds/"+guildID+"/members/"+userID+"/roles/"+roleID, nil)
	if err != nil {
		return "", err
	}

	return jsonResult(map[string]any{
		"success": true,
		"message": "角色已添加",
	})
}

// removeRole 移除角色。
func (t *DiscordExtTool) removeRole(ctx context.Context, args map[string]any) (string, error) {
	guildID := getString(args, "guild_id", "")
	userID := getString(args, "user_id", "")
	roleID := getString(args, "role_id", "")

	if guildID == "" || userID == "" || roleID == "" {
		return "", fmt.Errorf("guild_id, user_id 和 role_id 参数是必填项")
	}

	_, err := t.callAPI(ctx, "DELETE", "/guilds/"+guildID+"/members/"+userID+"/roles/"+roleID, nil)
	if err != nil {
		return "", err
	}

	return jsonResult(map[string]any{
		"success": true,
		"message": "角色已移除",
	})
}

// ───────────────────────────── API 调用 ─────────────────────────────

// callAPI 调用 Discord REST API。
func (t *DiscordExtTool) callAPI(ctx context.Context, method string, endpoint string, body map[string]any) (map[string]any, error) {
	url := t.apiURL + endpoint

	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("序列化请求体失败: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bot "+t.token)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == 403 {
		return nil, fmt.Errorf("权限不足 (HTTP 403)")
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("API 错误 (HTTP %d)", resp.StatusCode)
	}

	// 尝试解析为 JSON
	if len(respBody) == 0 {
		return map[string]any{"success": true}, nil
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return map[string]any{"raw": string(respBody)}, nil
	}

	return result, nil
}

// ───────────────────────────── 注册工具 ─────────────────────────────

func init() {
	GetRegistry().Register(NewDiscordExtTool())
}

// ───────────────────────────── 辅助函数 ─────────────────────────────

func getInt(m map[string]any, key string, defaultVal int) int {
	if m == nil {
		return defaultVal
	}
	val, ok := m[key]
	if !ok {
		return defaultVal
	}
	switch v := val.(type) {
	case int:
		return v
	case float64:
		return int(v)
	default:
		return defaultVal
	}
}

func getString(m map[string]any, key string, defaultVal string) string {
	if m == nil {
		return defaultVal
	}
	val, ok := m[key]
	if !ok {
		return defaultVal
	}
	str, ok := val.(string)
	if !ok {
		return defaultVal
	}
	return str
}

func getMap(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	val, ok := m[key]
	if !ok {
		return nil
	}
	mapVal, ok := val.(map[string]any)
	if !ok {
		return nil
	}
	return mapVal
}

func getListAnyFromMap(m map[string]any, key string) []any {
	if m == nil {
		return nil
	}
	val, ok := m[key]
	if !ok {
		return nil
	}
	list, ok := val.([]any)
	if !ok {
		return nil
	}
	return list
}

func jsonResult(data map[string]any) (string, error) {
	bytes, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}