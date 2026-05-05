// Package gateway 提供频道目录系统。
// ChannelDirectory 负责从各平台适配器枚举可用频道/聊天，并提供名称解析。
// 支持 API 枚举和会话数据回退两种发现策略。
package gateway

import (
	"context"
	"log/slog"
	"strings"

	"nexus-agent/internal/gateway/platforms"
)

// ───────────────────────────── 频道条目 ─────────────────────────────

// ChannelEntry 表示频道目录中的一个频道条目。
type ChannelEntry struct {
	Platform   platforms.Platform // 平台类型 (telegram / discord / slack 等)
	ChannelID  string             // 平台频道 ID
	Name       string             // 频道显示名称
	GuildInfo  string             // 所属服务器/群组信息 (可选)
}

// ───────────────────────────── 频道目录构建 ─────────────────────────────

// BuildChannelDirectory 从所有平台适配器构建频道目录。
// 尝试通过 API 枚举频道列表，如果适配器不支持，则回退到会话数据。
//
// 构建策略:
//  1. 对每个适配器，尝试调用 ChannelLister 接口获取频道列表
//  2. 如果适配器不支持 ChannelLister，使用会话管理器中的历史会话数据
//  3. 合并去重后返回
func BuildChannelDirectory(ctx context.Context, adapters []platforms.PlatformAdapter, sessionMgr *SessionManager) []ChannelEntry {
	var entries []ChannelEntry
	seen := make(map[string]bool) // platform:channelID → true

	for _, adapter := range adapters {
		platformType := adapter.PlatformType()

		// 尝试 API 枚举
		if lister, ok := adapter.(ChannelLister); ok {
			channels, err := lister.ListChannels(ctx)
			if err != nil {
				slog.Warn("频道目录: API 枚举失败",
					"platform", string(platformType),
					"error", err,
				)
			} else {
				for _, ch := range channels {
					key := string(platformType) + ":" + ch.ChannelID
					if seen[key] {
						continue
					}
					seen[key] = true
					ch.Platform = platformType
					entries = append(entries, ch)
				}
				continue
			}
		}

		// 回退: 从会话管理器中提取历史会话数据
		if sessionMgr != nil {
			sessionEntries := entriesFromSessions(sessionMgr, platformType)
			for _, entry := range sessionEntries {
				key := string(entry.Platform) + ":" + entry.ChannelID
				if seen[key] {
					continue
				}
				seen[key] = true
				entries = append(entries, entry)
			}
		}
	}

	slog.Debug("频道目录: 构建完成", "count", len(entries))
	return entries
}

// ───────────────────────────── 名称解析 ─────────────────────────────

// ResolveChannelName 在频道目录中查找与名称匹配的频道条目。
// 支持精确匹配和模糊匹配 (不区分大小写，忽略前缀 #/@)。
// 返回 nil 表示未找到匹配项。
func ResolveChannelName(entries []ChannelEntry, name string) *ChannelEntry {
	if name == "" {
		return nil
	}

	normalized := normalizeChannelName(name)

	// 第一轮: 精确匹配
	for i := range entries {
		if normalizeChannelName(entries[i].Name) == normalized {
			return &entries[i]
		}
	}

	// 第二轮: 精确匹配 ChannelID
	for i := range entries {
		if entries[i].ChannelID == name {
			return &entries[i]
		}
	}

	// 第三轮: 前缀匹配 (模糊)
	for i := range entries {
		if strings.HasPrefix(normalizeChannelName(entries[i].Name), normalized) {
			return &entries[i]
		}
	}

	// 第四轮: 包含匹配
	for i := range entries {
		if strings.Contains(normalizeChannelName(entries[i].Name), normalized) {
			return &entries[i]
		}
	}

	return nil
}

// ───────────────────────────── ChannelLister 接口 ─────────────────────────────

// ChannelLister 是支持频道枚举的适配器扩展接口。
// 平台适配器如果实现此接口，BuildChannelDirectory 会优先使用 API 枚举。
type ChannelLister interface {
	// ListChannels 返回适配器可访问的所有频道列表。
	ListChannels(ctx context.Context) ([]ChannelEntry, error)
}

// ───────────────────────────── 会话数据回退 ─────────────────────────────

// entriesFromSessions 从会话管理器中提取指定平台的历史会话条目。
func entriesFromSessions(sessionMgr *SessionManager, platform platforms.Platform) []ChannelEntry {
	var entries []ChannelEntry

	sessionMgr.mu.RLock()
	defer sessionMgr.mu.RUnlock()

	for _, session := range sessionMgr.sessions {
		if session.Source == nil {
			continue
		}
		if session.Source.Platform != platform {
			continue
		}

		entry := ChannelEntry{
			Platform:  platform,
			ChannelID: session.Source.ChatID,
			Name:      session.Source.ChatName,
		}

		// 使用 ChatType 作为 GuildInfo 的回退
		if session.Source.ChatType != "" {
			entry.GuildInfo = session.Source.ChatType
		}

		entries = append(entries, entry)
	}

	return entries
}

// ───────────────────────────── 内部辅助 ─────────────────────────────

// normalizeChannelName 标准化频道名称用于比较。
// 移除前缀 (#, @)，转换为小写，去除首尾空格。
func normalizeChannelName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "#")
	name = strings.TrimPrefix(name, "@")
	return strings.ToLower(name)
}
