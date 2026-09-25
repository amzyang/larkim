package tui

import (
	"slices"

	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
)

// outboxState is what the store cannot answer: how far a message the user
// submitted has got on its way to Feishu.
type outboxState int

const (
	outSending outboxState = iota
	outSent                // Feishu took it; the row has not reached the store yet
	outFailed
)

// outboxItem is a submitted message the store does not hold yet. localID is
// both the row's identity and the send's idempotency key, so retrying is the
// same send rather than a second one.
type outboxItem struct {
	localID  string
	chatID   string
	threadID string
	replyTo  string
	inThread bool
	msgType  string // "text", "post" or "image"
	// send is the body on the wire; body is what the bubble draws, which is
	// the shape lark-cli renders this message back into after ingest.
	send larkcli.Outgoing
	body string
	// images are the files this send has to upload first, and keys the ones
	// it already did, which is what keeps a retry from uploading twice.
	images    []draftImage
	keys      []string
	state     outboxState
	messageID string // set once Feishu answered
	createMs  int64
}

// message is the row a pending send draws as. Content is filled and
// RenderedAt is non-zero so the body takes the ordinary text path instead of
// the stand-in a message still awaiting rendering gets.
func (it outboxItem) message(selfID, selfName string) store.Message {
	var position int64
	if it.inThread {
		position = -1
	}
	return store.Message{
		MessageID: it.localID, ChatID: it.chatID, ThreadID: it.threadID, ReplyTo: it.replyTo,
		MsgType: it.msgType, SenderID: selfID, SenderType: "user", SenderName: selfName,
		Content: it.body, CreateMs: it.createMs, MessagePosition: position, RenderedAt: 1,
	}
}

func (m *Model) enqueue(it outboxItem) { m.outbox = append(m.outbox, it) }

// outboxAt points at the item a local id names, so a caller can move its
// state on. Nil covers the sends that carry no item at all, which is what
// :send does when it fires at a chat the panes are not showing.
func (m *Model) outboxAt(localID string) *outboxItem {
	i := slices.IndexFunc(m.outbox, func(it outboxItem) bool { return it.localID == localID })
	if i < 0 {
		return nil
	}
	return &m.outbox[i]
}

func (m *Model) dropOutbox(localID string) {
	m.outbox = slices.DeleteFunc(m.outbox, func(it outboxItem) bool { return it.localID == localID })
}

// outboxStates keys the pending sends by the id their rows carry, which is
// how the renderer tells one apart from a message the store returned.
func (m Model) outboxStates() map[string]outboxState {
	if len(m.outbox) == 0 {
		return nil
	}
	out := make(map[string]outboxState, len(m.outbox))
	for _, it := range m.outbox {
		out[it.localID] = it.state
	}
	return out
}

// applyOutbox rebuilds both message lists from the rows the store returned
// plus the sends still in flight. It is the only place msgs and thread are
// put together.
func (m *Model) applyOutbox() {
	m.msgs = append(slices.Clone(m.msgsBase), m.pendingRows(m.msgsBase, func(it outboxItem) bool {
		return m.chatID != "" && it.chatID == m.chatID
	})...)
	m.thread = append(slices.Clone(m.threadBase), m.pendingRows(m.threadBase, func(it outboxItem) bool {
		return m.threadID != "" && it.threadID == m.threadID
	})...)
	quotePending(&m.meta, m.msgsBase, m.outbox)
	quotePending(&m.threadMeta, m.threadBase, m.outbox)
	resPending(&m.meta, m.outbox)
	resPending(&m.threadMeta, m.outbox)
}

// resPending lets a pending image draw the file the user picked. The download
// row a picture needs is normally written by the syncer once Feishu answers,
// which is far too late for a bubble that is already on screen; the original
// is right here on disk, so it stands in until the real row lands.
func resPending(meta *msgMeta, items []outboxItem) {
	for _, it := range items {
		var rows []store.Resource
		for _, img := range it.images {
			if img.local == "" {
				continue
			}
			rows = append(rows, store.Resource{
				FileKey: img.key, Type: "image",
				Status: "done", LocalPath: img.local,
			})
		}
		if rows == nil {
			continue
		}
		if meta.res == nil {
			meta.res = map[string][]store.Resource{}
		}
		// Assigned rather than appended: a pane redraws far more often than
		// an outbox item changes, and an item's images never do.
		meta.res[it.localID] = rows
	}
}

// pendingRows draws the items a pane wants. One whose message has already
// landed is left out: that is what ends a bubble's life, whether the ingest
// after the send brought the row in or a later sync did.
func (m Model) pendingRows(landed []store.Message, wanted func(outboxItem) bool) []store.Message {
	var out []store.Message
	for _, it := range m.outbox {
		if !wanted(it) || (it.messageID != "" && indexOfID(landed, it.messageID) >= 0) {
			continue
		}
		out = append(out, it.message(m.deps.Self, m.selfName))
	}
	return out
}

// quotePending lets a pending reply draw its quote. loadMeta only looked at
// the rows the store returned, so the message this reply answers is missing
// from the page's detail even when it sits right above it.
func quotePending(meta *msgMeta, landed []store.Message, items []outboxItem) {
	for _, it := range items {
		if it.replyTo == "" {
			continue
		}
		if _, ok := meta.parents[it.replyTo]; ok {
			continue
		}
		i := indexOfID(landed, it.replyTo)
		if i < 0 {
			continue
		}
		if meta.parents == nil {
			meta.parents = map[string]store.Message{}
		}
		meta.parents[it.replyTo] = landed[i]
	}
}
