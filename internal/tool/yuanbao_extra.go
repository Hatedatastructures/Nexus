// Package tool 提供元宝平台工具集 — 贴纸搜索/发送、私聊发送、群成员查询工具实现。
package tool

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// ───────────────────────────── 群成员查询工具 ─────────────────────────────

// YBQueryGroupMembersTool 查询群成员。
type YBQueryGroupMembersTool struct{}

func (t *YBQueryGroupMembersTool) Name() string        { return "yb_query_group_members" }
func (t *YBQueryGroupMembersTool) Toolset() string     { return "hermes-yuanbao" }
func (t *YBQueryGroupMembersTool) Emoji() string       { return "📋" }
func (t *YBQueryGroupMembersTool) MaxResultChars() int { return 10000 }

func (t *YBQueryGroupMembersTool) Description() string {
	return `查询群成员。用于 @提及某人、按名称查找用户、列出机器人（含元宝 AI）或列出全部成员。

重要：在 @提及任何用户之前必须调用此工具，因为需要准确的昵称来构建 @提及格式。

To @mention a user, you MUST use the format: space + @ + nickname + space (e.g. " @Alice ").`
}

func (t *YBQueryGroupMembersTool) IsAvailable() bool {
	return GetYuanbaoAdapter() != nil || os.Getenv("HERMES_SESSION_PLATFORM") == "yuanbao"
}

func (t *YBQueryGroupMembersTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "yb_query_group_members",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"group_code": map[string]any{
					"type":        "string",
					"description": "群唯一标识 (group_code)。",
				},
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"find", "list_bots", "list_all"},
					"description": "find — 按名称搜索用户；list_bots — 列出机器人；list_all — 列出全部成员。",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "要搜索的用户名称（部分匹配，不区分大小写）。find 操作必需。",
				},
				"mention": map[string]any{
					"type":        "boolean",
					"description": "设为 true 时响应包含 @提及格式提示。",
				},
			},
			"required": []string{"group_code", "action"},
		},
	}
}

func (t *YBQueryGroupMembersTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	groupCode, ok := args["group_code"].(string)
	if !ok || groupCode == "" {
		return ToolError("参数 group_code 是必填项。"), nil
	}

	action, _ := args["action"].(string)
	if action == "" {
		action = "list_all"
	}

	name, _ := args["name"].(string)
	mention, _ := args["mention"].(bool)

	adapter := GetYuanbaoAdapter()
	if adapter == nil {
		return ToolError("元宝适配器未连接。"), nil
	}

	memberList, err := adapter.GetGroupMemberList(ctx, groupCode)
	if err != nil {
		return ToolError(fmt.Sprintf("获取群成员列表失败: %v", err)), nil
	}

	// 转换成员列表
	var allMembers []map[string]any
	for _, m := range memberList.Members {
		allMembers = append(allMembers, map[string]any{
			"user_id":  m.UserID,
			"nickname": m.Nickname,
			"role":     getUserTypeLabel(m.UserType),
		})
	}

	if len(allMembers) == 0 {
		return ToolError("群中未找到成员。"), nil
	}

	mentionHint := "To @mention a user, you MUST use the format: space + @ + nickname + space (e.g. \" @Alice \")."

	switch action {
	case "list_bots":
		var bots []map[string]any
		for _, m := range allMembers {
			role, _ := m["role"].(string)
			if role == "yuanbao_ai" || role == "bot" {
				bots = append(bots, m)
			}
		}
		if len(bots) == 0 {
			return ToolError("群中未找到机器人。"), nil
		}
		result := map[string]any{
			"success": true,
			"msg":     fmt.Sprintf("找到 %d 个机器人。", len(bots)),
			"members": bots,
		}
		if mention {
			result["mention_hint"] = mentionHint
		}
		return ToolResult(result), nil

	case "find":
		if name != "" {
			nameLower := strings.ToLower(name)
			var matched []map[string]any
			for _, m := range allMembers {
				nickname, _ := m["nickname"].(string)
				if strings.Contains(strings.ToLower(nickname), nameLower) {
					matched = append(matched, m)
				}
			}
			if len(matched) > 0 {
				result := map[string]any{
					"success": true,
					"msg":     fmt.Sprintf("找到 %d 个匹配 \"%s\" 的成员。", len(matched), name),
					"members": matched,
				}
				if mention {
					result["mention_hint"] = mentionHint
				}
				return ToolResult(result), nil
			}
			result := map[string]any{
				"success": false,
				"msg":     fmt.Sprintf("未找到匹配 \"%s\" 的成员。列出全部成员如下。", name),
				"members": allMembers,
			}
			if mention {
				result["mention_hint"] = mentionHint
			}
			return ToolResult(result), nil
		}
	}

	// list_all (default)
	result := map[string]any{
		"success": true,
		"msg":     fmt.Sprintf("找到 %d 个成员。", len(allMembers)),
		"members": allMembers,
	}
	if mention {
		result["mention_hint"] = mentionHint
	}
	return ToolResult(result), nil
}

// ───────────────────────────── 贴纸搜索工具 ─────────────────────────────

// YBSearchStickerTool 搜索贴纸。
type YBSearchStickerTool struct{}

