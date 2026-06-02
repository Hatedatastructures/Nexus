package llm

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"io"
	"testing"
)

// buildFrame constructs a valid binary eventstream frame with given headers and payload.
func buildFrame(t *testing.T, headers map[string]string, payload []byte) []byte {
	t.Helper()

	hdrData := buildHeaderBytes(t, headers)

	totalLen := uint32(8 + 4 + len(hdrData) + len(payload) + 4)
	hdrLen := uint32(len(hdrData))

	prelude := make([]byte, 8)
	binary.BigEndian.PutUint32(prelude[0:4], totalLen)
	binary.BigEndian.PutUint32(prelude[4:8], hdrLen)

	preludeCRC := make([]byte, 4)
	binary.BigEndian.PutUint32(preludeCRC, crc32.ChecksumIEEE(prelude))

	h := crc32.NewIEEE()
	h.Write(prelude)
	h.Write(preludeCRC)
	h.Write(hdrData)
	h.Write(payload)
	msgCRC := make([]byte, 4)
	binary.BigEndian.PutUint32(msgCRC, h.Sum32())

	var buf bytes.Buffer
	buf.Write(prelude)
	buf.Write(preludeCRC)
	buf.Write(hdrData)
	buf.Write(payload)
	buf.Write(msgCRC)

	return buf.Bytes()
}

// buildHeaderBytes encodes headers into binary eventstream header format.
func buildHeaderBytes(t *testing.T, headers map[string]string) []byte {
	t.Helper()
	if len(headers) == 0 {
		return nil
	}

	var buf bytes.Buffer
	for name, val := range headers {
		buf.WriteByte(byte(len(name)))
		buf.WriteString(name)
		buf.WriteByte(7) // string type
		binary.Write(&buf, binary.BigEndian, uint16(len(val)))
		buf.WriteString(val)
	}
	return buf.Bytes()
}

// ── parseEventStreamHeaders ─────────────────────────────────────────────────

