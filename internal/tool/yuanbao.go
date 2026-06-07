// Package tool 提供元宝平台工具集。
// 本文件包含贴纸搜索、用户类型标签、群成员查询工具等辅助逻辑。
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
	GroupName     string `json:"group_name"`
	MemberCount   int    `json:"member_count"`
	OwnerID       string `json:"owner_id"`
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

// ───────────────────────────── 群信息查询工具 ─────────────────────────────

// YBQueryGroupInfoTool 查询群基本信息。
type YBQueryGroupInfoTool struct{}

func (t *YBQueryGroupInfoTool) Name() string        { return "yb_query_group_info" }
func (t *YBQueryGroupInfoTool) Toolset() string     { return "hermes-yuanbao" }
func (t *YBQueryGroupInfoTool) Emoji() string       { return "👥" }
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
		"success":      true,
		"group_code":   groupCode,
		"group_name":   info.GroupName,
		"member_count": info.MemberCount,
		"owner": map[string]any{
			"user_id":  info.OwnerID,
			"nickname": info.OwnerNickname,
		},
		"note": "群在应用中称为'派(Pai)'。",
	}

	return ToolResult(result), nil
}
