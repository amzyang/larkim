package triage

import (
	"context"
	"encoding/json/v2"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/amzyang/larkim/jev"
	"github.com/stretchr/testify/require"
)

type fakeRanker struct {
	rank jev.Rank
	asks []jev.Ask
}

func (r *fakeRanker) Rank(_ context.Context, a jev.Ask) (jev.Rank, error) {
	r.asks = append(r.asks, a)
	return r.rank, nil
}

func TestJevJudge_CallsForTheReaderOnlyWhenAskedToActAndItCannotWait(t *testing.T) {
	t.Parallel()
	ask := GrayAsk{Message: text("om_q", "oc_team", "ou_a", "谁能看下线上报错"), Chat: group, Self: "ou_self", Reader: "林岚"}
	top := func(key string) []jev.Option { return []jev.Option{{Key: key, P: 0.6}, {Key: "chatter", P: 0.1}} }
	for name, tc := range map[string]struct {
		options []jev.Option
		fits    float64
		toMe    float64
		level   Level
		reason  string
	}{
		"a request that cannot wait":          {top("act"), attention + 0.01, addressed + 0.01, P0, "jev:act"},
		"a question that cannot wait":         {top("reply"), attention + 0.01, addressed + 0.01, P0, "jev:reply"},
		"a request that can wait":             {top("act"), attention - 0.01, 0.99, P1, "jev:act"},
		"news that cannot wait is news":       {top("fyi"), 0.99, 0.99, P1, "jev:fyi"},
		"someone else's request is not yours": {top("act"), 0.99, addressed - 0.01, P1, "jev:others"},
		"an ask split over reply and act outweighs news": {
			[]jev.Option{{Key: "fyi", P: 0.40}, {Key: "reply", P: 0.35}, {Key: "act", P: 0.25}}, 0.99, 0.99, P0, "jev:reply"},
		"news that outweighs the ask is news": {
			[]jev.Option{{Key: "fyi", P: 0.55}, {Key: "reply", P: 0.25}, {Key: "act", P: 0.20}}, 0.99, 0.99, P1, "jev:fyi"},
	} {
		t.Run(name, func(t *testing.T) {
			rank := jev.Rank{Options: tc.options, Fits: tc.fits, Nouls: map[string]float64{ToReader: tc.toMe}}
			r := &fakeRanker{rank: rank}
			v, err := JevJudge{Ranker: r}.Judge(t.Context(), ask)
			require.NoError(t, err)
			require.Equal(t, tc.level, v.Level)
			require.Equal(t, tc.reason, v.Reason)
			require.InDelta(t, tc.fits, *v.JevP, 1e-9)
			require.Equal(t, &rank, v.Jev, "the whole answer is kept, so a cut can be re-read off it")
		})
	}
	r := &fakeRanker{rank: jev.Rank{Options: []jev.Option{{Key: "fyi", P: 1}}, Nouls: map[string]float64{ToReader: 1}}}
	_, err := JevJudge{Ranker: r}.Judge(t.Context(), ask)
	require.NoError(t, err)
	require.Len(t, r.asks, 1, "every question rides one request")
	require.ElementsMatch(t, []string{"reply", "act", "fyi", "chatter"}, slices.Collect(maps.Keys(r.asks[0].Options)))
	require.NotEmpty(t, r.asks[0].Fits, "the attention question rides the same call")
	require.Contains(t, r.asks[0].Nouls, ToReader, "whom it is for rides the same call too")
	require.Equal(t, "林岚", r.asks[0].State.(grayState).Reader, "a message that names the reader can only be read as theirs if their name is known")
}

func TestJevJudge_StateNamesTheReaderFieldEvenWhenTheNameIsUnknown(t *testing.T) {
	t.Parallel()
	r := &fakeRanker{rank: jev.Rank{Options: []jev.Option{{Key: "fyi", P: 1}}, Nouls: map[string]float64{ToReader: 1}}}
	_, err := JevJudge{Ranker: r}.Judge(t.Context(), GrayAsk{Message: text("om_q", "oc_team", "ou_a", "x"), Chat: group, Self: "ou_self"})
	require.NoError(t, err)
	b, err := json.Marshal(r.asks[0].State)
	require.NoError(t, err)
	require.Contains(t, string(b), `"reader":""`, "the to_reader question names `reader`, so the path must exist")
}

