package platforms

import (
	"context"
	"encoding/json"
	"testing"
)

// ---------------------------------------------------------------------------
// Matrix adapter -- handleRoomEvent (internal, tested indirectly)
// ---------------------------------------------------------------------------

func TestMatrixHandleRoomEventTextMessage(t *testing.T) {
	t.Parallel()

	a := NewMatrixAdapter("https://matrix.org", "token", "@bot:matrix.org")
	a.msgCh = make(chan *MessageEvent, 128)

	content := map[string]any{
		"msgtype": "m.text",
		"body":    "Hello Matrix",
	}
	contentJSON, _ := json.Marshal(content)

	evt := &matrixEvent{
		Type:           "m.room.message",
		Sender:         "@alice:matrix.org",
		EventID:        "$event123",
		OriginServerTS: 1700000000000,
		Content:        contentJSON,
	}

	a.handleRoomEvent(context.Background(), "!room123:matrix.org", evt)

	select {
	case msg := <-a.msgCh:
		if msg.Text != "Hello Matrix" {
			t.Errorf("Text = %q, want %q", msg.Text, "Hello Matrix")
		}
		if msg.MessageType != MsgText {
			t.Errorf("MessageType = %q, want %q", msg.MessageType, MsgText)
		}
		if msg.Source.Platform != PlatformMatrix {
			t.Errorf("Platform = %q, want %q", msg.Source.Platform, PlatformMatrix)
		}
		if msg.Source.ChatID != "!room123:matrix.org" {
			t.Errorf("ChatID = %q, want %q", msg.Source.ChatID, "!room123:matrix.org")
		}
		if msg.Source.UserID != "@alice:matrix.org" {
			t.Errorf("UserID = %q, want %q", msg.Source.UserID, "@alice:matrix.org")
		}
		if msg.Source.ChatType != "group" {
			t.Errorf("ChatType = %q, want %q", msg.Source.ChatType, "group")
		}
		if msg.MessageID != "$event123" {
			t.Errorf("MessageID = %q, want %q", msg.MessageID, "$event123")
		}
	default:
		t.Fatal("expected message on channel")
	}
}

func TestMatrixHandleRoomEventIgnoreOwnMessages(t *testing.T) {
	t.Parallel()

	a := NewMatrixAdapter("https://matrix.org", "token", "@bot:matrix.org")
	a.msgCh = make(chan *MessageEvent, 128)

	content := map[string]any{
		"msgtype": "m.text",
		"body":    "My own message",
	}
	contentJSON, _ := json.Marshal(content)

	evt := &matrixEvent{
		Type:    "m.room.message",
		Sender:  "@bot:matrix.org", // same as userID
		Content: contentJSON,
	}

	a.handleRoomEvent(context.Background(), "!room:matrix.org", evt)

	select {
	case <-a.msgCh:
		t.Error("should not receive own messages")
	default:
		// expected
	}
}

func TestMatrixHandleRoomEventIgnoreNonMessage(t *testing.T) {
	t.Parallel()

	a := NewMatrixAdapter("https://matrix.org", "token", "@bot:matrix.org")
	a.msgCh = make(chan *MessageEvent, 128)

	evt := &matrixEvent{
		Type:   "m.room.member",
		Sender: "@alice:matrix.org",
	}

	a.handleRoomEvent(context.Background(), "!room:matrix.org", evt)

	select {
	case <-a.msgCh:
		t.Error("should not receive non-message events")
	default:
		// expected
	}
}

func TestMatrixHandleRoomEventIgnoreEdits(t *testing.T) {
	t.Parallel()

	a := NewMatrixAdapter("https://matrix.org", "token", "@bot:matrix.org")
	a.msgCh = make(chan *MessageEvent, 128)

	content := map[string]any{
		"msgtype": "m.text",
		"body":    "edited text",
		"m.relates_to": map[string]any{
			"rel_type": "m.replace",
			"event_id": "$original",
		},
	}
	contentJSON, _ := json.Marshal(content)

	evt := &matrixEvent{
		Type:    "m.room.message",
		Sender:  "@alice:matrix.org",
		Content: contentJSON,
	}

	a.handleRoomEvent(context.Background(), "!room:matrix.org", evt)

	select {
	case <-a.msgCh:
		t.Error("should not receive edit events")
	default:
		// expected
	}
}

func TestMatrixHandleRoomEventImageMessage(t *testing.T) {
	t.Parallel()

	a := NewMatrixAdapter("https://matrix.org", "token", "@bot:matrix.org")
	a.msgCh = make(chan *MessageEvent, 128)

	content := map[string]any{
		"msgtype": "m.image",
		"body":    "photo.jpg",
		"url":     "mxc://matrix.org/abc123",
	}
	contentJSON, _ := json.Marshal(content)

	evt := &matrixEvent{
		Type:    "m.room.message",
		Sender:  "@alice:matrix.org",
		EventID: "$img123",
		Content: contentJSON,
	}

	a.handleRoomEvent(context.Background(), "!room:matrix.org", evt)

	select {
	case msg := <-a.msgCh:
		if msg.MessageType != MsgPhoto {
			t.Errorf("MessageType = %q, want %q", msg.MessageType, MsgPhoto)
		}
		if len(msg.MediaURLs) != 1 || msg.MediaURLs[0] != "mxc://matrix.org/abc123" {
			t.Errorf("MediaURLs = %v, want [mxc://matrix.org/abc123]", msg.MediaURLs)
		}
	default:
		t.Fatal("expected message on channel")
	}
}

