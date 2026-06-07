package llm

import (
	"context"
	"encoding/binary"
	"fmt"

	"hash/crc32"
	"io"
	"log/slog"
	pkgerrors "nexus-agent/internal/errors"
)

// ParseBinaryEventStream 读取 AWS 二进制 eventstream 帧并转换为 SSEEvent。
// 用于 Bedrock ConverseStream 响应，其使用二进制帧格式而非文本 SSE。
func ParseBinaryEventStream(ctx context.Context, r io.Reader) <-chan *SSEEvent {
	ch := make(chan *SSEEvent, 64)

	go func() {
		defer close(ch)

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			msg, err := readEventStreamFrame(r)
			if err != nil {
				if err != io.EOF {
					slog.Debug("binary eventstream read error", "error", err)
				}
				return
			}

			if getEventHeader(msg, ":message-type") == "error" {
				slog.Debug("binary eventstream error frame",
					"code", getEventHeader(msg, ":error-code"),
					"payload", string(msg.Payload))
				continue
			}

			event := &SSEEvent{
				Event: getEventHeader(msg, ":event-type"),
				Data:  string(msg.Payload),
			}

			select {
			case ch <- event:
			case <-ctx.Done():
				return
			}
		}
	}()

	return ch
}

// eventStreamFrame 表示解码后的二进制 eventstream 帧。
type eventStreamFrame struct {
	Headers map[string]string
	Payload []byte
}

// readEventStreamFrame 从读取器中解析单个二进制 eventstream 帧。
//
// 帧格式:
//
//	[4 bytes: total length][4 bytes: headers length][4 bytes: prelude CRC]
//	[headers...][payload...][4 bytes: message CRC]
func readEventStreamFrame(r io.Reader) (*eventStreamFrame, error) {
	prelude := make([]byte, 8)
	if _, err := io.ReadFull(r, prelude); err != nil {
		return nil, err
	}

	totalLen := binary.BigEndian.Uint32(prelude[0:4])
	hdrLen := binary.BigEndian.Uint32(prelude[4:8])

	preludeCRC := make([]byte, 4)
	if _, err := io.ReadFull(r, preludeCRC); err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ProviderAPI, "eventstream: prelude CRC", err)
	}

	if crc32.ChecksumIEEE(prelude) != binary.BigEndian.Uint32(preludeCRC) {
		return nil, pkgerrors.New(pkgerrors.ProviderAPI, "eventstream: prelude CRC mismatch")
	}

	if totalLen < 16 || totalLen > 16<<20 {
		return nil, pkgerrors.New(pkgerrors.ProviderAPI, fmt.Sprintf("eventstream: invalid total length %d", totalLen))
	}

	payloadLen := int(totalLen) - int(hdrLen) - 16
	if payloadLen < 0 || payloadLen > 16<<20 {
		return nil, pkgerrors.New(pkgerrors.ProviderAPI, fmt.Sprintf("eventstream: invalid payload length %d", payloadLen))
	}

	var hdrData []byte
	if hdrLen > 0 {
		hdrData = make([]byte, hdrLen)
		if _, err := io.ReadFull(r, hdrData); err != nil {
			return nil, pkgerrors.Wrap(pkgerrors.ProviderAPI, "eventstream: headers", err)
		}
	}

	var payload []byte
	if payloadLen > 0 {
		payload = make([]byte, payloadLen)
		if _, err := io.ReadFull(r, payload); err != nil {
			return nil, pkgerrors.Wrap(pkgerrors.ProviderAPI, "eventstream: payload", err)
		}
	}

	msgCRC := make([]byte, 4)
	if _, err := io.ReadFull(r, msgCRC); err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ProviderAPI, "eventstream: message CRC", err)
	}

	h := crc32.NewIEEE()
	h.Write(prelude)
	h.Write(preludeCRC)
	if hdrData != nil {
		h.Write(hdrData)
	}
	if payload != nil {
		h.Write(payload)
	}
	if h.Sum32() != binary.BigEndian.Uint32(msgCRC) {
		return nil, pkgerrors.New(pkgerrors.ProviderAPI, "eventstream: message CRC mismatch")
	}

	headers, err := parseEventStreamHeaders(hdrData)
	if err != nil {
		return nil, pkgerrors.Wrap(pkgerrors.ProviderAPI, "eventstream: header parse", err)
	}

	return &eventStreamFrame{Headers: headers, Payload: payload}, nil
}

// parseEventStreamHeaders 解析二进制 eventstream 头部。
func parseEventStreamHeaders(data []byte) (map[string]string, error) {
	headers := make(map[string]string)
	off := 0

	for off < len(data) {
		nameLen := int(data[off])
		off++
		if off+nameLen > len(data) {
			return headers, pkgerrors.New(pkgerrors.ProviderAPI, fmt.Sprintf("header name overflow at offset %d", off))
		}
		name := string(data[off : off+nameLen])
		off += nameLen

		if off >= len(data) {
			return headers, pkgerrors.New(pkgerrors.ProviderAPI, "header type overflow")
		}
		vtype := data[off]
		off++

		var val string
		switch vtype {
		case 7: // String
			if off+2 > len(data) {
				return headers, pkgerrors.New(pkgerrors.ProviderAPI, "string header length overflow")
			}
			slen := int(binary.BigEndian.Uint16(data[off : off+2]))
			off += 2
			if off+slen > len(data) {
				return headers, pkgerrors.New(pkgerrors.ProviderAPI, "string header value overflow")
			}
			val = string(data[off : off+slen])
			off += slen

		case 8: // UUID
			if off+16 > len(data) {
				return headers, pkgerrors.New(pkgerrors.ProviderAPI, "UUID header overflow")
			}
			val = fmt.Sprintf("%x", data[off:off+16])
			off += 16

		default:
			return headers, pkgerrors.New(pkgerrors.ProviderAPI, fmt.Sprintf("unsupported header type %d for %s", vtype, name))
		}

		headers[name] = val
	}

	return headers, nil
}

func getEventHeader(msg *eventStreamFrame, name string) string {
	if msg.Headers == nil {
		return ""
	}
	return msg.Headers[name]
}