func (t *YBSearchStickerTool) Name() string        { return "yb_search_sticker" }
func (t *YBSearchStickerTool) Toolset() string     { return "hermes-yuanbao" }
func (t *YBSearchStickerTool) Emoji() string       { return "🔍" }
func (t *YBSearchStickerTool) MaxResultChars() int { return 5000 }

func (t *YBSearchStickerTool) Description() string {
	return `搜索内置元宝贴纸（TIM 表情包）。返回匹配候选的 sticker_id、名称和描述。

在发送贴纸前调用此工具发现合适的 sticker_id。贴纸 = TIM face，不是消息反应。

用户要求发送贴纸/表情包时，优先使用此工具而非 Unicode emoji。`
}

func (t *YBSearchStickerTool) IsAvailable() bool {
	return GetYuanbaoAdapter() != nil || os.Getenv("HERMES_SESSION_PLATFORM") == "yuanbao"
}

func (t *YBSearchStickerTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "yb_search_sticker",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "搜索关键词（中文或英文，如'666'、'比心'、'cool'、'吃瓜'）。空字符串返回前 N 条贴纸。",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "返回候选数量上限（默认 10，最大 50）。",
				},
			},
			"required": []string{},
		},
	}
}

func (t *YBSearchStickerTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	query, _ := args["query"].(string)

	limit := 10
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}
	if limit > 50 {
		limit = 50
	}

	matches := searchStickers(query, limit)

	results := make([]map[string]any, len(matches))
	for i, s := range matches {
		results[i] = map[string]any{
			"sticker_id":  s.StickerID,
			"name":        s.Name,
			"description": s.Description,
			"package_id":  s.PackageID,
		}
	}

	return ToolResult(map[string]any{
		"success": true,
		"query":   query,
		"count":   len(matches),
		"results": results,
	}), nil
}

// ───────────────────────────── 贴纸发送工具 ─────────────────────────────

// YBSendStickerTool 发送贴纸。
type YBSendStickerTool struct{}

func (t *YBSendStickerTool) Name() string        { return "yb_send_sticker" }
func (t *YBSendStickerTool) Toolset() string     { return "hermes-yuanbao" }
func (t *YBSendStickerTool) Emoji() string       { return "🎨" }
func (t *YBSendStickerTool) MaxResultChars() int { return 2000 }

func (t *YBSendStickerTool) Description() string {
	return `发送内置贴纸（TIMFaceElem）到当前元宝聊天。

如果不知道 sticker_id/name，先调用 yb_search_sticker。

重要：用户要求发送贴纸/表情包时必须使用此工具，不要用 execute_code/Pillow/matplotlib 绘制 PNG 然后调用 send_image_file——那是假贴纸。`
}

func (t *YBSendStickerTool) IsAvailable() bool {
	return GetYuanbaoAdapter() != nil || os.Getenv("HERMES_SESSION_PLATFORM") == "yuanbao"
}

func (t *YBSendStickerTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "yb_send_sticker",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"sticker": map[string]any{
					"type":        "string",
					"description": "贴纸名称（如'六六六'、'比心'）或数字 sticker_id。空字符串发送随机贴纸。",
				},
				"chat_id": map[string]any{
					"type":        "string",
					"description": "目标聊天。默认当前会话。格式：'direct:{account_id}' 或 'group:{group_code}'。",
				},
				"reply_to": map[string]any{
					"type":        "string",
					"description": "引用消息 ID（仅群聊）。",
				},
			},
			"required": []string{},
		},
	}
}

func (t *YBSendStickerTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	sticker, _ := args["sticker"].(string)
	chatID, _ := args["chat_id"].(string)
	replyTo, _ := args["reply_to"].(string)

	// 默认使用当前会话
	if chatID == "" {
		chatID = os.Getenv("HERMES_SESSION_CHAT_ID")
	}
	if chatID == "" {
		return ToolError("chat_id 是必填项（未检测到活动元宝会话）。"), nil
	}

	adapter := GetYuanbaoAdapter()
	if adapter == nil {
		return ToolError("元宝适配器未连接。"), nil
	}

	// 解析贴纸
	var stickerObj *YuanbaoSticker
	sticker = strings.TrimSpace(sticker)

	if sticker == "" {
		stickerObj = getRandomSticker()
	} else {
		// 尝试按 ID 查找
		stickerObj = getStickerByID(sticker)
		if stickerObj == nil {
			// 尝试按名称查找
			stickerObj = getStickerByName(sticker)
		}
	}

	if stickerObj == nil {
		return ToolError(fmt.Sprintf("未找到贴纸: '%s'。请先用 yb_search_sticker 发现可用贴纸。", sticker)), nil
	}

	result, err := adapter.SendSticker(ctx, chatID, stickerObj.Name, replyTo)
	if err != nil {
		return ToolError(fmt.Sprintf("发送贴纸失败: %v", err)), nil
	}

	if !result.Success {
		return ToolError(result.Error), nil
	}

	return ToolResult(map[string]any{
		"success": true,
		"chat_id": chatID,
		"sticker": map[string]any{
			"sticker_id": stickerObj.StickerID,
			"name":       stickerObj.Name,
		},
		"message_id": result.MessageID,
		"note":       "贴纸已发送到聊天。如果有附加文本要说，现在回复；否则结束本轮。",
	}), nil
}