func TestMatrixHandleRoomEventFormattedBody(t *testing.T) {
	t.Parallel()

	a := NewMatrixAdapter("https://matrix.org", "token", "@bot:matrix.org")
	a.msgCh = make(chan *MessageEvent, 128)

	content := map[string]any{
		"msgtype":        "m.text",
		"body":           "plain text",
		"format":         "org.matrix.custom.html",
		"formatted_body": "<b>bold text</b>",
	}
	contentJSON, _ := json.Marshal(content)

	evt := &matrixEvent{
		Type:    "m.room.message",
		Sender:  "@alice:matrix.org",
		Content: contentJSON,
	}

	a.handleRoomEvent(context.Background(), "!room:matrix.org", evt)

	select {
	case msg := <-a.msgCh:
		if msg.Text != "<b>bold text</b>" {
			t.Errorf("Text = %q, want %q", msg.Text, "<b>bold text</b>")
		}
	default:
		t.Fatal("expected message on channel")
	}
}

func TestMatrixHandleRoomEventReplyParsing(t *testing.T) {
	t.Parallel()

	a := NewMatrixAdapter("https://matrix.org", "token", "@bot:matrix.org")
	a.msgCh = make(chan *MessageEvent, 128)

	content := map[string]any{
		"msgtype": "m.text",
		"body":    "> reply\n\nresponse",
		"m.relates_to": map[string]any{
			"m.in_reply_to": map[string]any{
				"event_id": "$original_msg",
			},
		},
	}
	contentJSON, _ := json.Marshal(content)

	evt := &matrixEvent{
		Type:    "m.room.message",
		Sender:  "@alice:matrix.org",
		Content: contentJSON,
	}

	a.handleRoomEvent(context.Background(), "!room:matrix.org", evt)

	select {
	case msg := <-a.msgCh:
		if msg.ReplyToMsgID != "$original_msg" {
			t.Errorf("ReplyToMsgID = %q, want %q", msg.ReplyToMsgID, "$original_msg")
		}
	default:
		t.Fatal("expected message on channel")
	}
}

func TestMatrixHandleRoomEventMediaTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		msgType  string
		expected MessageType
	}{
		{"m.text", MsgText},
		{"m.image", MsgPhoto},
		{"m.audio", MsgVoice},
		{"m.video", MsgVideo},
		{"m.file", MsgDocument},
	}

	for _, tc := range tests {
		t.Run(tc.msgType, func(t *testing.T) {
			t.Parallel()

			a := NewMatrixAdapter("https://matrix.org", "token", "@bot:matrix.org")
			a.msgCh = make(chan *MessageEvent, 128)

			content := map[string]any{
				"msgtype": tc.msgType,
				"body":    "content",
			}
			contentJSON, _ := json.Marshal(content)

			evt := &matrixEvent{
				Type:    "m.room.message",
				Sender:  "@alice:matrix.org",
				EventID: "$event",
				Content: contentJSON,
			}

			a.handleRoomEvent(context.Background(), "!room:matrix.org", evt)

			select {
			case msg := <-a.msgCh:
				if msg.MessageType != tc.expected {
					t.Errorf("MessageType = %q, want %q", msg.MessageType, tc.expected)
				}
			default:
				t.Fatal("expected message on channel")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Matrix adapter -- matrixEvent struct
// ---------------------------------------------------------------------------

func TestMatrixEventParsing(t *testing.T) {
	t.Parallel()

	raw := `{
			"type": "m.room.message",
			"sender": "@alice:matrix.org",
			"event_id": "$evt123",
			"origin_server_ts": 1700000000000,
			"content": {"msgtype":"m.text","body":"hi"}
		}`

	var evt matrixEvent
	if err := json.Unmarshal([]byte(raw), &evt); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if evt.Type != "m.room.message" {
		t.Errorf("Type = %q, want %q", evt.Type, "m.room.message")
	}
	if evt.Sender != "@alice:matrix.org" {
		t.Errorf("Sender = %q, want %q", evt.Sender, "@alice:matrix.org")
	}
	if evt.EventID != "$evt123" {
		t.Errorf("EventID = %q, want %q", evt.EventID, "$evt123")
	}
	if evt.OriginServerTS != 1700000000000 {
		t.Errorf("OriginServerTS = %d, want 1700000000000", evt.OriginServerTS)
	}
}

// ---------------------------------------------------------------------------
// Matrix adapter -- syncResponse struct
// ---------------------------------------------------------------------------

func TestMatrixSyncResponseParsing(t *testing.T) {
	t.Parallel()

	raw := `{
			"next_batch": "token123",
			"rooms": {
				"join": {
					"!room1:matrix.org": {
						"timeline": {
							"events": [{"type":"m.room.message"}]
						}
					}
				}
			}
		}`

	var sr syncResponse
	if err := json.Unmarshal([]byte(raw), &sr); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if sr.NextBatch != "token123" {
		t.Errorf("NextBatch = %q, want %q", sr.NextBatch, "token123")
	}
	if len(sr.Rooms.Join) != 1 {
		t.Fatalf("Rooms.Join length = %d, want 1", len(sr.Rooms.Join))
	}
	room, ok := sr.Rooms.Join["!room1:matrix.org"]
	if !ok {
		t.Fatal("expected room !room1:matrix.org in join")
	}
	if len(room.Timeline.Events) != 1 {
		t.Errorf("Timeline.Events length = %d, want 1", len(room.Timeline.Events))
	}
}

// ---------------------------------------------------------------------------
// Matrix adapter -- session key construction
// ---------------------------------------------------------------------------

func TestMatrixSessionKeyConstruction(t *testing.T) {
	t.Parallel()

	src := &SessionSource{
		Platform: PlatformMatrix,
		ChatType: "group",
		ChatID:   "!room123:matrix.org",
	}
	key := BuildSessionKey(src)
	expected := "agent:main:matrix:group:!room123:matrix.org"
	if key != expected {
		t.Errorf("BuildSessionKey() = %q, want %q", key, expected)
	}
}
