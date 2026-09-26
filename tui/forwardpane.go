package tui

import (
	"context"
	"encoding/json"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/sync"
)

// expandTimeout bounds the expansion a reader is waiting on. One bundle is
// one call, and a reader looking at an empty frame would rather be told than
// held.
const expandTimeout = 20 * time.Second

// forwardLoadedMsg carries one level of a bundle. gist rides along because an
// empty level has more than one meaning — not expanded yet, or refused for
// good — and only the queue row tells them apart.
type forwardLoadedMsg struct {
	bundleID string
	level    string
	msgs     []store.Message
	meta     msgMeta
	gist     store.ForwardGist
}

// forwardExpandedMsg answers an expansion a reader waited for.
type forwardExpandedMsg struct {
	level string
	err   error
}

// loadForward reads one level of a bundle: the children whose direct parent
// is level, which is the bundle itself at the top. A nested bundle among them
// stays one row — it opens a frame of its own rather than being drawn inside
// this one.
func loadForward(d Deps, bundleID, level string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		kids, err := d.Store.ForwardChildren(ctx, bundleID, level)
		if err != nil {
			return errMsg{err}
		}
		rows := make([]store.Message, 0, len(kids))
		for _, k := range kids {
			rows = append(rows, forwardedRow(k))
		}
		meta, err := loadMeta(ctx, d.Store, rows)
		if err != nil {
			return errMsg{err}
		}
		gists, err := d.Store.ForwardGists(ctx, []string{bundleID})
		if err != nil {
			return errMsg{err}
		}
		// Every picture in the tree is registered against the outermost
		// bundle, because the resource endpoint refuses a child's id.
		res, err := d.Store.ResourcesFor(ctx, bundleID)
		if err != nil {
			return errMsg{err}
		}
		meta.res = dispatchResources(kids, res)
		// A nested bundle has no queue row, so its line is counted from the
		// children that came down with the tree.
		var nested []string
		for _, k := range kids {
			if k.MsgType == "merge_forward" {
				nested = append(nested, k.MessageID)
			}
		}
		if meta.forwards, err = d.Store.ForwardLevels(ctx, bundleID, nested); err != nil {
			return errMsg{err}
		}
		return forwardLoadedMsg{bundleID: bundleID, level: level, msgs: rows, meta: meta, gist: gists[bundleID]}
	}
}

// dispatchResources hands each child the bundle's resources it actually
// names. Handing every child the whole list instead would make o offer every
// picture in the bundle on every row.
func dispatchResources(kids []store.Forwarded, res []store.Resource) map[string][]store.Resource {
	byKey := make(map[string]store.Resource, len(res))
	for _, r := range res {
		byKey[r.FileKey] = r
	}
	out := map[string][]store.Resource{}
	for _, k := range kids {
		for _, ref := range sync.ExtractResources(k.MessageID, k.MsgType, k.ContentRaw) {
			if r, ok := byKey[ref.FileKey]; ok {
				out[k.MessageID] = append(out[k.MessageID], r)
			}
		}
	}
	return out
}

// forwardedRow maps a child onto the row the list draws. Position and thread
// are dropped: in this shape they would speak of the source chat, and a child
// opens nothing but the nested bundle it may already be.
func forwardedRow(f store.Forwarded) store.Message {
	x := store.Message{
		MessageID: f.MessageID, ChatID: f.ChatID, MsgType: f.MsgType,
		SenderID: f.SenderID, SenderName: f.SenderName, SenderType: "user",
		ContentRaw: f.ContentRaw, CreateMs: f.CreateMs, UpdateMs: f.CreateMs,
		MentionsJSON: f.MentionsJSON, RawJSON: f.RawJSON,
	}
	if content, ok := forwardedContent(f); ok {
		x.Content, x.RenderedAt = content, f.CreateMs
	}
	return x
}

