package llm

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
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
		_ = binary.Write(&buf, binary.BigEndian, uint16(len(val)))
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
	_ = binary.Write(&buf, binary.BigEndian, uint16(len(val)))
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
		_ = binary.Write(&buf, binary.BigEndian, uint16(len(kv.v)))
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
	_ = binary.Write(&buf, binary.BigEndian, uint16(100)) // claims 100 bytes but none follow
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
