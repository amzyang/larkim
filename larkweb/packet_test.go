package larkweb

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protowire"
)

// The bytes are pinned rather than round-tripped: a field number that drifts
// would round-trip perfectly and still be refused by the gateway. These match
// the web client's own descriptor — envelope payloadType=2, cmd=3, payload=5,
// cid=6, and PutReadMessagesRequest chatId=1, messageIds=2, maxPosition=3.
func TestPacket_EncodesTheReadWatermark(t *testing.T) {
	got := encodePacket("cid1", readRequest{chatID: "oc_test", maxPosition: 7})

	const want = "10" + "01" + // payloadType = PB2
		"18" + "28" + // cmd = 40
		"2a" + "0b" + // payload, 11 bytes
		"0a" + "07" + "6f635f74657374" + // chatId = "oc_test"
		"18" + "07" + // maxPosition = 7
		"32" + "04" + "63696431" // cid = "cid1"
	require.Equal(t, want, hex.EncodeToString(got))
}

func TestDecodeReply_TakesAnAbsentStatusAsSuccess(t *testing.T) {
	// The gateway omits status on the happy path, so a reply carrying only
	// counts has to read as accepted rather than as a malformed envelope.
	reply := protowire.AppendTag(nil, fieldPacketCmd, protowire.VarintType)
	reply = protowire.AppendVarint(reply, 40)

	status, _, err := decodeReply(reply)
	require.NoError(t, err)
	require.Zero(t, status)
}

func TestDecodeReply_ReadsAStatusPastFieldsItDoesNotKnow(t *testing.T) {
	// A length-delimited field before the status is the case that breaks a
	// decoder which scans for tags instead of stepping over values: payload
	// bytes are indistinguishable from tags.
	reply := protowire.AppendTag(nil, fieldPacketPayload, protowire.BytesType)
	reply = protowire.AppendBytes(reply, []byte{0x20, 0x63, 0x0a, 0xff})
	reply = protowire.AppendTag(reply, fieldPacketStatus, protowire.VarintType)
	reply = protowire.AppendVarint(reply, 99)

	status, _, err := decodeReply(reply)
	require.NoError(t, err)
	require.Equal(t, uint32(99), status)
}

func TestDecodeReply_RejectsTruncatedBytes(t *testing.T) {
	reply := protowire.AppendTag(nil, fieldPacketPayload, protowire.BytesType)
	reply = protowire.AppendBytes(reply, []byte{0x01, 0x02, 0x03})

	_, _, err := decodeReply(reply[:len(reply)-2])
	require.Error(t, err)
}

// payloadOf pulls the inner payload out of an envelope.
func payloadOf(t *testing.T, packet []byte) []byte {
	t.Helper()
	_, payload, err := decodeReply(packet)
	require.NoError(t, err)
	require.NotNil(t, payload, "envelope carried no payload")
	return payload
}
