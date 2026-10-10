package cli

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

func TestTriageList_FiltersByLevelAndChat(t *testing.T) {
	a, st := candidatesApp(t)
	defer st.Close()
	ctx := t.Context()
	require.NoError(t, st.UpdateRendered(ctx, "om_ask", "接口什么时候好", "", 2))
	p := 0.62
	require.NoError(t, st.PutTriage(ctx, store.Triage{MessageID: "om_ask", ChatID: "oc_quiet", Level: "P0", Reason: "jev:reply", JevP: &p,
		Jev: jsontext.Value(`{"model":"jev-1.13.0","pick":{"reply":0.7},"fits":0.62,"nouls":{"to_reader":0.83}}`), JudgedMs: 2}))
	require.NoError(t, st.PutTriage(ctx, store.Triage{MessageID: "om_other", ChatID: "oc_other", Level: "P1", Reason: "p1", JudgedMs: 1}))

	out := a.Out.(*bytes.Buffer)
	a.jsonOut = true
	cmd := a.triageCmd()
	cmd.SetArgs([]string{"list", "--level", "P0"})
	require.NoError(t, cmd.Execute())
	var rows []store.TriageEntry
	require.NoError(t, json.Unmarshal(out.Bytes(), &rows))
	require.Len(t, rows, 1)
	require.Equal(t, "om_ask", rows[0].MessageID)
	require.Equal(t, "平台组", rows[0].ChatName)
	require.Equal(t, "接口什么时候好", rows[0].Content)
	require.JSONEq(t, `{"model":"jev-1.13.0","pick":{"reply":0.7},"fits":0.62,"nouls":{"to_reader":0.83}}`, string(rows[0].Jev),
		"the answer is embedded as JSON, not as a quoted string")

	out.Reset()
	cmd = a.triageCmd()
	cmd.SetArgs([]string{"list", "--chat", "平台组"})
	require.NoError(t, cmd.Execute())
	require.NoError(t, json.Unmarshal(out.Bytes(), &rows))
	require.Len(t, rows, 1, "the chat is resolved by name")

	out.Reset()
	a.jsonOut = false
	cmd = a.triageCmd()
	cmd.SetArgs([]string{"list"})
	require.NoError(t, cmd.Execute())
	require.Contains(t, out.String(), "jev:reply")
	require.Contains(t, out.String(), "0.62")
	require.Contains(t, out.String(), "to-me")
	require.Contains(t, out.String(), "0.83", "whether it was meant for the reader is read off beside the attention")
}

func TestTriageList_RefusesALevelThatIsNotOne(t *testing.T) {
	a, st := candidatesApp(t)
	defer st.Close()
	cmd := a.triageCmd()
	cmd.SetArgs([]string{"list", "--level", "urgent"})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	require.Error(t, cmd.Execute())
}