func TestParseEventStreamHeaders_Empty(t *testing.T) {
	headers, err := parseEventStreamHeaders(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(headers) != 0 {
		t.Errorf("expected empty headers, got %d", len(headers))
	}
}

func TestParseEventStreamHeaders_SingleString(t *testing.T) {
	var buf bytes.Buffer
	name := ":event-type"
	val := "contentBlockDelta"
	buf.WriteByte(byte(len(name)))
	buf.WriteString(name)
	buf.WriteByte(7) // string type
	binary.Write(&buf, binary.BigEndian, uint16(len(val)))
	buf.WriteString(val)

	headers, err := parseEventStreamHeaders(buf.Bytes())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if headers[":event-type"] != "contentBlockDelta" {
		t.Errorf(":event-type = %q, want contentBlockDelta", headers[":event-type"])
	}
}

func TestParseEventStreamHeaders_MultipleHeaders(t *testing.T) {
	var buf bytes.Buffer

	for _, kv := range []struct{ k, v string }{
		{":event-type", "messageStart"},
		{":message-type", "event"},
		{":content-type", "application/json"},
	} {
		buf.WriteByte(byte(len(kv.k)))
		buf.WriteString(kv.k)
		buf.WriteByte(7)
		binary.Write(&buf, binary.BigEndian, uint16(len(kv.v)))
		buf.WriteString(kv.v)
	}

	headers, err := parseEventStreamHeaders(buf.Bytes())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(headers) != 3 {
		t.Fatalf("expected 3 headers, got %d", len(headers))
	}
	if headers[":event-type"] != "messageStart" {
		t.Errorf(":event-type = %q", headers[":event-type"])
	}
	if headers[":message-type"] != "event" {
		t.Errorf(":message-type = %q", headers[":message-type"])
	}
	if headers[":content-type"] != "application/json" {
		t.Errorf(":content-type = %q", headers[":content-type"])
	}
}

func TestParseEventStreamHeaders_UUIDType(t *testing.T) {
	var buf bytes.Buffer
	name := ":request-id"
	uuid := make([]byte, 16)
	for i := range uuid {
		uuid[i] = byte(i)
	}
	buf.WriteByte(byte(len(name)))
	buf.WriteString(name)
	buf.WriteByte(8) // UUID type
	buf.Write(uuid)

	headers, err := parseEventStreamHeaders(buf.Bytes())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if headers[":request-id"] != "000102030405060708090a0b0c0d0e0f" {
		t.Errorf(":request-id = %q, want hex uuid", headers[":request-id"])
	}
}

func TestParseEventStreamHeaders_UnsupportedType(t *testing.T) {
	var buf bytes.Buffer
	name := "foo"
	buf.WriteByte(byte(len(name)))
	buf.WriteString(name)
	buf.WriteByte(3) // unsupported type

	_, err := parseEventStreamHeaders(buf.Bytes())
	if err == nil {
		t.Fatal("expected error for unsupported header type")
	}
}

func TestParseEventStreamHeaders_NameOverflow(t *testing.T) {
	data := []byte{10} // name length = 10 but only 1 byte available
	_, err := parseEventStreamHeaders(data)
	if err == nil {
		t.Fatal("expected error for name overflow")
	}
}

func TestParseEventStreamHeaders_TypeOverflow(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte(3)
	buf.WriteString("abc")
	// no type byte
	_, err := parseEventStreamHeaders(buf.Bytes())
	if err == nil {
		t.Fatal("expected error for type overflow")
	}
}

func TestParseEventStreamHeaders_StringLengthOverflow(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte(1)
	buf.WriteString("x")
	buf.WriteByte(7) // string type
	// no string length bytes
	_, err := parseEventStreamHeaders(buf.Bytes())
	if err == nil {
		t.Fatal("expected error for string length overflow")
	}
}

func TestParseEventStreamHeaders_StringValueOverflow(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte(1)
	buf.WriteString("x")
	buf.WriteByte(7)
	binary.Write(&buf, binary.BigEndian, uint16(100)) // claims 100 bytes but none follow
	_, err := parseEventStreamHeaders(buf.Bytes())
	if err == nil {
		t.Fatal("expected error for string value overflow")
	}
}

func TestParseEventStreamHeaders_UUIDOverflow(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte(1)
	buf.WriteString("x")
	buf.WriteByte(8) // UUID type
	// only 4 bytes instead of 16
	buf.Write([]byte{0, 0, 0, 0})
	_, err := parseEventStreamHeaders(buf.Bytes())
	if err == nil {
		t.Fatal("expected error for UUID overflow")
	}
}

// ── getEventHeader ──────────────────────────────────────────────────────────

func TestGetEventHeader_NilHeaders(t *testing.T) {
	msg := &eventStreamFrame{}
	if got := getEventHeader(msg, "key"); got != "" {
		t.Errorf("expected empty string for nil headers, got %q", got)
	}
}

func TestGetEventHeader_Found(t *testing.T) {
	msg := &eventStreamFrame{Headers: map[string]string{"key": "value"}}
	if got := getEventHeader(msg, "key"); got != "value" {
		t.Errorf("got %q, want value", got)
	}
}

func TestGetEventHeader_NotFound(t *testing.T) {
	msg := &eventStreamFrame{Headers: map[string]string{"other": "val"}}
	if got := getEventHeader(msg, "key"); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

// ── readEventStreamFrame ───────────────────────────────────────────────────

func TestReadEventStreamFrame_ValidFrame(t *testing.T) {
	headers := map[string]string{
		":event-type":   "messageStart",
		":message-type": "event",
	}
	payload := []byte(`{"role":"assistant"}`)

	frame := buildFrame(t, headers, payload)
	msg, err := readEventStreamFrame(bytes.NewReader(frame))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Headers[":event-type"] != "messageStart" {
		t.Errorf(":event-type = %q, want messageStart", msg.Headers[":event-type"])
	}
	if string(msg.Payload) != `{"role":"assistant"}` {
		t.Errorf("payload = %q, want JSON", string(msg.Payload))
	}
}

func TestReadEventStreamFrame_EmptyPayload(t *testing.T) {
	headers := map[string]string{":event-type": "ping"}
	frame := buildFrame(t, headers, nil)

	msg, err := readEventStreamFrame(bytes.NewReader(frame))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msg.Payload) != 0 {
		t.Errorf("expected empty payload, got %d bytes", len(msg.Payload))
	}
}

func TestReadEventStreamFrame_NoHeaders(t *testing.T) {
	frame := buildFrame(t, nil, []byte("data"))

	msg, err := readEventStreamFrame(bytes.NewReader(frame))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msg.Headers) != 0 {
		t.Errorf("expected no headers, got %d", len(msg.Headers))
	}
	if string(msg.Payload) != "data" {
		t.Errorf("payload = %q, want data", string(msg.Payload))
	}
}

func TestReadEventStreamFrame_EOF(t *testing.T) {
	_, err := readEventStreamFrame(bytes.NewReader(nil))
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}
}

func TestReadEventStreamFrame_TruncatedPreludeCRC(t *testing.T) {
	prelude := make([]byte, 8)
	binary.BigEndian.PutUint32(prelude[0:4], 20)
	binary.BigEndian.PutUint32(prelude[4:8], 0)
	// only 2 bytes of CRC instead of 4
	_, err := readEventStreamFrame(bytes.NewReader(append(prelude, 0, 0)))
	if err == nil {
		t.Fatal("expected error for truncated prelude CRC")
	}
}

