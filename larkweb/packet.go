package larkweb

import (
	"fmt"

	"google.golang.org/protobuf/encoding/protowire"
)

// Packet is the envelope every gateway call is wrapped in. The field numbers
// are the web client's own, and only these five are needed: the rest of the
// envelope carries the long-connection's concerns (pipes, cursors, retry
// intervals) that an HTTP request answers by returning.
const (
	fieldPacketPayloadType protowire.Number = 2
	fieldPacketCmd         protowire.Number = 3
	fieldPacketStatus      protowire.Number = 4
	fieldPacketPayload     protowire.Number = 5
	fieldPacketCID         protowire.Number = 6
)

// payloadTypePB2 selects protobuf for the inner payload. The enum's other
// values are unknown and JSON, neither of which the gateway takes here.
const payloadTypePB2 = 1

// Payload is one request the gateway understands: the command that routes it
// and its own encoding. Adding a second command means adding an implementation
// of this, not touching the envelope.
type Payload interface {
	Cmd() int32
	Encode() []byte
}

// encodePacket wraps a payload for the gateway. Fields go out in ascending
// number so the bytes are reproducible and a golden test can pin them.
func encodePacket(cid string, p Payload) []byte {
	var b []byte
	b = protowire.AppendTag(b, fieldPacketPayloadType, protowire.VarintType)
	b = protowire.AppendVarint(b, payloadTypePB2)
	b = protowire.AppendTag(b, fieldPacketCmd, protowire.VarintType)
	b = protowire.AppendVarint(b, uint64(p.Cmd()))
	b = protowire.AppendTag(b, fieldPacketPayload, protowire.BytesType)
	b = protowire.AppendBytes(b, p.Encode())
	b = protowire.AppendTag(b, fieldPacketCID, protowire.BytesType)
	b = protowire.AppendString(b, cid)
	return b
}

// decodeReply reads the reply envelope's status and payload. An absent status
// field is a zero status, which is success: the gateway omits it on the happy
// path.
func decodeReply(b []byte) (status uint32, payload []byte, err error) {
	err = eachField(b, func(num protowire.Number, typ protowire.Type, v []byte) error {
		switch {
		case num == fieldPacketStatus && typ == protowire.VarintType:
			x, _ := protowire.ConsumeVarint(v)
			status = uint32(x)
		case num == fieldPacketPayload && typ == protowire.BytesType:
			payload, _ = protowire.ConsumeBytes(v)
		}
		return nil
	})
	if err != nil {
		return 0, nil, fmt.Errorf("gateway reply: %w", err)
	}
	return status, payload, nil
}

// eachField walks a message's fields, handing each one's raw value bytes to
// fn. Every field is stepped over by its own length rather than skipped to the
// next tag: a tag byte is indistinguishable from payload bytes.
func eachField(b []byte, fn func(num protowire.Number, typ protowire.Type, v []byte) error) error {
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return protowire.ParseError(n)
		}
		b = b[n:]
		n = protowire.ConsumeFieldValue(num, typ, b)
		if n < 0 {
			return fmt.Errorf("field %d: %w", num, protowire.ParseError(n))
		}
		if err := fn(num, typ, b[:n]); err != nil {
			return err
		}
		b = b[n:]
	}
	return nil
}
