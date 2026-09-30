package larkweb

import "google.golang.org/protobuf/encoding/protowire"

// cmdPutReadMessages is the command a read watermark is routed by. It travels
// twice — in the envelope and in the x-command header — because the gateway
// routes on the header before it parses the body.
const cmdPutReadMessages int32 = 40

// PutReadMessagesRequest's field numbers. messageIds (2) names messages one
// by one, which the watermark makes redundant. threadId (4) and
// threadMaxPosition (5) would settle a thread's own dot, which no applink can
// reach; they are left out until the badge count means to include thread
// replies.
const (
	fieldReadChatID      protowire.Number = 1
	fieldReadMaxPosition protowire.Number = 3
)

// readRequest settles a chat's unread messages up to a position.
type readRequest struct {
	chatID      string
	maxPosition int32
}

func (readRequest) Cmd() int32 { return cmdPutReadMessages }

func (r readRequest) Encode() []byte {
	var b []byte
	b = protowire.AppendTag(b, fieldReadChatID, protowire.BytesType)
	b = protowire.AppendString(b, r.chatID)
	// Emitted even at zero: the field carries explicit presence, so an absent
	// watermark and a watermark of nothing are different requests.
	b = protowire.AppendTag(b, fieldReadMaxPosition, protowire.VarintType)
	b = protowire.AppendVarint(b, uint64(r.maxPosition))
	return b
}