func TestJevJudge_TellsJevTheNamesOnlyTheReaderGoesBy(t *testing.T) {
	t.Parallel()
	r := &fakeRanker{rank: jev.Rank{Options: []jev.Option{{Key: "fyi", P: 1}}, Nouls: map[string]float64{ToReader: 1}}}
	ask := GrayAsk{Message: text("om_q", "oc_team", "ou_a", "小林 帮忙看下"), Chat: group, Self: "ou_self", Reader: "林岚", Aliases: []string{"小林"}}
	_, err := JevJudge{Ranker: r}.Judge(t.Context(), ask)
	require.NoError(t, err)
	require.Equal(t, []string{"小林"}, r.asks[0].State.(grayState).ReaderAliases)
	require.Contains(t, r.asks[0].Nouls[ToReader].Instructions, "`reader_aliases`", "a name without an @ is the reader's only if the question says so")

	r = &fakeRanker{rank: r.rank}
	_, err = JevJudge{Ranker: r}.Judge(t.Context(), GrayAsk{Message: text("om_q", "oc_team", "ou_a", "x"), Chat: group, Self: "ou_self"})
	require.NoError(t, err)
	b, err := json.Marshal(r.asks[0].State)
	require.NoError(t, err)
	require.Contains(t, string(b), `"reader_aliases":[]`, "the question names `reader_aliases`, so the path must exist")
}

func TestJudgment_IsTheAnswerAsStored(t *testing.T) {
	t.Parallel()
	j := NewJudgment(jev.Rank{Model: "jev-1.13.0", Fits: 0.7,
		Options: []jev.Option{{Key: "reply", P: 0.6}, {Key: "fyi", P: 0.4}}, Nouls: map[string]float64{ToReader: 0.2}})
	require.Equal(t, Judgment{Model: "jev-1.13.0", Fits: 0.7, Pick: map[string]float64{"reply": 0.6, "fyi": 0.4},
		Nouls: map[string]float64{ToReader: 0.2}}, j)
}

type fakeAnswerer struct {
	answer string
	prompt string
}

func (a *fakeAnswerer) Answer(_ context.Context, transcript, prompt, instructions string) (string, error) {
	a.prompt = prompt
	return a.answer, nil
}

func TestAgentDrafter_ReadsTheAnswersJSON(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 10, 9, 0, 0, 0, time.Local)
	for name, tc := range map[string]struct {
		answer string
		want   Draft
	}{
		"plain": {`{"drafts":["收到","我看下再回你"],"format":"text","remind":null}`,
			Draft{Texts: []string{"收到", "我看下再回你"}, Format: "text"}},
		"fenced with a reminder": {"```json\n{\"drafts\":[],\"format\":\"text\",\"remind\":{\"at\":\"2026-10-10 13:58\",\"title\":\"14:00 前交周报\"}}\n```",
			Draft{Format: "text", Remind: &Remind{At: time.Date(2026, 10, 10, 13, 58, 0, 0, time.Local), Title: "14:00 前交周报"}}},
		"more than three is three": {`{"drafts":["一","二","三","四"]}`,
			Draft{Texts: []string{"一", "二", "三"}, Format: "text"}},
		"markdown": {`{"drafts":["**好**"],"format":"markdown"}`, Draft{Texts: []string{"**好**"}, Format: "markdown"}},
		"reactions in their wire spelling": {`{"drafts":["周四好"],"reactions":["thumbsup","Lark_Emoji_OnIt_0"]}`,
			Draft{Texts: []string{"周四好"}, Reactions: []string{"THUMBSUP", "OnIt"}, Format: "text"}},
		"a reaction-only answer": {`{"drafts":[],"reactions":["Get"]}`,
			Draft{Reactions: []string{"Get"}, Format: "text"}},
		"unknown and refused reactions are dropped": {`{"drafts":["好"],"reactions":["NoSuchEmoji","ATTENTION","OK"]}`,
			Draft{Texts: []string{"好"}, Reactions: []string{"OK"}, Format: "text"}},
		"reactions dedupe and cap at three": {`{"reactions":["OK","ok","DONE","Get","LGTM"]}`,
			Draft{Reactions: []string{"OK", "DONE", "Get"}, Format: "text"}},
	} {
		t.Run(name, func(t *testing.T) {
			a := &fakeAnswerer{answer: tc.answer}
			d, err := AgentDrafter{Answerer: a}.Draft(t.Context(), DraftAsk{ChatName: "张三", Reader: "林岚", Target: "[10-10 09:00] 张三: 在吗", Now: now})
			require.NoError(t, err)
			require.Equal(t, tc.want, d)
			require.Contains(t, a.prompt, "2026-10-10 09:00", "the drafter is told what time it is")
			require.Contains(t, a.prompt, "用户：林岚", "the drafter is told who the user is, who may have no line in the window")
			require.Contains(t, a.prompt, "张三: 在吗")
		})
	}
}

