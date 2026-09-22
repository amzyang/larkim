// Package resolve turns human references (email, chat name) into Feishu ids,
// preferring the local store and falling back to lark-cli lookups.
package resolve

import (
	"context"
	"fmt"
	"strings"

	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
)

// Resolver combines the store and the client.
type Resolver struct {
	Store  *store.Store
	Client larkcli.Client
	Now    func() int64 // Unix ms, for contact upserts
}

// AmbiguousError lists the candidates a reference matched.
type AmbiguousError struct {
	Ref        string
	Candidates []string
}

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("%q matches %d entries: %s", e.Ref, len(e.Candidates), strings.Join(e.Candidates, ", "))
}

// Chat resolves an oc_ id, or a chat name (whitespace and case insensitive).
func (r *Resolver) Chat(ctx context.Context, ref string) (store.Chat, error) {
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, "oc_") {
		c, err := r.Store.GetChat(ctx, ref)
		if err == store.ErrNotFound {
			return store.Chat{ChatID: ref}, nil
		}
		return c, err
	}
	local, err := r.Store.FindChatsByName(ctx, ref)
	if err != nil {
		return store.Chat{}, err
	}
	if len(local) == 1 {
		return local[0], nil
	}
	if len(local) > 1 {
		return store.Chat{}, ambiguousChats(ref, local)
	}
	remote, err := r.Client.SearchChats(ctx, ref)
	if err != nil {
		return store.Chat{}, err
	}
	want := fold(ref)
	var hits []store.Chat
	for _, c := range remote {
		if fold(c.Name) == want {
			hits = append(hits, store.Chat{ChatID: c.ChatID, Name: c.Name, ChatMode: c.ChatMode, RawJSON: string(c.Raw)})
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return store.Chat{}, fmt.Errorf("no chat named %q", ref)
	default:
		return store.Chat{}, ambiguousChats(ref, hits)
	}
}

// User resolves an ou_ id, an email or an exact display name to a contact.
func (r *Resolver) User(ctx context.Context, ref string) (store.Contact, error) {
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, "ou_") {
		c, err := r.Store.GetContact(ctx, ref)
		if err == store.ErrNotFound {
			return store.Contact{OpenID: ref}, nil
		}
		return c, err
	}
	local, err := r.Store.FindContacts(ctx, ref)
	if err != nil {
		return store.Contact{}, err
	}
	if len(local) == 1 {
		return local[0], nil
	}
	if len(local) > 1 {
		return store.Contact{}, ambiguousContacts(ref, local)
	}
	users, err := r.Client.SearchUsers(ctx, ref, nil)
	if err != nil {
		return store.Contact{}, err
	}
	var hits []store.Contact
	for _, u := range users {
		if strings.EqualFold(u.Email, ref) || u.Name == ref {
			hits = append(hits, store.Contact{OpenID: u.OpenID, Name: u.Name, Email: u.Email, P2PChatID: u.P2PChatID})
		}
	}
	switch len(hits) {
	case 1:
		if r.Now != nil {
			_ = r.Store.UpsertContacts(ctx, hits, r.Now())
		}
		return hits[0], nil
	case 0:
		return store.Contact{}, fmt.Errorf("no user matching %q", ref)
	default:
		return store.Contact{}, ambiguousContacts(ref, hits)
	}
}

func ambiguousChats(ref string, chats []store.Chat) error {
	e := &AmbiguousError{Ref: ref}
	for _, c := range chats {
		e.Candidates = append(e.Candidates, c.ChatID+" ("+c.Name+")")
	}
	return e
}

func ambiguousContacts(ref string, cs []store.Contact) error {
	e := &AmbiguousError{Ref: ref}
	for _, c := range cs {
		e.Candidates = append(e.Candidates, c.OpenID+" ("+c.Name+" "+c.Email+")")
	}
	return e
}

func fold(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), "")) }
