package larkweb

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Store is where web ids are matched and kept. The web client names chats by a
// numeric id and the OpenAPI by an oc_ one, and nothing either side hands out
// links the two; a message's send time and position are the pair both sides
// hold, so a chat is matched through one of its messages and the answer kept.
type Store interface {
	// ChatAt answers ok=false when no single chat holds the message, which is
	// the common case for a chat older than the local history.
	ChatAt(ctx context.Context, createMs, position int64) (chatID string, ok bool, err error)
	// WebChatID is empty for a chat never matched.
	WebChatID(ctx context.Context, chatID string) (string, error)
	SetWebChatIDs(ctx context.Context, ids map[string]string) error
}

// relistAfter is how soon a miss may list the inbox again. A sweep asks about
// many chats in a row, and every one the inbox cannot place would otherwise
// list all of it again; a chat that only just got its first message waits this
// long to be found.
const relistAfter = time.Minute

// Resolver finds the web client's id for an OpenAPI chat.
//
// A matched id is kept in the store, so a chat is matched once: afterwards it
// resolves even when the inbox drops it (moved to Done) or lists it by a
// message the store has not synced yet, and a sweep costs no listing at all.
type Resolver struct {
	Client *Client
	Store  Store
	// Now is the clock the relisting pace is read from; nil is time.Now.
	Now func() time.Time

	mu       sync.Mutex
	listedAt time.Time
	// listErr is what the last listing failed with. A sweep whose listing
	// fails — no session, gateway down — answers every chat behind it with
	// that failure until relistAfter, instead of listing again per chat.
	listErr error
}

// errNotInInbox is a chat never matched that the inbox does not list, or lists
// by a last message the store cannot place.
var errNotInInbox = errors.New("chat not matched to Feishu's web client yet; it needs its newest message synced while it is in the inbox")

// webID is the web client's id for chatID.
func (r *Resolver) webID(ctx context.Context, chatID string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id, err := r.Store.WebChatID(ctx, chatID); err != nil || id != "" {
		return id, err
	}
	now := r.now()
	if !r.listedAt.IsZero() && now.Sub(r.listedAt) < relistAfter {
		if r.listErr != nil {
			return "", r.listErr
		}
		return "", &Error{Op: "resolve chat", Err: errNotInInbox}
	}
	if err := r.match(ctx); err != nil {
		// A listing cut short by this caller's own deadline says nothing
		// about the next caller's, so it is not held against them.
		if ctx.Err() == nil {
			r.listedAt, r.listErr = now, err
		}
		return "", err
	}
	r.listedAt, r.listErr = now, nil
	id, err := r.Store.WebChatID(ctx, chatID)
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", &Error{Op: "resolve chat", Err: errNotInInbox}
	}
	return id, nil
}

// match lists the inbox and keeps every chat the store can place. One it
// cannot is left out rather than failing the rest: each other chat's answer is
// still good, and kept, it spares the next sweep a listing.
func (r *Resolver) match(ctx context.Context) error {
	last, err := r.Client.LastMessages(ctx)
	if err != nil {
		return err
	}
	ids := make(map[string]string, len(last))
	// claimed are local chats two different web chats landed on. The store
	// holding one of the pair does not make it the right one — the other
	// chat's message may just not be synced — so neither is kept.
	claimed := map[string]bool{}
	for _, m := range last {
		chatID, ok, err := r.Store.ChatAt(ctx, m.CreateMs, m.Position)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if prev, seen := ids[chatID]; seen && prev != m.ChatID {
			claimed[chatID] = true
		}
		ids[chatID] = m.ChatID
	}
	for chatID := range claimed {
		delete(ids, chatID)
	}
	return r.Store.SetWebChatIDs(ctx, ids)
}

func (r *Resolver) now() time.Time {
	if r.Now == nil {
		return time.Now()
	}
	return r.Now()
}

// MarkRead settles an OpenAPI chat's unread messages up to maxPosition.
func (r *Resolver) MarkRead(ctx context.Context, chatID string, maxPosition int64) error {
	id, err := r.webID(ctx, chatID)
	if err != nil {
		return err
	}
	return r.Client.MarkRead(ctx, id, maxPosition)
}
