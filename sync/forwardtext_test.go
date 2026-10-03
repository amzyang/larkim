package sync

import (
	"testing"
	"time"

	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

// fwdAt is the millisecond stamp the bundle tests date their children by.
const fwdAt = 1790071200000 // 2026-09-22T10:00:00Z

// kid is one stored child of om_fwd, at the top level unless upper says else.
func kid(id, upper, msgType, contentRaw string) store.Forwarded {
	return store.Forwarded{
		RootMessageID: "om_fwd", UpperMessageID: upper, MessageID: id,
		ChatID: "oc_elsewhere", MsgType: msgType, SenderID: "ou_a", SenderName: "张三",
		CreateMs: fwdAt, ContentRaw: contentRaw,
	}
}

func TestForwardText_SpellsABundleTheWayLarkCLIDoes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		kids []store.Forwarded
		want string
	}{
		{"a bundle with nothing in it", nil, "<forwarded_messages/>"},
		{"one message", []store.Forwarded{kid("om_a", "om_fwd", "text", `{"text":"你好"}`)},
			"<forwarded_messages>\n[2026-09-22T10:00:00Z] 张三:\n    你好\n</forwarded_messages>"},
		{"two messages", []store.Forwarded{
			kid("om_a", "om_fwd", "text", `{"text":"你好"}`),
			kid("om_b", "om_fwd", "text", `{"text":"在"}`),
		}, "<forwarded_messages>\n[2026-09-22T10:00:00Z] 张三:\n    你好\n" +
			"[2026-09-22T10:00:00Z] 张三:\n    在\n</forwarded_messages>"},
		{"a message of several paragraphs keeps the offset on each",
			[]store.Forwarded{kid("om_a", "om_fwd", "text", `{"text":"<p>第一行</p><p>第二行</p>"}`)},
			"<forwarded_messages>\n[2026-09-22T10:00:00Z] 张三:\n    第一行\n    第二行\n</forwarded_messages>"},
		{"an attachment keeps the tag its readers match", []store.Forwarded{
			kid("om_a", "om_fwd", "file", `{"file_key":"file_a","file_name":"纪要.pdf"}`),
		}, "<forwarded_messages>\n[2026-09-22T10:00:00Z] 张三:\n    <file key=\"file_a\" name=\"纪要.pdf\"/>\n</forwarded_messages>"},
		{"a bundle inside a bundle opens in place", []store.Forwarded{
			kid("om_inner", "om_fwd", "merge_forward", `{"text":"Merged and Forwarded Message"}`),
			kid("om_deep", "om_inner", "text", `{"text":"里面"}`),
		}, "<forwarded_messages>\n[2026-09-22T10:00:00Z] 张三:\n" +
			"    <forwarded_messages>\n    [2026-09-22T10:00:00Z] 张三:\n        里面\n    </forwarded_messages>\n" +
			"</forwarded_messages>"},
		{"a nested bundle nobody expanded", []store.Forwarded{
			kid("om_inner", "om_fwd", "merge_forward", `{"text":"Merged and Forwarded Message"}`),
		}, "<forwarded_messages>\n[2026-09-22T10:00:00Z] 张三:\n    <forwarded_messages/>\n</forwarded_messages>"},
		{"a type larkim has no renderer for names itself", []store.Forwarded{
			kid("om_a", "om_fwd", "brand_new", `{}`),
		}, "<forwarded_messages>\n[2026-09-22T10:00:00Z] 张三:\n    [brand_new]\n</forwarded_messages>"},
		{"a child carrying no body at all", []store.Forwarded{kid("om_a", "om_fwd", "text", "")},
			"<forwarded_messages>\n[2026-09-22T10:00:00Z] 张三:\n    \n</forwarded_messages>"},
		{"a child with no type reads as words", []store.Forwarded{kid("om_a", "om_fwd", "", `{"text":"你好"}`)},
			"<forwarded_messages>\n[2026-09-22T10:00:00Z] 张三:\n    你好\n</forwarded_messages>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, ForwardText("om_fwd", tc.kids, time.UTC))
		})
	}
}

func TestForwardText_NamesWhoeverTheExpansionCarried(t *testing.T) {
	t.Parallel()
	named := kid("om_a", "om_fwd", "text", `{"text":"你好"}`)
	byID := named
	byID.SenderName = ""
	anonymous := byID
	anonymous.SenderID = ""

	for want, k := range map[string]store.Forwarded{"张三": named, "ou_a": byID, "unknown": anonymous} {
		require.Contains(t, ForwardText("om_fwd", []store.Forwarded{k}, time.UTC), "] "+want+":\n")
	}
}

func TestForwardText_SaysUnknownForAChildItCannotDate(t *testing.T) {
	t.Parallel()
	k := kid("om_a", "om_fwd", "text", `{"text":"你好"}`)
	k.CreateMs = 0
	require.Equal(t, "<forwarded_messages>\n[unknown] 张三:\n    你好\n</forwarded_messages>",
		ForwardText("om_fwd", []store.Forwarded{k}, time.UTC))
}

func TestForwardText_ResolvesTheMentionsAChildCarried(t *testing.T) {
	t.Parallel()
	k := kid("om_a", "om_fwd", "text", `{"text":"@_user_1 在吗"}`)
	k.MentionsJSON = `[{"id":"ou_b","key":"@_user_1","name":"李四"}]`
	require.Contains(t, ForwardText("om_fwd", []store.Forwarded{k}, time.UTC), "    @李四 在吗\n")
}

func TestForwardText_StopsAtABundleThatNamesItsOwnAncestor(t *testing.T) {
	t.Parallel()
	// upper_message_id comes off the wire, so nothing but this bounds the walk.
	require.NotPanics(t, func() {
		out := ForwardText("om_fwd", []store.Forwarded{
			kid("om_fwd", "om_fwd", "merge_forward", `{"text":"Merged and Forwarded Message"}`),
		}, time.UTC)
		require.Contains(t, out, "<forwarded_messages/>")
	})
}

func TestForwardText_KeepsThePictureSpellingTheBackScanReads(t *testing.T) {
	t.Parallel()
	// registerExistingResources finds a bundle's pictures nowhere but here.
	out := ForwardText("om_fwd", []store.Forwarded{
		kid("om_a", "om_fwd", "image", `{"image_key":"img_a"}`),
		kid("om_b", "om_fwd", "post", `{"content":[[{"tag":"img","image_key":"img_b"}]]}`),
	}, time.UTC)

	require.Equal(t, []store.ResourceRef{
		{MessageID: "om_fwd", FileKey: "img_a", Type: "image"},
		{MessageID: "om_fwd", FileKey: "img_b", Type: "image"},
	}, ExtractRendered("om_fwd", "merge_forward", out))
}
