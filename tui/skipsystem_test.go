package tui

import (
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

// kinds builds a list from a pattern: 'm' is a message, 's' a system notice,
// 'r' a recalled message.
func kinds(p string) []store.Message {
	var out []store.Message
	for i, c := range p {
		x := store.Message{MessageID: "om_" + string(rune('a'+i)), ChatID: "oc_1", SenderName: "张三",
			SenderID: "ou_a", Content: "line", RenderedAt: 1, CreateMs: int64(i) * 1000}
		if c == 's' {
			x.MsgType, x.Content = "system", "李四 joined the group"
		}
		if c == 'r' {
			x.Deleted = true
		}
		out = append(out, x)
	}
	return out
}

func TestStepCursor_PassesOverNotices(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pattern string
		from, n int
		want    int
	}{
		{"one notice down", "msm", 0, 1, 2},
		{"a run of notices up", "mssm", 3, -1, 0},
		{"only notices below stays put", "mss", 0, 1, 0},
		{"only notices above stays put", "ssm", 2, -1, 2},
		{"a page landing on a notice goes on", "mmmsm", 0, 3, 4},
		{"a page past the last message backs up to it", "mmss", 0, 10, 1},
		{"gg over leading notices", "ssmm", 3, -1 << 30, 2},
		{"G over trailing notices", "mmss", 0, 1 << 30, 1},
		{"a cursor on a notice with nothing beyond stays", "mms", 2, 1, 2},
		{"all notices", "sss", 1, 1, 1},
		{"a recall down", "mrm", 0, 1, 2},
		{"recalls and notices mixed up", "msrm", 3, -1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, stepCursor(kinds(tc.pattern), tc.from, tc.n))
		})
	}
}

func TestNewestSelectable_SkipsTrailingNotices(t *testing.T) {
	require.Equal(t, 1, newestSelectable(kinds("mmss")))
	require.Equal(t, 2, newestSelectable(kinds("mmm")))
	require.Equal(t, 1, newestSelectable(kinds("ss")), "a list of notices alone keeps its last row")
	require.Equal(t, 0, newestSelectable(nil))
}

func TestMove_SkipsNoticesInMessages(t *testing.T) {
	m := sized(120, 36)
	m.msgs = kinds("msrm")
	m.layout()
	m.focus, m.msgIdx = paneMessages, 0

	mm, _ := m.onNormalKey("j")
	m = mm.(Model)
	require.Equal(t, 3, m.msgIdx)

	mm, _ = m.onNormalKey("k")
	m = mm.(Model)
	require.Equal(t, 0, m.msgIdx)
}

func TestMove_SkipsNoticesInThread(t *testing.T) {
	m := sized(120, 36)
	m.thread = kinds("msrm")
	m.focus, m.threadIdx = paneThread, 0
	m.rebuildThread()

	mm, _ := m.onNormalKey("j")
	m = mm.(Model)
	require.Equal(t, 3, m.threadIdx)
}

func TestMove_UpAgainstLeadingNoticesShowsTheTop(t *testing.T) {
	m := sized(120, 36)
	m.msgs = kinds("sssssssssssssssssssssssssssssm")
	m.layout()
	m.focus, m.msgIdx = paneMessages, len(m.msgs)-1
	m.scrollMessagesToSelection()
	require.Positive(t, m.msgTop, "the fixture must start scrolled away from the top")

	mm, _ := m.onNormalKey("k")
	m = mm.(Model)
	require.Equal(t, len(m.msgs)-1, m.msgIdx, "no message above to stop on")
	require.Zero(t, m.msgTop, "the notices above are shown, so reaching the top still loads what lies older")
}

func TestMessagesLoaded_LandsOnTheNewestMessageAboveNotices(t *testing.T) {
	m := sized(120, 36)
	m.pendingChat = "oc_1"
	mm, _ := m.Update(messagesLoadedMsg{chatID: "oc_1", msgs: kinds("mms")})
	m = mm.(Model)
	require.Equal(t, 1, m.msgIdx)

	// The cursor on the newest message still follows one that arrives.
	mm, _ = m.Update(messagesLoadedMsg{chatID: "oc_1", msgs: kinds("mmsm")})
	m = mm.(Model)
	require.Equal(t, 3, m.msgIdx)
}
