package larkweb

import (
	"context"
	"fmt"
	"strconv"

	"google.golang.org/protobuf/encoding/protowire"
)

// cmdPullFeedCards lists the inbox, the web client's own chat list.
const cmdPullFeedCards int32 = 1000

// PullFeedCardsRequest's fields. The enums are fixed to one value each: the
// inbox, refreshed from the top, for IM rather than the documents feed.
const (
	fieldFeedType         protowire.Number = 1
	fieldFeedPullType     protowire.Number = 2
	fieldFeedCursor       protowire.Number = 3
	fieldFeedCount        protowire.Number = 4
	fieldFeedParentCardID protowire.Number = 5
	fieldFeedBusinessType protowire.Number = 9

	feedTypeInbox    = 1
	pullTypeRefresh  = 1
	pullTypeLoadMore = 2
	businessTypeLark = 1
)

// PullFeedCardsResponse's fields: the chats' last messages, the cards they
// sit on, and where the next page starts.
const (
	fieldFeedMessages   protowire.Number = 2
	fieldFeedCards      protowire.Number = 3
	fieldFeedNextCursor protowire.Number = 4
)

// entities.FeedCard fields. A box is Collapsed Chats: one card standing for
// the chats folded into it.
const (
	fieldCardID   protowire.Number = 1
	fieldCardType protowire.Number = 2

	cardTypeBox = 5
)

// entities.Message fields a chat is recognised by.
const (
	fieldMessageChatID       protowire.Number = 10
	fieldMessagePosition     protowire.Number = 13
	fieldMessageCreateTimeMs protowire.Number = 62
)

// feedPageSize is how many chats one request asks for, which is what the web
// client asks for too.
const feedPageSize = 200

// maxFeedPages bounds a listing. An inbox of this many pages is not one a
// reader keeps, and a cursor that never ends must not loop.
const maxFeedPages = 10

// LastMessage is a chat's newest message as the web client knows it: the only
// thing it shares with the OpenAPI's view of the same chat, because the two
// name chats in different id spaces.
type LastMessage struct {
	// ChatID is the web client's numeric id, the one PutReadMessages takes.
	ChatID   string
	Position int64
	CreateMs int64
}

type feedRequest struct {
	cursor int64
	// parent lists one box's chats rather than the inbox's top level.
	parent int64
}

func (feedRequest) Cmd() int32 { return cmdPullFeedCards }

func (r feedRequest) Encode() []byte {
	pull := uint64(pullTypeRefresh)
	if r.cursor != 0 {
		pull = pullTypeLoadMore
	}
	var b []byte
	b = protowire.AppendTag(b, fieldFeedType, protowire.VarintType)
	b = protowire.AppendVarint(b, feedTypeInbox)
	b = protowire.AppendTag(b, fieldFeedPullType, protowire.VarintType)
	b = protowire.AppendVarint(b, pull)
	if r.cursor != 0 {
		b = protowire.AppendTag(b, fieldFeedCursor, protowire.VarintType)
		b = protowire.AppendVarint(b, uint64(r.cursor))
	}
	b = protowire.AppendTag(b, fieldFeedCount, protowire.VarintType)
	b = protowire.AppendVarint(b, feedPageSize)
	if r.parent != 0 {
		b = protowire.AppendTag(b, fieldFeedParentCardID, protowire.VarintType)
		b = protowire.AppendVarint(b, uint64(r.parent))
	}
	b = protowire.AppendTag(b, fieldFeedBusinessType, protowire.VarintType)
	return protowire.AppendVarint(b, businessTypeLark)
}

// LastMessages lists every inbox chat's newest message, the ones folded into
// Collapsed Chats included: the inbox's top level carries the box as one card,
// and its chats come back only when the box itself is listed. A chat moved to
// Done is not in the inbox and so is not here.
func (c *Client) LastMessages(ctx context.Context) ([]LastMessage, error) {
	out, boxes, err := c.listFeed(ctx, 0)
	if err != nil {
		return nil, err
	}
	for _, box := range boxes {
		inBox, _, err := c.listFeed(ctx, box)
		if err != nil {
			return nil, err
		}
		out = append(out, inBox...)
	}
	return out, nil
}

