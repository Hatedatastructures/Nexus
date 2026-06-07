package tool

import (
	"log/slog"
)

// RegisterAllTools 将所有内置工具注册到指定注册中心。
// 替代分散在各文件中的 init() 自注册模式。
func RegisterAllTools(r RegistryInterface) {
	// ── 文件操作 ──
	r.Register(&FileReadTool{})
	r.Register(&FileWriteTool{})
	r.Register(&FileEditTool{})
	r.Register(&PatchTool{})
	r.Register(&FileSearchTool{})
	r.Register(&GlobTool{})

	// ── 网络工具 ──
	r.Register(&WebSearchTool{})
	r.Register(&WebExtractTool{})
	r.Register(&URLSafetyTool{})

	// ── 浏览器 (Camofox) ──
	r.Register(&CamofoxNavigateTool{})
	r.Register(&CamofoxSnapshotTool{})
	r.Register(&CamofoxClickTool{})
	r.Register(&CamofoxTypeTool{})
	r.Register(&CamofoxScrollTool{})
	r.Register(&CamofoxCloseTool{})
	r.Register(&CamofoxVisionTool{})

	// ── 浏览器 (Playwright) ──
	r.Register(&BrowserNavigateTool{})
	r.Register(&BrowserScreenshotTool{})
	r.Register(&BrowserClickTool{})
	r.Register(&BrowserTypeTool{})
	r.Register(&BrowserHandleDialogTool{})
	r.Register(&BrowserSuperviseTool{})
	r.Register(&BrowserCDPTool{})

	// ── 系统工具 ──
	r.Register(NewTerminalTool())
	r.Register(&CodeExecuteTool{})
	r.Register(&ProcessTool{})
	r.Register(&NotebookEditTool{})

	// ── 搜索 ──
	r.Register(&ToolSearchTool{})
	r.Register(&XSearchTool{})
	r.Register(&OSVCheckTool{})
	r.Register(&SessionSearchTool{})

	// ── 多媒体 ──
	r.Register(&VisionAnalyzeTool{})
	r.Register(&TranscriptionTool{})
	r.Register(&TTSTool{})
	r.Register(&ImageGenTool{})
	r.Register(&VideoGenerateTool{})
	r.Register(&VoiceRecordTool{})
	r.Register(&VoicePlayTool{})

	// ── 计划/看板 ──
	r.Register(&TodoTool{})
	r.Register(&KanbanListTool{})
	r.Register(&KanbanShowTool{})
	r.Register(&KanbanCreateTool{})
	r.Register(&KanbanCompleteTool{})
	r.Register(&KanbanBlockTool{})
	r.Register(&KanbanUnblockTool{})
	r.Register(&KanbanHeartbeatTool{})
	r.Register(&KanbanCommentTool{})
	r.Register(&KanbanLinkTool{})
	r.Register(&EnterPlanModeTool{})
	r.Register(&ExitPlanModeTool{})
	r.Register(&CheckpointTool{})

	// ── 记忆/技能 ──
	r.Register(&MemoryTool{})
	r.Register(&SkillsListTool{})
	r.Register(&SkillViewTool{})
	r.Register(&SkillManageTool{})
	r.Register(&SkillScanTool{})

	// ── 沟通/扩展 ──
	r.Register(&SendMessageTool{})
	r.Register(&DelegateTaskTool{})
	r.Register(&ClarifyTool{})
	r.Register(&MCPTool{})
	r.Register(&MoATool{})
	r.Register(&CronJobTool{})
	r.Register(NewHomeAssistantTool())
	r.Register(NewDiscordExtTool())

	// ── 计算机操作 ──
	r.Register(&ScreenshotTool{})
	r.Register(&MouseClickTool{})
	r.Register(&MouseMoveTool{})
	r.Register(&TypeTextTool{})
	r.Register(&KeyPressTool{})

	// ── 飞书 ──
	r.Register(&FeishuDocReadTool{})
	r.Register(&FeishuDriveListCommentsTool{})
	r.Register(&FeishuDriveListRepliesTool{})
	r.Register(&FeishuDriveReplyCommentTool{})
	r.Register(&FeishuDriveAddCommentTool{})

	// ── 元宝 ──
	r.Register(&YBQueryGroupInfoTool{})
	r.Register(&YBQueryGroupMembersTool{})
	r.Register(&YBSearchStickerTool{})
	r.Register(&YBSendStickerTool{})

	slog.Debug("all builtin tools registered", "count", len(r.ListTools()))
}

// RegistryInterface 是注册中心的最小接口，仅用于 RegisterAllTools。
type RegistryInterface interface {
	Register(tool Tool)
	ListTools() []string
}

// Compile时断言: *Registry 满足 RegistryInterface
var _ RegistryInterface = (*Registry)(nil)
