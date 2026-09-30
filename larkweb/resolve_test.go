package larkweb

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protowire"
)

// fakeStore places messages the way the store would, keyed by the pair the
// two id spaces share, and keeps what was matched.
type fakeStore struct {
	chats map[[2]int64]string
	web   map[string]string
	err   error
}

func newFakeStore(chats map[[2]int64]string) *fakeStore {
	return &fakeStore{chats: chats, web: map[string]string{}}
}

func (f *fakeStore) ChatAt(_ context.Context, createMs, position int64) (string, bool, error) {
	if f.err != nil {
		return "", false, f.err
	}
	id, ok := f.chats[[2]int64{createMs, position}]
	return id, ok, nil
}

func (f *fakeStore) WebChatID(_ context.Context, chatID string) (string, error) {
	return f.web[chatID], nil
}

func (f *fakeStore) SetWebChatIDs(_ context.Context, ids map[string]string) error {
	maps.Copy(f.web, ids)
	return nil
}

// clock is a Resolver's time, moved by hand.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

// gateway answers the two commands a resolved mark-read sends, recording the
// chat each read was addressed to and how often the inbox was listed.
type gateway struct {
	mu    sync.Mutex
	inbox []LastMessage
	// boxed are chats folded into one box, listed only under its parent.
	boxed  []LastMessage
	lists  int
	readAt []string
}

// boxID is the stub's one box card.
const boxID = 7500

func (g *gateway) handle(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		switch r.Header.Get("x-command") {
		case "1000":
			parent := fieldVarintOr(t, payloadOf(t, readAll(t, r.Body)), fieldFeedParentCardID)
			if parent == boxID {
				w.Write(replyWith(feedPayload(g.boxed)))
				return
			}
			g.lists++
			var boxes []int64
			if len(g.boxed) > 0 {
				boxes = []int64{boxID}
			}
			w.Write(replyWith(feedPayload(g.inbox, boxes...)))
		case "40":
			g.readAt = append(g.readAt, fieldString(t, payloadOf(t, readAll(t, r.Body)), fieldReadChatID))
			w.Write(replyWith(nil))
		default:
			t.Errorf("unexpected command %s", r.Header.Get("x-command"))
		}
	}
}

func replyWith(payload []byte) []byte {
	b := protowire.AppendTag(nil, fieldPacketPayload, protowire.BytesType)
	return protowire.AppendBytes(b, payload)
}

// feedPayload builds a PullFeedCardsResponse holding each chat's last message
// and a box card for each of boxes.
func feedPayload(last []LastMessage, boxes ...int64) []byte {
	var b []byte
	for _, box := range boxes {
		var card []byte
		card = protowire.AppendTag(card, fieldCardID, protowire.BytesType)
		card = protowire.AppendString(card, strconv.FormatInt(box, 10))
		card = protowire.AppendTag(card, fieldCardType, protowire.VarintType)
		card = protowire.AppendVarint(card, cardTypeBox)
		b = protowire.AppendTag(b, fieldFeedCards, protowire.BytesType)
		b = protowire.AppendBytes(b, card)
	}
	for _, m := range last {
		var msg []byte
		msg = protowire.AppendTag(msg, fieldMessageChatID, protowire.BytesType)
		msg = protowire.AppendString(msg, m.ChatID)
		msg = protowire.AppendTag(msg, fieldMessagePosition, protowire.VarintType)
		msg = protowire.AppendVarint(msg, uint64(int32(m.Position)))
		msg = protowire.AppendTag(msg, fieldMessageCreateTimeMs, protowire.VarintType)
		msg = protowire.AppendVarint(msg, uint64(m.CreateMs))
		var entry []byte
		entry = protowire.AppendTag(entry, 1, protowire.BytesType)
		entry = protowire.AppendString(entry, "k")
		entry = protowire.AppendTag(entry, 2, protowire.BytesType)
		entry = protowire.AppendBytes(entry, msg)
		b = protowire.AppendTag(b, fieldFeedMessages, protowire.BytesType)
		b = protowire.AppendBytes(b, entry)
	}
	return b
}

func resolverTo(t *testing.T, g *gateway, st Store) (*Resolver, *clock) {
	t.Helper()
	c := &clock{t: time.Unix(1_000_000, 0)}
	return &Resolver{Client: clientTo(t, g.handle(t), sessionJar()), Store: st, Now: c.now}, c
}