func TestAgentDrafter_TellsTheDrafterTheUsersOtherNames(t *testing.T) {
	t.Parallel()
	a := &fakeAnswerer{answer: `{"drafts":[]}`}
	_, err := AgentDrafter{Answerer: a}.Draft(t.Context(), DraftAsk{ChatName: "项目协作群", Reader: "林岚", Aliases: []string{"小林", "岚姐"},
		Target: "[10-10 09:00] 张三: 小林 帮忙看下", Now: time.Now()})
	require.NoError(t, err)
	require.Contains(t, a.prompt, "用户：林岚（也被叫作：小林、岚姐）")
}

func TestAgentDrafter_RefusesAnAnswerThatIsNotTheJSON(t *testing.T) {
	t.Parallel()
	for _, answer := range []string{"好的，我来起草", `{"drafts":["x"],"format":"html"}`, `{"remind":{"at":"明天","title":"x"}}`} {
		_, err := AgentDrafter{Answerer: &fakeAnswerer{answer: answer}}.Draft(t.Context(), DraftAsk{Now: time.Now()})
		require.Error(t, err, answer)
	}
}

func TestAlerterArgs_OffersJoinOnlyForACall(t *testing.T) {
	t.Parallel()
	plain := alerterArgs(Banner{Title: "张三", Body: "-在吗", Icon: "/Users/linlan/.larkim/avatars/a.png"})
	require.Equal(t, []string{"--title", "张三", "--message", " -在吗", "--close-label", "Dismiss",
		"--timeout", "60", "--ignore-dnd", "--app-icon", "/Users/linlan/.larkim/avatars/a.png"}, plain,
		"a leading dash would read as a flag")
	call := alerterArgs(Banner{Title: "项目协作群", Body: "张三: 站会", JoinLink: "lark://vc.feishu.cn/j/100000000"})
	require.Contains(t, call, "--actions")
	require.Equal(t, ActionJoin, call[slices.Index(call, "--actions")+1])
}

func TestParseHIDIdle_ReadsNanoseconds(t *testing.T) {
	t.Parallel()
	require.Equal(t, 2500*time.Millisecond, parseHIDIdle(`  |   "HIDIdleTime" = 2500000000`))
	require.Equal(t, time.Duration(-1), parseHIDIdle("nothing here"))
}

func TestParseBundleID_ReadsEitherSpelling(t *testing.T) {
	t.Parallel()
	require.Equal(t, "net.kovidgoyal.kitty", parseBundleID(`"CFBundleIdentifier"="net.kovidgoyal.kitty"`))
	require.Equal(t, "com.apple.loginwindow", parseBundleID("[ NULL ]  ASN:0x0-0x1001: (in front) \n    bundleID=\"com.apple.loginwindow\"\n"))
	require.Empty(t, parseBundleID("nothing"))
}

func TestIsLarkBundle_KnowsTheWhiteLabelBuild(t *testing.T) {
	t.Parallel()
	require.True(t, isLarkBundle("com.dancesuite.dance.ka.sagtjy516.mac"))
	require.True(t, isLarkBundle("com.electron.lark"))
	require.False(t, isLarkBundle("net.kovidgoyal.kitty"))
}
