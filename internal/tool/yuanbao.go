// Package tool 提供元宝平台工具集。
// 包含群信息查询、群成员查询、私聊发送、贴纸搜索和发送等功能。
// 依赖元宝平台适配器通过回调注入。
package tool

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
)

// ───────────────────────────── 元宝适配器接口 ─────────────────────────────

// YuanbaoAdapter 定义元宝平台适配器需要实现的方法。
type YuanbaoAdapter interface {
	// QueryGroupInfo 查询群基本信息（群名、群主、成员数）。
	QueryGroupInfo(ctx context.Context, groupCode string) (*YuanbaoGroupInfo, error)

	// GetGroupMemberList 获取群成员列表。
	GetGroupMemberList(ctx context.Context, groupCode string) (*YuanbaoMemberList, error)

	// SendDM 发送私聊消息。
	SendDM(ctx context.Context, userID, message, groupCode string) (*YuanbaoSendResult, error)

	// SendSticker 发送贴纸。
	SendSticker(ctx context.Context, chatID, stickerName, replyTo string) (*YuanbaoSendResult, error)

	// SendImageFile 发送图片文件。
	SendImageFile(ctx context.Context, chatID, filePath, groupCode string) (*YuanbaoSendResult, error)

	// SendDocument 发送文档文件。
	SendDocument(ctx context.Context, chatID, filePath, groupCode string) (*YuanbaoSendResult, error)
}

// YuanbaoGroupInfo 群信息结构。
type YuanbaoGroupInfo struct {
	GroupName    string `json:"group_name"`
	MemberCount  int    `json:"member_count"`
	OwnerID      string `json:"owner_id"`
	OwnerNickname string `json:"owner_nickname"`
}

// YuanbaoMemberList 群成员列表结构。
type YuanbaoMemberList struct {
	Members []YuanbaoMember `json:"members"`
}

// YuanbaoMember 群成员结构。
type YuanbaoMember struct {
	UserID   string `json:"user_id"`
	Nickname string `json:"nickname"`
	UserType int    `json:"user_type"` // 0=unknown, 1=user, 2=yuanbao_ai, 3=bot
}

// YuanbaoSendResult 发送结果结构。
type YuanbaoSendResult struct {
	Success   bool   `json:"success"`
	MessageID string `json:"message_id"`
	Error     string `json:"error"`
}

