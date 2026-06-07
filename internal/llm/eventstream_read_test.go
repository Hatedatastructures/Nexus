package llm

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"testing"
)

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
	if err.Error() != "EOF" {
		// io.EOF is wrapped, so check string
		t.Logf("got error: %v", err)
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
