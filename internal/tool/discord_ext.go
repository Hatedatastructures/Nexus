// Package tool 提供 Discord 扩展功能工具。
// 通过 REST API 实现 Discord 服务器管理操作。
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"time"
)

// ───────────────────────────── 常量 ─────────────────────────────

const (
	discordAPIBaseURL     = "https://discord.com/api/v10"
	discordRequestTimeout = 15 * time.Second
	discordMaxResultChars = 50000
	discordMaxLimit       = 100
)

// discordIDRe 验证 Discord Snowflake ID 格式 (纯数字)。
var discordIDRe = regexp.MustCompile(`^\d+$`)

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
	token      string
	apiURL     string
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
		token:      token,
		apiURL:     apiURL,
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

// pinMessage 置顶消息。
func (t *DiscordExtTool) pinMessage(ctx context.Context, args map[string]any) (string, error) {
	channelID := getString(args, "channel_id", "")
	messageID := getString(args, "message_id", "")

	if err := validateDiscordIDs("channel_id", channelID, "message_id", messageID); err != nil {
		return "", err
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

	if err := validateDiscordIDs("channel_id", channelID, "message_id", messageID); err != nil {
		return "", err
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

// addRole 添加角色。
func (t *DiscordExtTool) addRole(ctx context.Context, args map[string]any) (string, error) {
	guildID := getString(args, "guild_id", "")
	userID := getString(args, "user_id", "")
	roleID := getString(args, "role_id", "")

	if err := validateDiscordIDs("guild_id", guildID, "user_id", userID, "role_id", roleID); err != nil {
		return "", err
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

	if err := validateDiscordIDs("guild_id", guildID, "user_id", userID, "role_id", roleID); err != nil {
		return "", err
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


// ───────────────────────────── 辅助函数 ─────────────────────────────

// validateDiscordID 验证 Discord Snowflake ID 格式。
func validateDiscordID(name, id string) error {
	if id == "" {
		return fmt.Errorf("%s 参数是必填项", name)
	}
	if !discordIDRe.MatchString(id) {
		return fmt.Errorf("%s 格式无效: 必须为纯数字 Snowflake ID", name)
	}
	return nil
}

// validateDiscordIDs 批量验证 Discord ID。
func validateDiscordIDs(pairs ...string) error {
	for i := 0; i < len(pairs); i += 2 {
		if err := validateDiscordID(pairs[i], pairs[i+1]); err != nil {
			return err
		}
	}
	return nil
}

// clampLimit 将 limit 限制在 [1, max] 范围内。
func clampLimit(limit, maxVal int) int {
	if limit < 1 {
		return 1
	}
	if limit > maxVal {
		return maxVal
	}
	return limit
}

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
