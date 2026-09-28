package applink

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestChatLink_CarriesAPositionOnlyWhenThereIsOne(t *testing.T) {
	const base = "lark://applink.feishu.cn/client/chat/open?openChatId=oc_quiet"
	require.Equal(t, base, ChatLink("oc_quiet", 0))
	require.Equal(t, base+"&position=42", ChatLink("oc_quiet", 42))
	// A thread reply is stored at -1, which is no position in the main flow:
	// sending it would land the client somewhere it cannot receipt from.
	require.Equal(t, base, ChatLink("oc_quiet", -1))
}

func TestLinks_UseTheSchemeThatReachesTheClientDirectly(t *testing.T) {
	for _, url := range []string{ChatLink("oc_quiet", 7), MeetingLink("123456789")} {
		// The https applink form opens a browser tab that only redirects here.
		require.True(t, strings.HasPrefix(url, "lark://"), "%s", url)
	}
	require.Equal(t, "lark://vc.feishu.cn/j/123456789", MeetingLink("123456789"))
}

func TestDefaultPace_LeavesTheClientTimeToDraw(t *testing.T) {
	// The client sends a receipt once it has rendered the chat it was walked
	// onto, so a gap shorter than a frame loses the chats it was hurried
	// through. Nothing here fixes the exact value — config's applink_pace_ms
	// is where it is retuned; what it must not be is 0.
	require.Positive(t, DefaultPaceMS)
	require.Equal(t, time.Duration(DefaultPaceMS)*time.Millisecond, DefaultPace)
}
