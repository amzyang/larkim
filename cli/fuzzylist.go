package cli

import (
	"strings"

	"github.com/amzyang/larkim/fuzzy"
	"github.com/amzyang/larkim/store"
)

// fuzzyFilter narrows rows to those answering search, keeping the order the
// store returned them in: the listing is ordered by recency, and a search says
// which rows come in, not which come first. An empty search takes everything.
// limit <= 0 takes every hit. The index is built here so that a name is spelled
// once however many rows are walked.
func fuzzyFilter[T any](rows []T, search string, limit int, keep func(*fuzzy.Index, T) bool) []T {
	if search == "" {
		return rows
	}
	ix := fuzzy.NewIndex()
	out := make([]T, 0, len(rows))
	for _, r := range rows {
		if !keep(ix, r) {
			continue
		}
		if out = append(out, r); limit > 0 && len(out) == limit {
			break
		}
	}
	return out
}

func fuzzyChats(chats []store.Chat, search string, limit int) []store.Chat {
	return fuzzyFilter(chats, search, limit, func(ix *fuzzy.Index, c store.Chat) bool {
		_, ok := ix.Match(c.ChatID, c.Name, search)
		return ok
	})
}

// fuzzyContacts is fuzzyChats for contacts, which are reached by the local
// part of an address as well as by name.
func fuzzyContacts(contacts []store.Contact, search string, limit int) []store.Contact {
	address := strings.ToLower(search)
	return fuzzyFilter(contacts, search, limit, func(ix *fuzzy.Index, c store.Contact) bool {
		if _, ok := ix.Match(c.OpenID, c.Name, search); ok {
			return true
		}
		return strings.Contains(strings.ToLower(c.Email), address)
	})
}
