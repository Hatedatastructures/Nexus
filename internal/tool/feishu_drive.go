package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// bytesReader2 创建 bytes Reader。
func bytesReader2(data []byte) io.Reader {
	return bytes.NewReader(data)
}



// ───────────────────────────── 列出评论工具 ─────────────────────────────

// FeishuDriveListCommentsTool 列出文档评论。
type FeishuDriveListCommentsTool struct{}

func (t *FeishuDriveListCommentsTool) Name() string        { return "feishu_drive_list_comments" }
func (t *FeishuDriveListCommentsTool) Toolset() string     { return "feishu_drive" }
func (t *FeishuDriveListCommentsTool) Emoji() string       { return "💬" }
func (t *FeishuDriveListCommentsTool) MaxResultChars() int { return 50000 }

func (t *FeishuDriveListCommentsTool) Description() string {
	return "列出飞书文档上的评论。使用 is_whole=true 仅列出整文档评论。"
}

func (t *FeishuDriveListCommentsTool) IsAvailable() bool { return checkFeishuDrive() }

func (t *FeishuDriveListCommentsTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "feishu_drive_list_comments",
		Description: t.Description(),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_token": map[string]any{
					"type":        "string",
					"description": "文档 file token。",
				},
				"file_type": map[string]any{
					"type":        "string",
					"description": "文件类型（默认 docx）。",
				},
				"is_whole": map[string]any{
					"type":        "boolean",
					"description": "为 true 时仅返回整文档评论。",
				},
				"page_size": map[string]any{
					"type":        "integer",
					"description": "每页评论数（最大 100）。",
				},
				"page_token": map[string]any{
					"type":        "string",
					"description": "分页 token。",
				},
			},
			"required": []string{"file_token"},
		},
	}
}

func (t *FeishuDriveListCommentsTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	fileToken, ok := args["file_token"].(string)
	if !ok || strings.TrimSpace(fileToken) == "" {
		return ToolError("参数 file_token 是必填项。"), nil
	}
	if err := validateFeishuToken(fileToken); err != nil {
		return ToolError(fmt.Sprintf("file_token %s", err)), nil
	}

	fileType, _ := args["file_type"].(string)
	if fileType == "" {
		fileType = "docx"
	}

	isWhole, _ := args["is_whole"].(bool)
	pageSize := 100
	if v, ok := args["page_size"].(float64); ok && v > 0 && v <= 100 {
		pageSize = int(v)
	}
	pageToken, _ := args["page_token"].(string)

	client, err := getDriveClient(ctx)
	if err != nil {
		return ToolError("飞书客户端不可用。"), nil
	}

	queries := map[string]string{
		"file_type":    fileType,
		"user_id_type": "open_id",
		"page_size":    fmt.Sprintf("%d", pageSize),
	}
	if isWhole {
		queries["is_whole"] = "true"
	}
	if pageToken != "" {
		queries["page_token"] = pageToken
	}

	respBody, err := client.Request(ctx, "GET",
		"/open-apis/drive/v1/files/:file_token/comments",
		nil,
		map[string]string{"file_token": fileToken},
		queries)
	if err != nil {
		return ToolError(fmt.Sprintf("列出评论失败: %v", err)), nil
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

	return ToolResult(apiResp.Data), nil
}

// ───────────────────────────── 列出评论回复工具 ─────────────────────────────

// FeishuDriveListRepliesTool 列出评论回复。
type FeishuDriveListRepliesTool struct{}

func (t *FeishuDriveListRepliesTool) Name() string        { return "feishu_drive_list_comment_replies" }
func (t *FeishuDriveListRepliesTool) Toolset() string     { return "feishu_drive" }
func (t *FeishuDriveListRepliesTool) Emoji() string       { return "💬" }
func (t *FeishuDriveListRepliesTool) MaxResultChars() int { return 50000 }

func (t *FeishuDriveListRepliesTool) Description() string {
	return "列出飞书文档评论线程中的所有回复。"
}

func (t *FeishuDriveListRepliesTool) IsAvailable() bool { return checkFeishuDrive() }

func (t *FeishuDriveListRepliesTool) Schema() *ToolSchema {
	return &ToolSchema{
		Name:        "feishu_drive_list_comment_replies",
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
					"description": "评论 ID。",
				},
				"file_type": map[string]any{
					"type":        "string",
					"description": "文件类型（默认 docx）。",
				},
				"page_size": map[string]any{
					"type":        "integer",
					"description": "每页回复数（最大 100）。",
				},
				"page_token": map[string]any{
					"type":        "string",
					"description": "分页 token。",
				},
			},
			"required": []string{"file_token", "comment_id"},
		},
	}
}

func (t *FeishuDriveListRepliesTool) Execute(ctx context.Context, args map[string]any) (string, error) {
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

	fileType, _ := args["file_type"].(string)
	if fileType == "" {
		fileType = "docx"
	}
	pageSize := 100
	if v, ok := args["page_size"].(float64); ok && v > 0 && v <= 100 {
		pageSize = int(v)
	}
	pageToken, _ := args["page_token"].(string)

	client, err := getDriveClient(ctx)
	if err != nil {
		return ToolError("飞书客户端不可用。"), nil
	}

	queries := map[string]string{
		"file_type":    fileType,
		"user_id_type": "open_id",
		"page_size":    fmt.Sprintf("%d", pageSize),
	}
	if pageToken != "" {
		queries["page_token"] = pageToken
	}

	respBody, err := client.Request(ctx, "GET",
		"/open-apis/drive/v1/files/:file_token/comments/:comment_id/replies",
		nil,
		map[string]string{"file_token": fileToken, "comment_id": commentID},
		queries)
	if err != nil {
		return ToolError(fmt.Sprintf("列出回复失败: %v", err)), nil
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

	return ToolResult(apiResp.Data), nil
}



