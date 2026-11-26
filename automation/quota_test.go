package automation

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestQuotaRule_DisabledPresetHasExplicitScope(t *testing.T) {
	t.Parallel()
	r := QuotaRule("quota", "/Users/linlan/bin/larkim", "oc_quiet", "ou_bot")
	require.False(t, r.Enabled)
	require.NoError(t, ValidateRules([]Rule{r}))
	require.Equal(t, 60*time.Second, r.Callback.Timeout)
	require.Equal(t, []string{"申请加油包"}, r.Criteria.Contains)
	require.Equal(t, []string{"未达加油条件"}, r.Criteria.Excludes)
	chat, sender, ok := QuotaScope(r)
	require.True(t, ok)
	require.Equal(t, "oc_quiet", chat)
	require.Equal(t, "ou_bot", sender)
	r.Criteria.ChatIDs = []string{"oc_elsewhere"}
	_, _, ok = QuotaScope(r)
	require.False(t, ok)
}

func TestQuotaScope_RejectsArbitraryCommandsAndMissingScopes(t *testing.T) {
	t.Parallel()
	for _, change := range []func(*Rule){
		func(r *Rule) { r.Callback.Args = []string{"anything"} },
		func(r *Rule) { r.Callback.Args = append(r.Callback.Args, "--check") },
		func(r *Rule) { r.Criteria.SenderIDs = nil },
		func(r *Rule) { r.Criteria.ChatIDs = []string{"oc_quiet", "oc_elsewhere"} },
		func(r *Rule) { r.Callback.Args[3] = ""; r.Criteria.ChatIDs = []string{""} },
	} {
		r := QuotaRule("quota", "/Users/linlan/bin/larkim", "oc_quiet", "ou_bot")
		change(&r)
		_, _, ok := QuotaScope(r)
		require.False(t, ok)
	}
}