func TestResolver_MarksTheChatReadByItsWebID(t *testing.T) {
	g := &gateway{inbox: []LastMessage{{ChatID: "7001", Position: 126, CreateMs: 5000}}}
	r, _ := resolverTo(t, g, newFakeStore(map[[2]int64]string{{5000, 126}: "oc_quiet"}))

	require.NoError(t, r.MarkRead(t.Context(), "oc_quiet", 126))

	require.Equal(t, []string{"7001"}, g.readAt, "the gateway takes the numeric id, never the oc_ one")
}

func TestResolver_KeepsEveryChatOneListingMatched(t *testing.T) {
	// A sweep is many chats; one listing answers all it can place, and what
	// it answered is kept for every later sweep too.
	g := &gateway{inbox: []LastMessage{
		{ChatID: "7001", Position: 1, CreateMs: 10},
		{ChatID: "7002", Position: 2, CreateMs: 20},
	}}
	st := newFakeStore(map[[2]int64]string{{10, 1}: "oc_a", {20, 2}: "oc_b"})
	r, _ := resolverTo(t, g, st)

	require.NoError(t, r.MarkRead(t.Context(), "oc_a", 1))
	require.NoError(t, r.MarkRead(t.Context(), "oc_b", 2))

	require.Equal(t, 1, g.lists)
	require.Equal(t, map[string]string{"oc_a": "7001", "oc_b": "7002"}, st.web)
	require.Equal(t, []string{"7001", "7002"}, g.readAt)
}

func TestResolver_AKeptIDOutlivesTheInbox(t *testing.T) {
	// A chat moved to Done leaves the inbox, and one whose newest message is
	// not synced yet cannot be placed from it; either way the id matched
	// earlier still addresses it, without listing anything.
	g := &gateway{}
	st := newFakeStore(nil)
	st.web["oc_done"] = "7009"
	r, _ := resolverTo(t, g, st)

	require.NoError(t, r.MarkRead(t.Context(), "oc_done", 4))

	require.Zero(t, g.lists)
	require.Equal(t, []string{"7009"}, g.readAt)
}

func TestResolver_RefusesAChatItCannotMatch(t *testing.T) {
	// Guessing an id would mark some other chat read, so nothing is sent.
	g := &gateway{inbox: []LastMessage{{ChatID: "7001", Position: 1, CreateMs: 10}}}
	r, _ := resolverTo(t, g, newFakeStore(map[[2]int64]string{{10, 1}: "oc_a"}))

	err := r.MarkRead(t.Context(), "oc_unmatched", 4)

	require.ErrorIs(t, err, errNotInInbox)
	require.Empty(t, g.readAt)
}

func TestResolver_ASweepOfMissesListsOnce(t *testing.T) {
	// Every chat the inbox cannot place would otherwise list all of it again,
	// which is a sweep of misses costing a listing each.
	g := &gateway{}
	r, _ := resolverTo(t, g, newFakeStore(nil))

	for _, id := range []string{"oc_a", "oc_b", "oc_c"} {
		require.ErrorIs(t, r.MarkRead(t.Context(), id, 1), errNotInInbox)
	}

	require.Equal(t, 1, g.lists)
}

func TestResolver_ListsAgainOnceThePaceHasPassed(t *testing.T) {
	// A chat that only just got its first message is found by the next
	// listing, which a miss is allowed once relistAfter has gone by.
	g := &gateway{}
	st := newFakeStore(map[[2]int64]string{{30, 1}: "oc_new"})
	r, c := resolverTo(t, g, st)
	require.ErrorIs(t, r.MarkRead(t.Context(), "oc_new", 1), errNotInInbox)

	g.inbox = []LastMessage{{ChatID: "7003", Position: 1, CreateMs: 30}}
	c.t = c.t.Add(relistAfter)
	require.NoError(t, r.MarkRead(t.Context(), "oc_new", 1))

	require.Equal(t, 2, g.lists)
	require.Equal(t, []string{"7003"}, g.readAt)
}

