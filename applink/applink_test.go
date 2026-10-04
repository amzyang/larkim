package applink

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChatLink_CarriesAPositionOnlyWhenThereIsOne(t *testing.T) {
	const base = "lark://applink.feishu.cn/client/chat/open?openChatId=oc_quiet"
	require.Equal(t, base, ChatLink("oc_quiet", "", 0))
	require.Equal(t, base+"&position=42", ChatLink("oc_quiet", "", 42))
	// A thread reply is stored at -1, which is no position in the main flow:
	// sending it would land the client somewhere it cannot receipt from.
	require.Equal(t, base, ChatLink("oc_quiet", "", -1))
}

func TestChatLink_CarriesAMessageIdForLaterReaders(t *testing.T) {
	// The client navigates by position; the id is for the readers the link
	// leaves behind — a yanked row, a task filed elsewhere — so it rides
	// along whenever one is at hand, position or no position.
	require.Equal(t,
		"lark://applink.feishu.cn/client/chat/open?openChatId=oc_quiet&position=42&messageId=om_a",
		ChatLink("oc_quiet", "om_a", 42))
	require.Equal(t,
		"lark://applink.feishu.cn/client/chat/open?openChatId=oc_quiet&messageId=om_a",
		ChatLink("oc_quiet", "om_a", -1))
}

func TestLinks_UseTheSchemeThatReachesTheClientDirectly(t *testing.T) {
	for _, url := range []string{ChatLink("oc_quiet", "", 7), MeetingLink("123456789"),
		EventLink("cal_team", "evt-a_0", 1788143400000)} {
		// The https applink form opens a browser tab that only redirects here.
		require.True(t, strings.HasPrefix(url, "lark://"), "%s", url)
	}
	require.Equal(t, "lark://vc.feishu.cn/j/123456789", MeetingLink("123456789"))
}

func TestTodoLink_AddressesTheTaskByItsGuid(t *testing.T) {
	t.Parallel()
	require.Equal(t, "lark://applink.feishu.cn/client/todo/detail?guid=7bc2f1b2-5660-44cf-a3dc-2300acbc22c3",
		TodoLink("7bc2f1b2-5660-44cf-a3dc-2300acbc22c3"))
	require.Empty(t, TodoLink(""), "a body naming no task is not addressed")
}

func TestEventLink_SplitsTheEventIdIntoKeyAndOriginalTime(t *testing.T) {
	// The API names an event with key and originalTime joined, while the
	// client's own URL wants them apart.
	require.Equal(t,
		"lark://applink.feishu.cn/client/calendar/event/detail?"+
			"calendarId=cal_team&key=evt-a&originalTime=0&startTime=1788143400",
		EventLink("cal_team", "evt-a_0", 1788143400000))
	// A recurring instance carries the time of the occurrence it was cut from.
	require.Contains(t, EventLink("cal_team", "evt-a_1788143400", 1788143400000),
		"originalTime=1788143400")
	// An id with no original time at all is the whole key, and the client is
	// still given one.
	require.Contains(t, EventLink("cal_team", "evt-a", 0), "key=evt-a&originalTime=0")
}

func TestEventLink_EscapesTheCalendarId(t *testing.T) {
	// A shared calendar's id is an address, and @ ends a query value.
	require.Contains(t, EventLink("feishu.cn_x@group.calendar.feishu.cn", "evt-a_0", 0),
		"calendarId=feishu.cn_x%40group.calendar.feishu.cn")
}

func TestEventLink_IsEmptyWithoutBothIds(t *testing.T) {
	require.Empty(t, EventLink("", "evt-a_0", 1788143400000))
	require.Empty(t, EventLink("cal_team", "", 1788143400000))
}