func TestReadEventStreamFrame_PreludeCRCMismatch(t *testing.T) {
	prelude := make([]byte, 8)
	binary.BigEndian.PutUint32(prelude[0:4], 20)
	binary.BigEndian.PutUint32(prelude[4:8], 0)

	wrongCRC := make([]byte, 4)
	binary.BigEndian.PutUint32(wrongCRC, 0xDEADBEEF)

	var buf bytes.Buffer
	buf.Write(prelude)
	buf.Write(wrongCRC)

	_, err := readEventStreamFrame(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Fatal("expected error for prelude CRC mismatch")
	}
}

func TestReadEventStreamFrame_InvalidTotalLengthTooSmall(t *testing.T) {
	prelude := make([]byte, 8)
	binary.BigEndian.PutUint32(prelude[0:4], 10) // too small (<16)
	binary.BigEndian.PutUint32(prelude[4:8], 0)

	preludeCRC := make([]byte, 4)
	binary.BigEndian.PutUint32(preludeCRC, crc32.ChecksumIEEE(prelude))

	var buf bytes.Buffer
	buf.Write(prelude)
	buf.Write(preludeCRC)

	_, err := readEventStreamFrame(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Fatal("expected error for invalid total length")
	}
}

func TestReadEventStreamFrame_TotalLengthTooLarge(t *testing.T) {
	prelude := make([]byte, 8)
	binary.BigEndian.PutUint32(prelude[0:4], 17<<20) // exceeds 16MB limit
	binary.BigEndian.PutUint32(prelude[4:8], 0)

	preludeCRC := make([]byte, 4)
	binary.BigEndian.PutUint32(preludeCRC, crc32.ChecksumIEEE(prelude))

	var buf bytes.Buffer
	buf.Write(prelude)
	buf.Write(preludeCRC)

	_, err := readEventStreamFrame(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Fatal("expected error for total length too large")
	}
}

func TestReadEventStreamFrame_MessageCRCMismatch(t *testing.T) {
	totalLen := uint32(8 + 4 + 0 + 4 + 4) // 20
	hdrLen := uint32(0)
	payload := []byte("test")

	prelude := make([]byte, 8)
	binary.BigEndian.PutUint32(prelude[0:4], totalLen)
	binary.BigEndian.PutUint32(prelude[4:8], hdrLen)

	preludeCRC := make([]byte, 4)
	binary.BigEndian.PutUint32(preludeCRC, crc32.ChecksumIEEE(prelude))

	wrongMsgCRC := make([]byte, 4)
	binary.BigEndian.PutUint32(wrongMsgCRC, 0xBADBAD)

	var buf bytes.Buffer
	buf.Write(prelude)
	buf.Write(preludeCRC)
	buf.Write(payload)
	buf.Write(wrongMsgCRC)

	_, err := readEventStreamFrame(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Fatal("expected error for message CRC mismatch")
	}
}

func TestReadEventStreamFrame_TruncatedHeaders(t *testing.T) {
	totalLen := uint32(8 + 4 + 100 + 0 + 4) // claims 100 bytes of headers
	hdrLen := uint32(100)

	prelude := make([]byte, 8)
	binary.BigEndian.PutUint32(prelude[0:4], totalLen)
	binary.BigEndian.PutUint32(prelude[4:8], hdrLen)

	preludeCRC := make([]byte, 4)
	binary.BigEndian.PutUint32(preludeCRC, crc32.ChecksumIEEE(prelude))

	var buf bytes.Buffer
	buf.Write(prelude)
	buf.Write(preludeCRC)
	// no header data or message CRC

	_, err := readEventStreamFrame(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Fatal("expected error for truncated headers")
	}
}

func TestReadEventStreamFrame_TruncatedPayload(t *testing.T) {
	totalLen := uint32(8 + 4 + 0 + 100 + 4) // claims 100 bytes payload
	hdrLen := uint32(0)

	prelude := make([]byte, 8)
	binary.BigEndian.PutUint32(prelude[0:4], totalLen)
	binary.BigEndian.PutUint32(prelude[4:8], hdrLen)

	preludeCRC := make([]byte, 4)
	binary.BigEndian.PutUint32(preludeCRC, crc32.ChecksumIEEE(prelude))

	var buf bytes.Buffer
	buf.Write(prelude)
	buf.Write(preludeCRC)
	// no payload or message CRC

	_, err := readEventStreamFrame(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Fatal("expected error for truncated payload")
	}
}

