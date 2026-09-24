package tui

import (
	"github.com/amzyang/larkim/store"
)

// attachRes is the download row of one of a message's attachment keys. The
// zero value is a key the store has never registered.
func attachRes(key string, x store.Message, st msgStyle) store.Resource {
	for _, r := range st.res[x.MessageID] {
		if r.FileKey == key {
			return r
		}
	}
	return store.Resource{}
}
