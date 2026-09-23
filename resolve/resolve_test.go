package resolve

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

func newResolver(t *testing.T) (*Resolver, *larkcli.Fake) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	f := larkcli.NewFake()
	return &Resolver{Store: st, Client: f, Now: func() int64 { return 1 }}, f
}

func TestChat_LocalNameIgnoresSpacesThenRemote(t *testing.T) {
	r, f := newResolver(t)
	ctx := context.Background()
	require.NoError(t, r.Store.UpsertChats(ctx, []store.Chat{{ChatID: "oc_1", Name: "项目协作群", ChatMode: "group"}}, 1))

	c, err := r.Chat(ctx, "项目 协作群")
	require.NoError(t, err)
	require.Equal(t, "oc_1", c.ChatID)
	require.Empty(t, f.Calls, "local hit needs no API")

	c, err = r.Chat(ctx, "oc_unknown")
	require.NoError(t, err)
	require.Equal(t, "oc_unknown", c.ChatID)

	f.Chats = []larkcli.RawChat{{ChatID: "oc_2", Name: "Remote"}}
	c, err = r.Chat(ctx, "Remote")
	require.NoError(t, err)
	require.Equal(t, "oc_2", c.ChatID)

	_, err = r.Chat(ctx, "Nope")
	require.ErrorContains(t, err, "no chat named")
}

func TestUser_EmailViaRemoteIsCached(t *testing.T) {
	r, f := newResolver(t)
	ctx := context.Background()
	f.Users = []larkcli.User{{OpenID: "ou_z", Name: "林岚", Email: "linlan@example.com", P2PChatID: "oc_p"}}

	c, err := r.User(ctx, "linlan@example.com")
	require.NoError(t, err)
	require.Equal(t, "ou_z", c.OpenID)
	require.Equal(t, "oc_p", c.P2PChatID)

	f.Calls = nil
	c, err = r.User(ctx, "LINLAN@example.com")
	require.NoError(t, err)
	require.Equal(t, "ou_z", c.OpenID)
	require.Empty(t, f.Calls, "second lookup served from contacts")

	require.NoError(t, r.Store.UpsertContacts(ctx, []store.Contact{{OpenID: "ou_a", Name: "Dup"}, {OpenID: "ou_b", Name: "Dup"}}, 1))
	_, err = r.User(ctx, "Dup")
	var amb *AmbiguousError
	require.ErrorAs(t, err, &amb)
	require.Len(t, amb.Candidates, 2)
}