// YuanbaoSticker 贴纸结构。
type YuanbaoSticker struct {
	StickerID   string `json:"sticker_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	PackageID   string `json:"package_id"`
}

// ───────────────────────────── 全局适配器 ─────────────────────────────

var (
	globalYuanbaoAdapter YuanbaoAdapter
	yuanbaoAdapterMu     sync.RWMutex
)

// SetYuanbaoAdapter 设置全局元宝适配器。
func SetYuanbaoAdapter(adapter YuanbaoAdapter) {
	yuanbaoAdapterMu.Lock()
	defer yuanbaoAdapterMu.Unlock()
	globalYuanbaoAdapter = adapter
}

// GetYuanbaoAdapter 获取当前元宝适配器。
func GetYuanbaoAdapter() YuanbaoAdapter {
	yuanbaoAdapterMu.RLock()
	defer yuanbaoAdapterMu.RUnlock()
	return globalYuanbaoAdapter
}

// ───────────────────────────── 贴纸搜索 ─────────────────────────────

// 内置贴纸表（简化版，实际应从数据库或配置加载）
var builtInStickers = []YuanbaoSticker{
	{StickerID: "278", Name: "六六六", Description: "666 太棒了", PackageID: "default"},
	{StickerID: "279", Name: "比心", Description: "爱你 比心", PackageID: "default"},
	{StickerID: "280", Name: "ok", Description: "OK 没问题", PackageID: "default"},
	{StickerID: "281", Name: "吃瓜", Description: "围观 吃瓜群众", PackageID: "default"},
	{StickerID: "282", Name: "cool", Description: "酷 酷毙了", PackageID: "default"},
	{StickerID: "283", Name: "笑哭", Description: "笑哭了 哈哈哈", PackageID: "default"},
	{StickerID: "284", Name: "点赞", Description: "赞 很赞", PackageID: "default"},
	{StickerID: "285", Name: "握手", Description: "握手 合作愉快", PackageID: "default"},
	{StickerID: "286", Name: "抱抱", Description: "抱抱 安慰", PackageID: "default"},
	{StickerID: "287", Name: "加油", Description: "加油 努力", PackageID: "default"},
}

// searchStickers 搜索贴纸。
func searchStickers(query string, limit int) []YuanbaoSticker {
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	query = strings.ToLower(query)
	var results []YuanbaoSticker

	for _, s := range builtInStickers {
		if query == "" ||
			strings.Contains(strings.ToLower(s.Name), query) ||
			strings.Contains(strings.ToLower(s.Description), query) {
			results = append(results, s)
			if len(results) >= limit {
				break
			}
		}
	}

	return results
}

// getStickerByID 通过 ID 获取贴纸。
func getStickerByID(id string) *YuanbaoSticker {
	for _, s := range builtInStickers {
		if s.StickerID == id {
			return &s
		}
	}
	return nil
}

// getStickerByName 通过名称获取贴纸。
func getStickerByName(name string) *YuanbaoSticker {
	name = strings.ToLower(name)
	for _, s := range builtInStickers {
		if strings.ToLower(s.Name) == name {
			return &s
		}
	}
	return nil
}

// getRandomSticker 获取随机贴纸。
func getRandomSticker() *YuanbaoSticker {
	if len(builtInStickers) == 0 {
		return nil
	}
	return &builtInStickers[0] // 简化：返回第一个
}

// ───────────────────────────── 用户类型标签 ─────────────────────────────

var userTypeLabel = map[int]string{
	0: "unknown",
	1: "user",
	2: "yuanbao_ai",
	3: "bot",
}

func getUserTypeLabel(userType int) string {
	if label, ok := userTypeLabel[userType]; ok {
		return label
	}
	return "unknown"
}

// ───────────────────────────── 图片扩展名 ─────────────────────────────

var imageExts = map[string]bool{
	".jpg":  true,
	".jpeg": true,
	".png":  true,
	".gif":  true,
	".webp": true,
	".bmp":  true,
}

func isImageExt(ext string) bool {
	return imageExts[strings.ToLower(ext)]
}

// ───────────────────────────── 群信息查询工具 ─────────────────────────────

// YBQueryGroupInfoTool 查询群基本信息。
type YBQueryGroupInfoTool struct{}

func (t *YBQueryGroupInfoTool) Name() string { return "yb_query_group_info" }
func (t *YBQueryGroupInfoTool) Toolset() string { return "hermes-yuanbao" }
func (t *YBQueryGroupInfoTool) Emoji() string { return "👥" }
func (t *YBQueryGroupInfoTool) MaxResultChars() int { return 5000 }

func (t *YBQueryGroupInfoTool) Description() string {
	return "查询群基本信息（群名、群主、成员数）。群在应用中称为'派/Pai'。"
}

func (t *YBQueryGroupInfoTool) IsAvailable() bool {
	return GetYuanbaoAdapter() != nil || os.Getenv("HERMES_SESSION_PLATFORM") == "yuanbao"
}

func (t *YBQueryGroupInfoTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "yb_query_group_info",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"group_code": map[string]any{
					"type":        "string",
					"description": "群唯一标识 (group_code)。",
				},
			},
			"required": []string{"group_code"},
		},
	}
}

func (t *YBQueryGroupInfoTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	groupCode, ok := args["group_code"].(string)
	if !ok || groupCode == "" {
		return ToolError("参数 group_code 是必填项。"), nil
	}

	adapter := GetYuanbaoAdapter()
	if adapter == nil {
		return ToolError("元宝适配器未连接。"), nil
	}

	info, err := adapter.QueryGroupInfo(ctx, groupCode)
	if err != nil {
		return ToolError(fmt.Sprintf("查询群信息失败: %v", err)), nil
	}

	result := map[string]any{
		"success":     true,
		"group_code":  groupCode,
		"group_name":  info.GroupName,
		"member_count": info.MemberCount,
		"owner": map[string]any{
			"user_id":   info.OwnerID,
			"nickname":  info.OwnerNickname,
		},
		"note": "群在应用中称为'派(Pai)'。",
	}

	return ToolResult(result), nil
}

// ───────────────────────────── 群成员查询工具 ─────────────────────────────

// YBQueryGroupMembersTool 查询群成员。
type YBQueryGroupMembersTool struct{}

func (t *YBQueryGroupMembersTool) Name() string { return "yb_query_group_members" }
func (t *YBQueryGroupMembersTool) Toolset() string { return "hermes-yuanbao" }
func (t *YBQueryGroupMembersTool) Emoji() string { return "📋" }
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

func (t *YBSearchStickerTool) Name() string { return "yb_search_sticker" }
func (t *YBSearchStickerTool) Toolset() string { return "hermes-yuanbao" }
func (t *YBSearchStickerTool) Emoji() string { return "🔍" }
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
	if l, ok := args["limit"].(int); ok && l > 0 {
		limit = l
	}
	if limit > 50 {
		limit = 50
	}

	matches := searchStickers(query, limit)

	results := make([]map[string]any, len(matches))
	for i, s := range matches {
		results[i] = map[string]any{
			"sticker_id":   s.StickerID,
			"name":         s.Name,
			"description":  s.Description,
			"package_id":   s.PackageID,
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

func (t *YBSendStickerTool) Name() string { return "yb_send_sticker" }
func (t *YBSendStickerTool) Toolset() string { return "hermes-yuanbao" }
func (t *YBSendStickerTool) Emoji() string { return "🎨" }
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
		"success":   true,
		"chat_id":   chatID,
		"sticker": map[string]any{
			"sticker_id": stickerObj.StickerID,
			"name":       stickerObj.Name,
		},
		"message_id": result.MessageID,
		"note":       "贴纸已发送到聊天。如果有附加文本要说，现在回复；否则结束本轮。",
	}), nil
}

// ───────────────────────────── 私聊发送工具 ─────────────────────────────

// YBSendDMTool 发送私聊消息。
type YBSendDMTool struct{}

func (t *YBSendDMTool) Name() string { return "yb_send_dm" }
func (t *YBSendDMTool) Toolset() string { return "hermes-yuanbao" }
func (t *YBSendDMTool) Emoji() string { return "✉️" }
func (t *YBSendDMTool) MaxResultChars() int { return 5000 }

func (t *YBSendDMTool) Description() string {
	return `发送私聊消息（DM）给群成员，可选附带媒体文件。

自动在群成员列表中按名称查找用户并发送消息。支持文本、图片和文件附件。也可直接提供 user_id。`
}

func (t *YBSendDMTool) IsAvailable() bool {
	return GetYuanbaoAdapter() != nil || os.Getenv("HERMES_SESSION_PLATFORM") == "yuanbao"
}

func (t *YBSendDMTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "yb_send_dm",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"group_code": map[string]any{
					"type":        "string",
					"description": "目标用户所在群。从 chat_id 提取：'group:328306697' → '328306697'。",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "目标用户昵称（部分匹配，不区分大小写）。未提供 user_id 时必需。",
				},
				"message": map[string]any{
					"type":        "string",
					"description": "要发送的消息文本。",
				},
				"user_id": map[string]any{
					"type":        "string",
					"description": "目标用户账号 ID。提供则跳过成员查找。",
				},
			},
			"required": []string{},
		},
	}
}

func (t *YBSendDMTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	groupCode, _ := args["group_code"].(string)
	name, _ := args["name"].(string)
	message, _ := args["message"].(string)
	userID, _ := args["user_id"].(string)

	// 如果未提供 groupCode，尝试从会话环境提取
	if groupCode == "" {
		chatID := os.Getenv("HERMES_SESSION_CHAT_ID")
		if strings.HasPrefix(chatID, "group:") {
			groupCode = strings.TrimPrefix(chatID, "group:")
		}
	}

	adapter := GetYuanbaoAdapter()
	if adapter == nil {
		return ToolError("元宝适配器未连接。"), nil
	}

	resolvedUserID := strings.TrimSpace(userID)
	resolvedNickname := strings.TrimSpace(name)

	// 如果未提供 user_id，在群成员列表中查找
	if resolvedUserID == "" {
		if groupCode == "" {
			return ToolError("未提供 user_id 时 group_code 是必填项。"), nil
		}
		if name == "" {
			return ToolError("未提供 user_id 时 name 是必填项。"), nil
		}

		memberList, err := adapter.GetGroupMemberList(ctx, groupCode)
		if err != nil {
			return ToolError(fmt.Sprintf("获取群成员列表失败: %v", err)), nil
		}

		nameLower := strings.ToLower(name)
		var matched []YuanbaoMember
		for _, m := range memberList.Members {
			if strings.Contains(strings.ToLower(m.Nickname), nameLower) {
				matched = append(matched, m)
			}
		}

		if len(matched) == 0 {
			return ToolError(fmt.Sprintf("群 %s 中未找到匹配 \"%s\" 的成员。", groupCode, name)), nil
		}
		if len(matched) > 1 {
			candidates := make([]map[string]any, len(matched))
			for i, m := range matched {
				candidates[i] = map[string]any{
					"user_id":  m.UserID,
					"nickname": m.Nickname,
				}
			}
			return ToolResult(map[string]any{
				"success":    false,
				"error":      fmt.Sprintf("多个成员匹配 \"%s\"。请指定具体用户。", name),
				"candidates": candidates,
			}), nil
		}

		resolvedUserID = matched[0].UserID
		resolvedNickname = matched[0].Nickname
	}

	if resolvedUserID == "" {
		return ToolError("无法解析 user_id。"), nil
	}

	// 发送私聊消息
	result, err := adapter.SendDM(ctx, resolvedUserID, message, groupCode)
	if err != nil {
		return ToolError(fmt.Sprintf("发送私聊失败: %v", err)), nil
	}

	if !result.Success {
		return ToolError(result.Error), nil
	}

	return ToolResult(map[string]any{
		"success":    true,
		"user_id":    resolvedUserID,
		"nickname":   resolvedNickname,
		"message_id": result.MessageID,
		"note":       fmt.Sprintf("私聊消息已成功发送给 \"%s\"。", resolvedNickname),
	}), nil
}

// ───────────────────────────── init 注册 ─────────────────────────────

func init() {
	r := GetRegistry()
	r.Register(&YBQueryGroupInfoTool{})
	r.Register(&YBQueryGroupMembersTool{})
	r.Register(&YBSearchStickerTool{})
	r.Register(&YBSendStickerTool{})
	r.Register(&YBSendDMTool{})
}