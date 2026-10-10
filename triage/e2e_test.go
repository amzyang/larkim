package triage

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/jev"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

var update = flag.Bool("update", false, "rewrite testdata/e2e.golden.json")

// TestTriager_EndToEnd runs one morning's arrivals through every stage and
// compares what the store and the desktop end up with to a file that is read
// by eye: go test ./triage -run EndToEnd -update rewrites it.
func TestTriager_EndToEnd(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.tr.SetRules(NewRules(config.Notifications{Watch: config.Strings{"ou_boss"}, Keywords: config.Strings{"故障"}}))
	p := 0.81
	h.tr.Judge = &fakeJudge{verdict: Verdict{Level: P0, Reason: "jev:act", JevP: &p, Jev: &jev.Rank{Model: "jev-1.13.0", Fits: p,
		Options: []jev.Option{{Key: "act", P: 0.7}, {Key: "reply", P: 0.2}, {Key: "fyi", P: 0.1}}, Nouls: map[string]float64{ToReader: 0.9}}}}
	h.tr.Drafter = &fakeDrafter{draft: Draft{Texts: []string{"在的，直接说就行"}, Format: "text",
		Remind: &Remind{At: t0.Add(20 * time.Minute), Title: "12:20 评审"}}, remindOn: "12:20 评审"}
	h.notifier.action = ActionOpen

	at := text("om_at", "oc_team", "ou_a", "@林岚 帮忙看下")
	at.MentionsJSON = `[{"id":"ou_self","key":"@_user_1","name":"林岚"}]`
	boss := text("om_boss", "oc_team", "ou_boss", "进度怎样")
	boss.SenderName = "李四"
	// In a chat of its own: a reply in oc_team would rightly hold back every
	// banner before it there.
	mine := text("om_mine", "oc_muted", "ou_self", "我看下")
	mine.SenderName = "林岚"
	for i, m := range []store.Message{
		text("om_dm", "oc_peer", "ou_a", "12:20 评审，记得来"),
		at,
		boss,
		text("om_kw", "oc_team", "ou_a", "支付故障了"),
		text("om_gray", "oc_team", "ou_a", "谁有空看下这个报错"),
		text("om_quiet", "oc_muted", "ou_a", "周末去哪"),
		mine,
	} {
		h.arrive(t, m, time.Duration(70-10*i)*time.Second)
	}
	h.pass(t)
	require.NoError(t, h.tr.DraftPass(t.Context()))
	h.clock.t = t0.Add(20 * time.Minute)
	h.pass(t)

	rows, err := h.st.ListTriage(t.Context(), store.TriageQuery{})
	require.NoError(t, err)
	cands, err := h.st.ChatCandidates(t.Context(), "", "ou_self")
	require.NoError(t, err)
	got, err := json.Marshal(map[string]any{
		"triage": rows, "banners": h.notifier.shown(), "opened_in_client": h.desktop.urls(), "candidates": cands,
	}, json.Deterministic(true), jsontext.WithIndent("  "))
	require.NoError(t, err)

	path := filepath.Join("testdata", "e2e.golden.json")
	if *update {
		require.NoError(t, os.MkdirAll("testdata", 0o755))
		require.NoError(t, os.WriteFile(path, append(got, '\n'), 0o644))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, string(want), string(got)+"\n")
}
