package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

// aroundFixture stores seven messages of one chat, three of them sharing a
// millisecond so the anchor can only be placed by the full sort key.
func aroundFixture(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	msgs := []store.Message{
		{MessageID: "om_1", ChatID: "oc_a", CreateMs: 10, MessagePosition: 1, RawJSON: "{}"},
		{MessageID: "om_2", ChatID: "oc_a", CreateMs: 20, MessagePosition: 2, RawJSON: "{}"},
		{MessageID: "om_3", ChatID: "oc_a", CreateMs: 30, MessagePosition: 3, RawJSON: "{}"},
		{MessageID: "om_4", ChatID: "oc_a", CreateMs: 30, MessagePosition: 4, RawJSON: "{}"},
		{MessageID: "om_5", ChatID: "oc_a", CreateMs: 30, MessagePosition: 5, RawJSON: "{}"},
		{MessageID: "om_6", ChatID: "oc_a", CreateMs: 40, MessagePosition: 6, RawJSON: "{}"},
		{MessageID: "om_7", ChatID: "oc_b", CreateMs: 35, MessagePosition: 1, RawJSON: "{}"},
	}
	_, err = s.UpsertMessages(context.Background(), msgs, 1)
	require.NoError(t, err)
	return s
}

func listedIDs(msgs []store.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.MessageID
	}
	return out
}

func TestListAround_IncludesTheAnchorBetweenBothSides(t *testing.T) {
	s := aroundFixture(t)
	got, err := listAround(context.Background(), s, store.MessageQuery{}, "om_4", 2)
	require.NoError(t, err)
	require.Equal(t, []string{"om_2", "om_3", "om_4", "om_5", "om_6"}, listedIDs(got),
		"2N+1 messages centred on the anchor, split correctly inside the shared millisecond")
}

func TestListAround_ScopesToTheAnchorsChat(t *testing.T) {
	s := aroundFixture(t)
	got, err := listAround(context.Background(), s, store.MessageQuery{}, "om_6", 3)
	require.NoError(t, err)
	require.NotContains(t, listedIDs(got), "om_7", "another chat's message of the same moment stays out")
}

func TestListAround_FollowsTheRequestedOrder(t *testing.T) {
	s := aroundFixture(t)
	got, err := listAround(context.Background(), s, store.MessageQuery{Desc: true}, "om_4", 1)
	require.NoError(t, err)
	require.Equal(t, []string{"om_5", "om_4", "om_3"}, listedIDs(got))
}

// run executes the CLI against a temporary data dir and returns stdout.
func run(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	root := New("test", "")
	root.SetOut(&out)
	root.SetErr(&errOut)
	cfg := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfg, []byte("data_dir: "+dir+"\n"), 0o600))
	root.SetArgs(append([]string{"--config", cfg, "--json"}, args...))
	err := root.Execute()
	return out.String(), err
}

func TestMessagesList_CursorFlagsRejectMixedPagination(t *testing.T) {
	dir := t.TempDir()
	_, err := run(t, dir, "messages", "list", "--before", "om_1", "--offset", "5")
	require.ErrorContains(t, err, "pick one")

	_, err = run(t, dir, "messages", "list", "--context", "5")
	require.ErrorContains(t, err, "--context only applies to --around")

	_, err = run(t, dir, "messages", "list", "--around", "om_1", "--before", "om_2")
	require.ErrorContains(t, err, "none of the others can be")
}

func TestMessagesList_AroundIsSizedByContextNotLimit(t *testing.T) {
	dir := t.TempDir()
	_, err := run(t, dir, "messages", "list", "--around", "om_1", "--limit", "5")
	require.ErrorContains(t, err, "none of the others can be", "--limit would be silently ignored")
}