func TestResolver_AFailedListingAnswersTheSweepBehindIt(t *testing.T) {
	// No session means every chat in the sweep fails the same way; listing
	// the inbox again for each would read the jar and hit the gateway per
	// chat for an answer already known.
	g := &gateway{}
	r, _ := resolverTo(t, g, newFakeStore(nil))
	jar := &countingJar{err: &Error{Op: "read cookies", Reason: "no Feishu web session"}}
	r.Client.Cookies = jar

	for _, id := range []string{"oc_a", "oc_b", "oc_c"} {
		require.ErrorContains(t, r.MarkRead(t.Context(), id, 1), "no Feishu web session")
	}

	require.Equal(t, 1, jar.reads, "one failed listing answers the sweep")
}

// countingJar is a jar that fails and counts how often it was asked.
type countingJar struct {
	err   error
	reads int
}

func (j *countingJar) Cookies(context.Context, string) ([]*http.Cookie, error) {
	j.reads++
	return nil, j.err
}

func TestResolver_KeepsNeitherChatWhenTwoWebChatsLandOnOne(t *testing.T) {
	// Two inbox chats whose newest messages share a pair can both be placed
	// on the one local chat that holds it; which of them it is cannot be
	// told, and a wrong pick marks the other chat read for good.
	g := &gateway{inbox: []LastMessage{
		{ChatID: "7001", Position: 5, CreateMs: 10},
		{ChatID: "7002", Position: 5, CreateMs: 10},
	}}
	st := newFakeStore(map[[2]int64]string{{10, 5}: "oc_a"})
	r, _ := resolverTo(t, g, st)

	require.ErrorIs(t, r.MarkRead(t.Context(), "oc_a", 5), errNotInInbox)
	require.Empty(t, st.web)
	require.Empty(t, g.readAt)
}

func TestResolver_SkipsInboxChatsTheStoreCannotPlace(t *testing.T) {
	// Chats older than the local history are the common case in a real inbox,
	// and they must not stop every other chat from being found.
	g := &gateway{inbox: []LastMessage{
		{ChatID: "7000", Position: 9, CreateMs: 1},
		{ChatID: "7001", Position: 1, CreateMs: 10},
	}}
	r, _ := resolverTo(t, g, newFakeStore(map[[2]int64]string{{10, 1}: "oc_a"}))

	require.NoError(t, r.MarkRead(t.Context(), "oc_a", 1))
	require.Equal(t, []string{"7001"}, g.readAt)
}

func TestResolver_AStoreFailureIsNotAMissingChat(t *testing.T) {
	g := &gateway{inbox: []LastMessage{{ChatID: "7001", Position: 1, CreateMs: 10}}}
	boom := errors.New("database is locked")
	st := newFakeStore(nil)
	st.err = boom
	r, _ := resolverTo(t, g, st)

	err := r.MarkRead(t.Context(), "oc_a", 1)

	require.ErrorIs(t, err, boom)
	require.NotErrorIs(t, err, errNotInInbox)
}

func TestDecodeFeed_KeepsAThreadReplysNegativePosition(t *testing.T) {
	// position is int32 on the wire; a negative one is ten bytes of varint and
	// has to come back negative, not as a large positive number.
	got, err := decodeFeed(feedPayload([]LastMessage{{ChatID: "7001", Position: -3, CreateMs: 10}}))
	require.NoError(t, err)
	require.Equal(t, []LastMessage{{ChatID: "7001", Position: -3, CreateMs: 10}}, got.messages)
}

func TestResolver_FindsAChatInCollapsedChats(t *testing.T) {
	// A chat folded into Collapsed Chats is one box card at the inbox's top
	// level; only listing the box brings its chats back.
	g := &gateway{
		inbox: []LastMessage{{ChatID: "7001", Position: 1, CreateMs: 10}},
		boxed: []LastMessage{{ChatID: "7002", Position: 135, CreateMs: 20}},
	}
	r, _ := resolverTo(t, g, newFakeStore(map[[2]int64]string{{10, 1}: "oc_a", {20, 135}: "oc_boxed"}))

	require.NoError(t, r.MarkRead(t.Context(), "oc_boxed", 135))

	require.Equal(t, []string{"7002"}, g.readAt)
}

// fieldVarintOr is a varint field's value, 0 when the message does not carry it.
func fieldVarintOr(t *testing.T, b []byte, want protowire.Number) uint64 {
	t.Helper()
	var v uint64
	require.NoError(t, eachField(b, func(num protowire.Number, typ protowire.Type, raw []byte) error {
		if num == want && typ == protowire.VarintType {
			v, _ = protowire.ConsumeVarint(raw)
		}
		return nil
	}))
	return v
}
