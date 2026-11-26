package cli

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/amzyang/larkim/automation"
	"github.com/amzyang/larkim/cuacallback"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/store/storetest"
	"github.com/stretchr/testify/require"
)

const checkQuotaText = `🟠 AI Coding 总额度已用 91% · 可申请加油包
账号：linlan
当前时段：12:00-17:00（17:00 自动重置）
主力额度合计（Claude/GPT）：$273.00 / $300.00（91%） · 本时段已加油 1 次
Claude：$68.00 / $75.00（91%）
GPT：$205.00 / $225.00（91%）
再次申请加油包（+50%）
已满足加油条件`

func TestCheckQuota_UsesLatestEligibleLocalMessageAndNeverExecutesCallback(t *testing.T) {
	t.Parallel()
	st, err := storetest.Open(t, filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	now := time.Date(2026, 11, 18, 15, 0, 0, 0, time.FixedZone("UTC+8", 8*3600))
	require.NoError(t, st.UpsertChats(t.Context(), []store.Chat{{ChatID: "oc_quiet"}}, now.UnixMilli()))
	m := store.Message{MessageID: "om_quota", ChatID: "oc_quiet", SenderID: "ou_bot", MsgType: "interactive", CreateMs: now.Add(-time.Minute).UnixMilli(), MessagePosition: 33}
	_, err = st.UpsertMessages(t.Context(), []store.Message{m}, now.UnixMilli())
	require.NoError(t, err)
	require.NoError(t, st.UpdateRendered(t.Context(), m.MessageID, checkQuotaText, "", now.UnixMilli()))
	r := automation.QuotaRule("quota", "/Users/linlan/bin/larkim", "oc_quiet", "ou_bot")
	calls := 0
	check := quotaChecker{Store: st, Executable: r.Callback.Executable, Now: func() time.Time { return now }, Inspect: func(_ context.Context, q cuacallback.Quota) automation.Outcome {
		calls++
		require.Contains(t, q.URL, "position=33")
		return automation.Outcome{Status: "failed", Result: "check passed: no claim submitted"}
	}}
	result, err := check.Check(t.Context(), r)
	require.NoError(t, err)
	require.Contains(t, result, "check passed")
	require.Equal(t, 1, calls)
	check.Inspect = func(context.Context, cuacallback.Quota) automation.Outcome {
		calls++
		return automation.Outcome{Status: "failed", Result: "AX window unavailable"}
	}
	_, err = check.Check(t.Context(), r)
	require.ErrorContains(t, err, "AX window unavailable")
	require.Equal(t, 2, calls)
	r.Callback.Executable = "/bin/sh"
	_, err = check.Check(t.Context(), r)
	require.Error(t, err)
	require.Equal(t, 2, calls)
	r.Callback.Executable = check.Executable
	check.Now = func() time.Time { return now.Add(25 * time.Hour) }
	_, err = check.Check(t.Context(), r)
	require.Error(t, err)
	require.Equal(t, 2, calls)
}

func TestCheckQuota_SkipsIneligibleCards(t *testing.T) {
	t.Parallel()
	st, err := storetest.Open(t, filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	now := time.Date(2026, 11, 18, 15, 0, 0, 0, time.FixedZone("UTC+8", 8*3600))
	require.NoError(t, st.UpsertChats(t.Context(), []store.Chat{{ChatID: "oc_quiet"}}, now.UnixMilli()))
	m := store.Message{MessageID: "om_warn", ChatID: "oc_quiet", SenderID: "ou_bot", MsgType: "interactive", CreateMs: now.Add(-time.Minute).UnixMilli(), MessagePosition: 34}
	_, err = st.UpsertMessages(t.Context(), []store.Message{m}, now.UnixMilli())
	require.NoError(t, err)
	require.NoError(t, st.UpdateRendered(t.Context(), m.MessageID, "🟡 GPT 额度预警（已用 86%）\n需合计用满 80% 后方可申请加油包\n[未达加油条件（需合计达 80%）]", "", now.UnixMilli()))
	r := automation.QuotaRule("quota", "/Users/linlan/bin/larkim", "oc_quiet", "ou_bot")
	check := quotaChecker{Store: st, Executable: r.Callback.Executable, Now: func() time.Time { return now }, Inspect: func(context.Context, cuacallback.Quota) automation.Outcome {
		t.Fatal("an ineligible card must not reach the client")
		return automation.Outcome{}
	}}
	_, err = check.Check(t.Context(), r)
	require.ErrorContains(t, err, "no matching quota card")
}
