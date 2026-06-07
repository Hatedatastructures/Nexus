// Package tool 提供 Discord 扩展功能工具。
// 本文件包含 Discord 工具的 API 调用和核心操作。
package tool

import (
	"context"
	"fmt"
	"net/url"
)

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
	if err := validateDiscordID("guild_id", guildID); err != nil {
		return "", err
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
	if err := validateDiscordID("guild_id", guildID); err != nil {
		return "", err
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
	if err := validateDiscordID("channel_id", channelID); err != nil {
		return "", err
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
	if err := validateDiscordID("guild_id", guildID); err != nil {
		return "", err
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

	if err := validateDiscordIDs("guild_id", guildID, "user_id", userID); err != nil {
		return "", err
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
	limit := clampLimit(getInt(args, "limit", 10), discordMaxLimit)

	if err := validateDiscordID("guild_id", guildID); err != nil {
		return "", err
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

// jsonResult is defined in discord_ext.go
