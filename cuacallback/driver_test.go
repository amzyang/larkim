package cuacallback

import (
	"context"
	"encoding/json/v2"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeTransport struct {
	tools     []string
	args      []map[string]any
	responses []string
}

func (f *fakeTransport) Call(_ context.Context, tool string, args map[string]any, out any) error {
	f.tools = append(f.tools, tool)
	f.args = append(f.args, args)
	if len(f.responses) == 0 {
		return errors.New("unexpected call")
	}
	r := f.responses[0]
	f.responses = f.responses[1:]
	return json.Unmarshal([]byte(r), out)
}
func TestCUA_UsesPositionLinkAndSnapshotTokenWithoutRetry(t *testing.T) {
	t.Parallel()
	f := &fakeTransport{responses: []string{
		`{"windows":[{"pid":17,"window_id":42,"title":"Lingxi"}]}`,
		`{"elements":[{"role":"AXWebArea","label":"messenger-chat"}]}`,
		`{"elements":[{"element_index":1,"role":"AXButton","label":"claim"}]}`,
		`{"effect":"unverifiable","route":"accessibility"}`,
	}}
	var opened []string
	d := CUA{Transport: f, Session: "test-job", OpenURL: func(_ context.Context, u string) error { opened = append(opened, u); return nil }}
	u := "lark://applink.feishu.cn/client/chat/open?openChatId=oc_quiet&position=200&messageId=om_quota"
	w, err := d.Open(t.Context(), u)
	require.NoError(t, err)
	require.Equal(t, Window{PID: 17, ID: 42}, w)
	require.Equal(t, []string{u}, opened)
	_, err = d.State(t.Context(), w)
	require.NoError(t, err)
	require.NoError(t, d.Click(t.Context(), w, "s00000001:5"))
	require.Equal(t, []string{"list_windows", "get_window_state", "get_window_state", "click"}, f.tools)
	require.Equal(t, "s00000001:5", f.args[3]["element_token"])
	require.Equal(t, "foreground", f.args[3]["delivery_mode"])
	require.NotContains(t, f.args[3], "x")
}
func TestCUA_FrontsAnUnreadableWindowOnceAndWaitsForAX(t *testing.T) {
	t.Parallel()
	// Lingxi also lists an untitled helper window, which keeps bring_to_front
	// from verifying the exact window even when activation works.
	f := &fakeTransport{responses: []string{
		`{"windows":[{"pid":17,"window_id":41,"title":""},{"pid":17,"window_id":42,"title":"Lingxi"}]}`,
		`{"degraded":true,"degraded_reason":"ax_window_unresolved"}`,
		`{"status":"partial"}`,
		`{"degraded":true,"degraded_reason":"ax_window_unresolved"}`,
		`{"elements":[{"role":"AXWebArea","label":"messenger-chat"}]}`,
	}}
	opens := 0
	d := CUA{Transport: f, Session: "test-job", OpenURL: func(context.Context, string) error { opens++; return nil }}
	w, err := d.Open(t.Context(), "lark://test")
	require.NoError(t, err)
	require.Equal(t, Window{PID: 17, ID: 42}, w)
	require.Equal(t, 1, opens)
	require.Equal(t, []string{"list_windows", "get_window_state", "bring_to_front", "get_window_state", "get_window_state"}, f.tools)
	require.Equal(t, 42, f.args[2]["window_id"])
}

func TestCUA_ReadableWindowIsNotActivated(t *testing.T) {
	t.Parallel()
	f := &fakeTransport{responses: []string{
		`{"windows":[{"pid":17,"window_id":42,"title":"Lingxi"}]}`,
		`{"elements":[{"role":"AXWebArea","label":"messenger-chat"}]}`,
	}}
	d := CUA{Transport: f, Session: "test-job", OpenURL: func(context.Context, string) error { return nil }}
	_, err := d.Open(t.Context(), "lark://test")
	require.NoError(t, err)
	require.NotContains(t, f.tools, "bring_to_front")
}

func TestCUA_RefusesAmbiguousWindowAndHonorsDeadline(t *testing.T) {
	t.Parallel()
	for _, response := range []string{
		`{"windows":[{"pid":17,"window_id":42,"title":"Lingxi"},{"pid":17,"window_id":43,"title":"Lingxi"}]}`,
		`{"windows":[{"pid":17,"window_id":41,"title":""}]}`,
		`{"windows":[]}`,
	} {
		f := &fakeTransport{responses: []string{response}}
		d := CUA{Transport: f, Session: "test-job", OpenURL: func(context.Context, string) error { return nil }}
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		_, err := d.Open(ctx, "lark://test")
		cancel()
		require.Error(t, err)
		require.NotContains(t, f.tools, "click")
	}
}
