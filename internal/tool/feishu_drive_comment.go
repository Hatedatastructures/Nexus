package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
)

// ───────────────────────────── 回复评论工具 ─────────────────────────────

// FeishuDriveReplyCommentTool 回复评论。
type FeishuDriveReplyCommentTool struct{}

func (t *FeishuDriveReplyCommentTool) Name() string        { return "feishu_drive_reply_comment" }
func (t *FeishuDriveReplyCommentTool) Toolset() string     { return "feishu_drive" }
func (t *FeishuDriveReplyCommentTool) Emoji() string       { return "✉️" }
func (t *FeishuDriveReplyCommentTool) MaxResultChars() int { return 5000 }

func (t *FeishuDriveReplyCommentTool) Description() string {
	return `回复飞书文档上的局部评论线程。用于局部（引用文本）评论。
整文档评论请使用 feishu_drive_add_comment。`
}

func (t *FeishuDriveReplyCommentTool) IsAvailable() bool { return checkFeishuDrive() }

func (t *FeishuDriveReplyCommentTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "feishu_drive_reply_comment",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_token": map[string]any{
					"type":        "string",
					"description": "文档 file token。",
				},
				"comment_id": map[string]any{
					"type":        "string",
					"description": "要回复的评论 ID。",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "回复文本内容（纯文本，不支持 markdown）。",
				},
				"file_type": map[string]any{
					"type":        "string",
					"description": "文件类型（默认 docx）。",
				},
			},
			"required": []string{"file_token", "comment_id", "content"},
		},
	}
}

func (t *FeishuDriveReplyCommentTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	fileToken, ok := args["file_token"].(string)
	if !ok || strings.TrimSpace(fileToken) == "" {
		return ToolError("参数 file_token 是必填项。"), nil
	}
	if err := validateFeishuToken(fileToken); err != nil {
		return ToolError(fmt.Sprintf("file_token %s", err)), nil
	}
	commentID, ok := args["comment_id"].(string)
	if !ok || strings.TrimSpace(commentID) == "" {
		return ToolError("参数 comment_id 是必填项。"), nil
	}
	if err := validateFeishuToken(commentID); err != nil {
		return ToolError(fmt.Sprintf("comment_id %s", err)), nil
	}
	content, ok := args["content"].(string)
	if !ok || strings.TrimSpace(content) == "" {
		return ToolError("参数 content 是必填项。"), nil
	}

	fileType, _ := args["file_type"].(string)
	if fileType == "" {
		fileType = "docx"
	}

	client, err := getDriveClient(ctx)
	if err != nil {
		return ToolError("飞书客户端不可用。"), nil
	}

	reqBody := map[string]any{
		"content": map[string]any{
			"elements": []map[string]any{
				{
					"type": "text_run",
					"text_run": map[string]any{
						"text": strings.TrimSpace(content),
					},
				},
			},
		},
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return ToolError(fmt.Sprintf("序列化请求体失败: %v", err)), nil
	}

	respBody, err := client.Request(ctx, "POST",
		"/open-apis/drive/v1/files/:file_token/comments/:comment_id/replies",
		bytesReader2(bodyBytes),
		map[string]string{"file_token": fileToken, "comment_id": commentID},
		map[string]string{"file_type": fileType})
	if err != nil {
		return ToolError(fmt.Sprintf("回复评论失败: %v", err)), nil
	}

	var apiResp struct {
		Code int            `json:"code"`
		Msg  string         `json:"msg"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return ToolError(fmt.Sprintf("解析响应失败: %v", err)), nil
	}
	if apiResp.Code != 0 {
		return ToolError(fmt.Sprintf("飞书 API 错误: code=%d msg=%s", apiResp.Code, apiResp.Msg)), nil
	}

	slog.Info("feishu comment reply succeeded", "file_token", fileToken, "comment_id", commentID)
	return ToolResult(map[string]any{
		"success": true,
		"data":    apiResp.Data,
	}), nil
}

// ───────────────────────────── 添加评论工具 ─────────────────────────────

// FeishuDriveAddCommentTool 添加整文档评论。
type FeishuDriveAddCommentTool struct{}

func (t *FeishuDriveAddCommentTool) Name() string        { return "feishu_drive_add_comment" }
func (t *FeishuDriveAddCommentTool) Toolset() string     { return "feishu_drive" }
func (t *FeishuDriveAddCommentTool) Emoji() string       { return "✉️" }
func (t *FeishuDriveAddCommentTool) MaxResultChars() int { return 5000 }

func (t *FeishuDriveAddCommentTool) Description() string {
	return `在飞书文档上添加新的整文档评论。
用于整文档评论，或 reply_comment 失败（错误码 1069302）时的备选方案。`
}

func (t *FeishuDriveAddCommentTool) IsAvailable() bool { return checkFeishuDrive() }

func (t *FeishuDriveAddCommentTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "feishu_drive_add_comment",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_token": map[string]any{
					"type":        "string",
					"description": "文档 file token。",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "评论文本内容（纯文本，不支持 markdown）。",
				},
				"file_type": map[string]any{
					"type":        "string",
					"description": "文件类型（默认 docx）。",
				},
			},
			"required": []string{"file_token", "content"},
		},
	}
}

func (t *FeishuDriveAddCommentTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	fileToken, ok := args["file_token"].(string)
	if !ok || strings.TrimSpace(fileToken) == "" {
		return ToolError("参数 file_token 是必填项。"), nil
	}
	if err := validateFeishuToken(fileToken); err != nil {
		return ToolError(fmt.Sprintf("file_token %s", err)), nil
	}
	content, ok := args["content"].(string)
	if !ok || strings.TrimSpace(content) == "" {
		return ToolError("参数 content 是必填项。"), nil
	}

	fileType, _ := args["file_type"].(string)
	if fileType == "" {
		fileType = "docx"
	}

	client, err := getDriveClient(ctx)
	if err != nil {
		return ToolError("飞书客户端不可用。"), nil
	}

	reqBody := map[string]any{
		"file_type": fileType,
		"reply_elements": []map[string]any{
			{
				"type": "text",
				"text": strings.TrimSpace(content),
			},
		},
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return ToolError(fmt.Sprintf("序列化请求体失败: %v", err)), nil
	}

	respBody, err := client.Request(ctx, "POST",
		"/open-apis/drive/v1/files/:file_token/new_comments",
		bytesReader2(bodyBytes),
		map[string]string{"file_token": fileToken},
		nil)
	if err != nil {
		return ToolError(fmt.Sprintf("添加评论失败: %v", err)), nil
	}

	var apiResp struct {
		Code int            `json:"code"`
		Msg  string         `json:"msg"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return ToolError(fmt.Sprintf("解析响应失败: %v", err)), nil
	}
	if apiResp.Code != 0 {
		return ToolError(fmt.Sprintf("飞书 API 错误: code=%d msg=%s", apiResp.Code, apiResp.Msg)), nil
	}

	slog.Info("feishu comment added successfully", "file_token", fileToken)
	return ToolResult(map[string]any{
		"success": true,
		"data":    apiResp.Data,
	}), nil
}
