package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseDocURL_ReadsTheDocumentThePathNames(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want DocRef
	}{
		{"https://example.feishu.cn/docx/AbC123", DocRef{"docx", "AbC123"}},
		{"https://example.feishu.cn/docs/AbC123", DocRef{"doc", "AbC123"}},
		{"https://feishu.cn/docx/AbC123", DocRef{"docx", "AbC123"}},
		{"https://example.feishu.cn/wiki/AbC123", DocRef{"wiki", "AbC123"}},
		{"https://example.feishu.cn/sheets/AbC123", DocRef{"sheet", "AbC123"}},
		{"https://example.feishu.cn/base/AbC123", DocRef{"bitable", "AbC123"}},
		{"https://example.feishu.cn/bitable/AbC123", DocRef{"bitable", "AbC123"}},
		{"https://example.feishu.cn/drive/folder/AbC123", DocRef{"folder", "AbC123"}},
		{"https://example.feishu.cn/mindnote/AbC123", DocRef{"mindnote", "AbC123"}},
	} {
		got, ok := ParseDocURL(tc.raw)
		require.True(t, ok, tc.raw)
		require.Equal(t, tc.want, got, "the path spells the type, which is not always the API's word for it")
	}
}

func TestParseDocURL_IgnoresWhatFollowsThePath(t *testing.T) {
	plain, ok := ParseDocURL("https://example.feishu.cn/docx/AbC123")
	require.True(t, ok)
	decorated, ok := ParseDocURL("https://example.feishu.cn/docx/AbC123?from=from_copylink#doxcnBlock")
	require.True(t, ok)
	require.Equal(t, plain, decorated,
		"a shared link carries where it came from and which block it opens; the document is the same one")
}

func TestParseDocURL_LeavesWhatIsNotADocument(t *testing.T) {
	for _, raw := range []string{
		"https://example.com/docx/AbC123",
		// Somebody else's domain that merely ends in the right letters. A
		// title drawn in place of this URL would hide where it actually goes.
		"https://evilfeishu.cn/docx/AbC123",
		"https://feishu.cn.example.com/docx/AbC123",
		"https://example.feishu.cn/client/chat/open",
		"https://example.feishu.cn/calendar/AbC123",
		"https://example.feishu.cn/j/1234567",
		// A form is shared under its own token, which names no document the
		// metadata API knows; asking would only earn a refusal.
		"https://example.feishu.cn/share/base/form/shrcnAbC123",
		// Minutes are not a document type the metadata API takes either.
		"https://example.feishu.cn/minutes/obcnAbC123",
		"https://example.feishu.cn/docx/",
	} {
		_, ok := ParseDocURL(raw)
		require.False(t, ok, raw)
	}
}

func TestFindDocRefs_ReadsEveryDocumentABodyLinksTo(t *testing.T) {
	refs := FindDocRefs("排期见 https://example.feishu.cn/docx/AbC123 ，数据在 https://example.feishu.cn/sheets/Xyz789 。")
	require.Equal(t, []DocRef{{"docx", "AbC123"}, {"sheet", "Xyz789"}}, refs,
		"the full stop after a URL belongs to the sentence")
}

func TestDocLinks_Lifecycle(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	named := DocRef{"docx", "AbC123"}
	wiki := DocRef{"wiki", "Xyz789"}
	shut := DocRef{"docx", "Nope456"}

	require.NoError(t, s.AddPendingDocLinks(ctx, []DocRef{named, wiki, shut}))
	require.NoError(t, s.AddPendingDocLinks(ctx, []DocRef{named}), "re-registering is a no-op")

	due, err := s.DocLinksDue(ctx, 100, 10)
	require.NoError(t, err)
	require.Len(t, due, 3)

	require.NoError(t, s.MarkDocTitle(ctx, named, "排期", "docx", 1000))
	require.NoError(t, s.MarkDocTitle(ctx, wiki, "组内约定", "sheet", 1000))
	require.NoError(t, s.MarkDocDenied(ctx, shut))

	due, _ = s.DocLinksDue(ctx, 100, 10)
	require.Empty(t, due, "a named document waits for its refresh, a refused one never comes back")
	due, _ = s.DocLinksDue(ctx, 1000, 10)
	require.Equal(t, []DocRef{named, wiki}, due, "a title is worth reading again once it may have been changed")

	labels, err := s.DocLabels(ctx)
	require.NoError(t, err)
	require.Equal(t, DocLabel{Title: "排期", Type: "docx"}, labels[named.Key()])
	require.Equal(t, DocLabel{Title: "组内约定", Type: "sheet"}, labels[wiki.Key()],
		"a wiki node is named by what it wraps, which is what picks its glyph")
	require.Equal(t, DocLabel{Type: "docx", Denied: true}, labels[shut.Key()],
		"a document out of reach is an answer the reader is shown, not an absence")
}

func TestDocLinks_ARedrawFollowsATitleLanding(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	ref := DocRef{"docx", "AbC123"}

	before, err := s.DataRev(ctx)
	require.NoError(t, err)
	require.NoError(t, s.AddPendingDocLinks(ctx, []DocRef{ref}))
	registered, _ := s.DataRev(ctx)
	require.Greater(t, registered, before, "a link becoming known is worth a look")

	require.NoError(t, s.MarkDocTitle(ctx, ref, "排期", "docx", 1000))
	named, _ := s.DataRev(ctx)
	require.Greater(t, named, registered, "the title has to reach the pane that is already drawn")

	require.NoError(t, s.MarkDocTitle(ctx, ref, "排期", "docx", 2000))
	require.Equal(t, named, mustRev(t, s), "moving the refresh clock changes nothing a reader can see")
}

func mustRev(t *testing.T, s *Store) int64 {
	t.Helper()
	rev, err := s.DataRev(context.Background())
	require.NoError(t, err)
	return rev
}