// forwardedContent is a child's rendering, built here because lark-cli never
// renders one: the endpoint that expands a bundle answers with raw bodies and
// nothing else. It covers what can be rebuilt faithfully — the words of a
// text message, the reference that places a picture — and marks the types
// that name themselves from their body before a rendering is ever consulted.
//
// A post is left out on purpose. Its rendering is markdown, which only
// lark-cli builds; handing the markdown path a flattened line would turn the
// sender's own punctuation into formatting. It keeps the dim stand-in, which
// is the truth: the words are here and the formatting is not.
func forwardedContent(f store.Forwarded) (string, bool) {
	switch f.MsgType {
	case "text":
		var v struct {
			Text string `json:"text"`
		}
		if json.Unmarshal([]byte(f.ContentRaw), &v) == nil && v.Text != "" {
			return sync.UnwrapParagraphs(v.Text), true
		}
	case "image":
		// The markdown form splitImages reads, which is how lark-cli's own
		// rendering places a picture.
		var v struct {
			ImageKey string `json:"image_key"`
		}
		if json.Unmarshal([]byte(f.ContentRaw), &v) == nil && v.ImageKey != "" {
			return "![Image](" + v.ImageKey + ")", true
		}
	case "file", "audio", "media", "video", "interactive", "video_chat", "sticker", "merge_forward":
		return "", true
	}
	return "", false
}

// onForwardLoaded puts a level of a bundle in the right pane. An empty level
// is the interesting case: the queue row says whether the children are still
// coming, and the reader who opened it is why the interactive lane exists.
func (m Model) onForwardLoaded(msg forwardLoadedMsg) (tea.Model, tea.Cmd) {
	if m.rightKind != rightForward || msg.level != m.threadID {
		return m, nil
	}
	wasOn, anchor, tailed := m.rightLanded(msg.msgs)
	m.threadBase, m.threadMeta = msg.msgs, msg.meta
	m.thread = msg.msgs
	m.rightNote = ""
	switch {
	case len(msg.msgs) > 0:
	case msg.gist.Refused:
		m.rightNote = noteRefused
	case msg.gist.Expanded:
		m.rightNote = "这条转发是空的"
	case m.deps.Syncer == nil:
		// Only the process holding the sync lock may write, so a TUI reading
		// beside a daemon can do nothing but wait for it.
		m.rightNote = "还没展开，等同步"
	default:
		m.rightNote = noteExpanding
	}
	if i := indexOfID(m.thread, wasOn); i >= 0 {
		m.threadIdx = i
	}
	m.threadIdx = clamp(m.threadIdx, 0, max(0, len(m.thread)-1))
	m.rebuildThread()
	m.threadTop = holdTop(m.threadRows, m.thread, anchor, tailed, m.threadTop, m.listHeight())
	if m.rightNote == noteExpanding {
		return m, expandForward(m.deps, msg.bundleID, msg.level)
	}
	return m, nil
}

const (
	noteExpanding = "正在展开…"
	noteRefused   = "这条转发无法展开"
)

// onForwardExpanded reloads the frame the expansion was for.
func (m Model) onForwardExpanded(msg forwardExpandedMsg) (tea.Model, tea.Cmd) {
	if m.rightKind != rightForward || msg.level != m.threadID {
		return m, nil
	}
	if msg.err != nil {
		m.rightNote = noteRefused
		m.rebuildThread()
		return m.notify("expand forward: "+msg.err.Error(), true), nil
	}
	return m, m.loadRight()
}

// expandForward asks Feishu for a bundle the queue has not reached. It takes
// the interactive lane: somebody pressed a key for this one, and the
// background sweep it would otherwise queue behind runs every few seconds.
func expandForward(d Deps, bundleID, level string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := waited(expandTimeout)
		defer cancel()
		// The queue row belongs to the outermost bundle; one call brings back
		// every level of the tree, this one among them.
		_, err := d.Syncer.ExpandForward(ctx, bundleID)
		return forwardExpandedMsg{level: level, err: err}
	}
}