func TestReadEventStreamFrame_TruncatedMessageCRC(t *testing.T) {
	totalLen := uint32(8 + 4 + 0 + 0 + 4) // 20 bytes total
	hdrLen := uint32(0)

	prelude := make([]byte, 8)
	binary.BigEndian.PutUint32(prelude[0:4], totalLen)
	binary.BigEndian.PutUint32(prelude[4:8], hdrLen)

	preludeCRC := make([]byte, 4)
	binary.BigEndian.PutUint32(preludeCRC, crc32.ChecksumIEEE(prelude))

	var buf bytes.Buffer
	buf.Write(prelude)
	buf.Write(preludeCRC)
	// no message CRC

	_, err := readEventStreamFrame(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Fatal("expected error for missing message CRC")
	}
}

func TestReadEventStreamFrame_InvalidPayloadLength(t *testing.T) {
	totalLen := uint32(8 + 4 + 0 + 0 + 4)
	hdrLen := uint32(30) // hdrLen > totalLen - 16, causing negative payload

	prelude := make([]byte, 8)
	binary.BigEndian.PutUint32(prelude[0:4], totalLen)
	binary.BigEndian.PutUint32(prelude[4:8], hdrLen)

	preludeCRC := make([]byte, 4)
	binary.BigEndian.PutUint32(preludeCRC, crc32.ChecksumIEEE(prelude))

	var buf bytes.Buffer
	buf.Write(prelude)
	buf.Write(preludeCRC)

	_, err := readEventStreamFrame(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Fatal("expected error for invalid payload length")
	}
}

// ── ParseBinaryEventStream ──────────────────────────────────────────────────

func TestParseBinaryEventStream_SingleFrame(t *testing.T) {
	headers := map[string]string{
		":event-type":   "contentBlockDelta",
		":message-type": "event",
	}
	payload := []byte(`{"delta":{"text":"hello"}}`)
	frame := buildFrame(t, headers, payload)

	ch := ParseBinaryEventStream(context.Background(), bytes.NewReader(frame))

	var events []*SSEEvent
	for e := range ch {
		events = append(events, e)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Event != "contentBlockDelta" {
		t.Errorf("Event = %q, want contentBlockDelta", events[0].Event)
	}
	if events[0].Data != `{"delta":{"text":"hello"}}` {
		t.Errorf("Data = %q", events[0].Data)
	}
}

func TestParseBinaryEventStream_MultipleFrames(t *testing.T) {
	var allBytes bytes.Buffer

	for i := 0; i < 3; i++ {
		headers := map[string]string{
			":event-type":   "contentBlockDelta",
			":message-type": "event",
		}
		frame := buildFrame(t, headers, []byte("chunk"))
		allBytes.Write(frame)
	}

	ch := ParseBinaryEventStream(context.Background(), bytes.NewReader(allBytes.Bytes()))
	var count int
	for range ch {
		count++
	}
	if count != 3 {
		t.Errorf("expected 3 events, got %d", count)
	}
}

func TestParseBinaryEventStream_ErrorFrameSkipped(t *testing.T) {
	errHeaders := map[string]string{
		":message-type": "error",
		":error-code":   "ThrottlingException",
	}
	errFrame := buildFrame(t, errHeaders, []byte(`{"message":"too many requests"}`))

	okHeaders := map[string]string{
		":event-type":   "messageStart",
		":message-type": "event",
	}
	okFrame := buildFrame(t, okHeaders, []byte(`{"role":"assistant"}`))

	var buf bytes.Buffer
	buf.Write(errFrame)
	buf.Write(okFrame)

	ch := ParseBinaryEventStream(context.Background(), bytes.NewReader(buf.Bytes()))
	var events []*SSEEvent
	for e := range ch {
		events = append(events, e)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event (error frame skipped), got %d", len(events))
	}
	if events[0].Event != "messageStart" {
		t.Errorf("Event = %q, want messageStart", events[0].Event)
	}
}

func TestParseBinaryEventStream_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	headers := map[string]string{":event-type": "test", ":message-type": "event"}
	frame := buildFrame(t, headers, []byte("data"))

	ch := ParseBinaryEventStream(ctx, bytes.NewReader(frame))

	// Read the first event
	<-ch

	// Cancel context to stop the goroutine
	cancel()

	// Channel should close
	_, ok := <-ch
	if ok {
		t.Error("expected channel to be closed after context cancel")
	}
}

func TestParseBinaryEventStream_EmptyReader(t *testing.T) {
	ch := ParseBinaryEventStream(context.Background(), bytes.NewReader(nil))

	var events []*SSEEvent
	for e := range ch {
		events = append(events, e)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events for empty reader, got %d", len(events))
	}
}

func TestParseBinaryEventStream_InvalidFrameStops(t *testing.T) {
	// Send garbage data that will fail prelude CRC check
	ch := ParseBinaryEventStream(context.Background(), bytes.NewReader(make([]byte, 20)))

	var events []*SSEEvent
	for e := range ch {
		events = append(events, e)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events for invalid data, got %d", len(events))
	}
}