// listFeed pages through one level of the inbox, answering its chats and the
// boxes it holds.
func (c *Client) listFeed(ctx context.Context, parent int64) ([]LastMessage, []int64, error) {
	var out []LastMessage
	var boxes []int64
	var cursor int64
	for range maxFeedPages {
		payload, err := c.do(ctx, "list chats", feedRequest{cursor: cursor, parent: parent})
		if err != nil {
			return nil, nil, err
		}
		page, err := decodeFeed(payload)
		if err != nil {
			return nil, nil, &Error{Op: "list chats", Err: err}
		}
		out = append(out, page.messages...)
		boxes = append(boxes, page.boxes...)
		if page.next == 0 || page.next == cursor {
			break
		}
		cursor = page.next
	}
	return out, boxes, nil
}

// feedPage is one decoded page.
type feedPage struct {
	messages []LastMessage
	// boxes are the page's box cards, each a level of its own to list.
	boxes []int64
	next  int64
}

// decodeFeed reads one page: each messages map entry's value, the box cards,
// and the cursor the next page starts from.
func decodeFeed(b []byte) (feedPage, error) {
	var p feedPage
	err := eachField(b, func(num protowire.Number, typ protowire.Type, v []byte) error {
		switch {
		case num == fieldFeedNextCursor && typ == protowire.VarintType:
			x, _ := protowire.ConsumeVarint(v)
			p.next = int64(x)
		case num == fieldFeedCards && typ == protowire.BytesType:
			card, _ := protowire.ConsumeBytes(v)
			box, ok, err := decodeBox(card)
			if err != nil {
				return err
			}
			if ok {
				p.boxes = append(p.boxes, box)
			}
		case num == fieldFeedMessages && typ == protowire.BytesType:
			entry, _ := protowire.ConsumeBytes(v)
			msg, err := mapValue(entry)
			if err != nil {
				return err
			}
			m, err := decodeLastMessage(msg)
			if err != nil {
				return err
			}
			if m.ChatID != "" {
				p.messages = append(p.messages, m)
			}
		}
		return nil
	})
	return p, err
}

// decodeBox answers a card's id when the card is a box. The id is a string on
// the wire and an int64 where a request names it as a parent.
func decodeBox(card []byte) (int64, bool, error) {
	var id string
	var cardType uint64
	err := eachField(card, func(num protowire.Number, typ protowire.Type, v []byte) error {
		switch {
		case num == fieldCardID && typ == protowire.BytesType:
			x, _ := protowire.ConsumeBytes(v)
			id = string(x)
		case num == fieldCardType && typ == protowire.VarintType:
			cardType, _ = protowire.ConsumeVarint(v)
		}
		return nil
	})
	if err != nil || cardType != cardTypeBox {
		return 0, false, err
	}
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("box card id %q: %w", id, err)
	}
	return n, true, nil
}

// mapValue is a map entry's value, field 2 by protobuf's definition of a map.
func mapValue(entry []byte) ([]byte, error) {
	var value []byte
	err := eachField(entry, func(num protowire.Number, typ protowire.Type, v []byte) error {
		if num == 2 && typ == protowire.BytesType {
			value, _ = protowire.ConsumeBytes(v)
		}
		return nil
	})
	return value, err
}

func decodeLastMessage(b []byte) (LastMessage, error) {
	var m LastMessage
	err := eachField(b, func(num protowire.Number, typ protowire.Type, v []byte) error {
		switch {
		case num == fieldMessageChatID && typ == protowire.BytesType:
			x, _ := protowire.ConsumeBytes(v)
			m.ChatID = string(x)
		case num == fieldMessagePosition && typ == protowire.VarintType:
			x, _ := protowire.ConsumeVarint(v)
			// int32 on the wire, and negative for a thread reply, so the
			// sign has to survive the widening.
			m.Position = int64(int32(x))
		case num == fieldMessageCreateTimeMs && typ == protowire.VarintType:
			x, _ := protowire.ConsumeVarint(v)
			m.CreateMs = int64(x)
		}
		return nil
	})
	if err != nil {
		return LastMessage{}, fmt.Errorf("message: %w", err)
	}
	return m, nil
}
