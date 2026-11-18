package automation

import (
	"testing"
	"time"

	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

func testRule() Rule {
	return Rule{ID: "quota", Enabled: true,
		Criteria: Criteria{ChatIDs: []string{"oc_quiet"}, SenderIDs: []string{"ou_bot"}, MessageTypes: []string{"interactive"}, Contains: []string{"quota", "claim"}, Excludes: []string{"completed"}},
		Callback: Callback{Executable: "/usr/bin/true", Timeout: time.Second}}
}

func testMessage() store.Message {
	return store.Message{MessageID: "om_quota", ChatID: "oc_quiet", SenderID: "ou_bot", SenderType: "app", MsgType: "interactive", CreateMs: 200, UpdateMs: 200, Content: "quota: claim", RenderedAt: 201}
}

func TestCriteria_MatchesCurrentRenderedMessages(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*store.Message)
		want   bool
	}{
		{"match", func(m *store.Message) {}, true},
		{"chat", func(m *store.Message) { m.ChatID = "oc_elsewhere" }, false},
		{"sender", func(m *store.Message) { m.SenderID = "ou_other" }, false},
		{"type", func(m *store.Message) { m.MsgType = "text" }, false},
		{"missing phrase", func(m *store.Message) { m.Content = "quota" }, false},
		{"excluded", func(m *store.Message) { m.Content += " completed" }, false},
		{"recalled", func(m *store.Message) { m.Deleted = true }, false},
		{"stale rendering", func(m *store.Message) { m.RenderedAt = 0 }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := testMessage()
			tc.change(&m)
			_, match := testRule().Criteria.Match(m)
			require.Equal(t, tc.want, match)
		})
	}
}

func TestValidateRules_RejectsAmbiguousOrUnboundedCallbacks(t *testing.T) {
	t.Parallel()
	require.NoError(t, ValidateRules([]Rule{testRule()}))
	for _, change := range []func(*Rule){
		func(r *Rule) { r.ID = "" },
		func(r *Rule) { r.Callback.Executable = "" },
		func(r *Rule) { r.Callback.Timeout = 0 },
		func(r *Rule) { r.Criteria.Contains = []string{""} },
	} {
		r := testRule()
		change(&r)
		require.Error(t, ValidateRules([]Rule{r}))
	}
	require.Error(t, ValidateRules([]Rule{testRule(), testRule()}))
}
