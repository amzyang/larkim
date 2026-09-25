package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/amzyang/larkim/card"
	"github.com/amzyang/larkim/store"
)

// yankKind is the single stored field the y family puts on the clipboard, as
// opposed to Y, which assembles an agent context out of many.
type yankKind int

const (
	yankID yankKind = iota
	yankRaw
	yankContent
)

// yankSource is one row the y family acts on, flattened out of the chat or
// message it came from so the rendering below stays a pure function.
type yankSource struct {
	id         string
	raw        string
	content    string
	contentRaw string
	rendered   bool
	deleted    bool
	sender     string
}

// chatYank flattens the highlighted chat. A chat carries no body of its own,
// so yc falls through to the message its preview line shows.
func chatYank(c store.Chat) yankSource {
	return yankSource{
		id:         c.ChatID,
		raw:        c.RawJSON,
		content:    c.LastContent,
		contentRaw: c.LastContentRaw,
		rendered:   c.LastRenderedAt > 0,
		deleted:    c.LastDeleted,
	}
}

func messageYank(m store.Message) yankSource {
	sender := m.SenderName
	if sender == "" {
		sender = m.SenderID
	}
	return yankSource{
		id:         m.MessageID,
		raw:        m.RawJSON,
		content:    m.Content,
		contentRaw: m.ContentRaw,
		rendered:   m.RenderedAt > 0,
		deleted:    m.Deleted,
		sender:     sender,
	}
}

// yank renders what the y family writes to the clipboard and the line the
// status bar reports it by. Empty text means there was nothing to copy.
func yank(k yankKind, src []yankSource) (text, notice string) {
	switch k {
	case yankID:
		return renderIDs(src)
	case yankRaw:
		return renderRaw(src)
	}
	return renderContent(src)
}

func renderIDs(src []yankSource) (string, string) {
	ids := make([]string, len(src))
	for i, s := range src {
		ids[i] = s.id
	}
	text := strings.Join(ids, "\n")
	if len(ids) == 1 {
		return text, "copied " + text
	}
	return text, fmt.Sprintf("copied %d ids", len(ids))
}

func renderRaw(src []yankSource) (string, string) {
	if len(src) == 1 {
		text := indentJSON(src[0].raw, "")
		return text, "copied raw json · " + humanBytes(int64(len(text)))
	}
	parts := make([]string, len(src))
	for i, s := range src {
		parts[i] = indentJSON(s.raw, "  ")
	}
	text := "[\n  " + strings.Join(parts, ",\n  ") + "\n]"
	return text, fmt.Sprintf("copied %d json · %s", len(src), humanBytes(int64(len(text))))
}

// indentJSON makes a stored payload readable without a trip through jq. One
// that does not parse goes out as it came, so a malformed element degrades on
// its own rather than taking a whole array down with it.
func indentJSON(raw, prefix string) string {
	var buf bytes.Buffer
	if json.Indent(&buf, []byte(raw), prefix, "  ") != nil {
		return raw
	}
	return buf.String()
}

// renderContent copies bodies the way a person reads them. A range carries
// the sender of each message, because a bare transcript of several people
// cannot be read; a recalled message has no body left and drops out.
func renderContent(src []yankSource) (string, string) {
	var parts []string
	unrendered := false
	for _, s := range src {
		body := s.content
		switch c, ok := card.Parse(s.contentRaw); {
		case ok:
			// A card is its own document: the body is built from the card
			// rather than from the text it renders to.
			body = c.Markdown()
		case !s.rendered:
			body, unrendered = s.contentRaw, true
		}
		if s.deleted || body == "" {
			continue
		}
		if len(src) > 1 && s.sender != "" {
			body = s.sender + ": " + body
		}
		parts = append(parts, body)
	}
	if len(parts) == 0 {
		return "", ""
	}
	text := strings.Join(parts, "\n\n")
	if len(src) == 1 {
		if unrendered {
			return text, "copied raw body (unrendered) · " + humanBytes(int64(len(text)))
		}
		return text, "copied content · " + humanBytes(int64(len(text)))
	}
	return text, fmt.Sprintf("copied %s · %s", plural(len(parts), "msg", "msgs"), humanBytes(int64(len(text))))
}
