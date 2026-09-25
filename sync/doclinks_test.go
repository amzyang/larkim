package sync

import (
	"context"
	"testing"
	"time"

	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

// docMessage stores a rendered body, which is where a link is read from: a
// body is registered as it is rendered, and the back-scan reads the same text.
func docMessage(t *testing.T, s *Syncer, id, content string) {
	t.Helper()
	ctx := context.Background()
	_, err := s.Store.UpsertMessages(ctx, []store.Message{{MessageID: id, ChatID: "oc_team",
		MsgType: "text", CreateMs: 10, RawJSON: "{}"}}, 1)
	require.NoError(t, err)
	// Only the rendering, not AddPendingDocLinks: the back-scan is what has
	// to find a link in a body that was stored before titles were read.
	require.NoError(t, s.Store.UpdateRendered(ctx, id, content, "", "", 1))
}

func TestResolveDocLinks_NamesWhatItCanAndSettlesWhatItCannot(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	f.Docs["docx/AbC123"] = larkcli.DocTitle{Type: "docx", Title: "排期"}
	f.Docs["wiki/Xyz789"] = larkcli.DocTitle{Type: "sheet", Title: "组内约定"}
	docMessage(t, s, "om_1", "排期 https://example.feishu.cn/docx/AbC123?from=from_copylink 见此")
	docMessage(t, s, "om_2", "https://example.feishu.cn/wiki/Xyz789 和 https://example.feishu.cn/docx/Nope456")

	n, err := s.resolveDocLinks(ctx, clk.t)
	require.NoError(t, err)
	require.Equal(t, 3, n)

	labels, err := s.Store.DocLabels(ctx)
	require.NoError(t, err)
	require.Equal(t, store.DocLabel{Title: "排期", Type: "docx"}, labels["docx/AbC123"])
	require.Equal(t, store.DocLabel{Title: "组内约定", Type: "sheet"}, labels["wiki/Xyz789"],
		"the server unwraps a wiki node, so the type it answers with is not the one it was asked for")
	require.Equal(t, store.DocLabel{Type: "docx", Denied: true}, labels["docx/Nope456"])
}

func TestResolveDocLinks_AsksOnceForADocumentSharedAround(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	f.Docs["docx/AbC123"] = larkcli.DocTitle{Type: "docx", Title: "排期"}
	for _, id := range []string{"om_1", "om_2", "om_3"} {
		docMessage(t, s, id, "https://example.feishu.cn/docx/AbC123?from=from_copylink")
	}

	n, err := s.resolveDocLinks(ctx, clk.t)
	require.NoError(t, err)
	require.Equal(t, 1, n, "one document, however many messages carry it")

	n, err = s.resolveDocLinks(ctx, clk.t)
	require.NoError(t, err)
	require.Zero(t, n, "a named document is left alone until its title is worth reading again")
	require.Equal(t, 1, callsTo(f, "doc-titles"), "the second pass found nothing to ask about")

	n, err = s.resolveDocLinks(ctx, clk.t.Add(docRefreshEvery))
	require.NoError(t, err)
	require.Equal(t, 1, n, "documents get renamed, so a title is re-read eventually")
}

func TestResolveDocLinks_NeverAsksAgainForADocumentOutOfReach(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	docMessage(t, s, "om_1", "https://example.feishu.cn/docx/Nope456")

	_, err := s.resolveDocLinks(ctx, clk.t)
	require.NoError(t, err)
	require.Equal(t, 1, callsTo(f, "doc-titles"))

	_, err = s.resolveDocLinks(ctx, clk.t.Add(365*24*time.Hour))
	require.NoError(t, err)
	require.Equal(t, 1, callsTo(f, "doc-titles"),
		"no permission, no such document and an unsupported type are all permanent")
}

func TestResolveDocLinks_ReadsLinksFromBodiesStoredBeforeTitlesWere(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	f.Docs["docx/AbC123"] = larkcli.DocTitle{Type: "docx", Title: "排期"}
	docMessage(t, s, "om_old", "https://example.feishu.cn/docx/AbC123")

	v, _, _ := s.Store.GetState(ctx, KeyDocScanID)
	require.Empty(t, v, "the scan has not run yet")

	_, err := s.resolveDocLinks(ctx, clk.t)
	require.NoError(t, err)
	labels, _ := s.Store.DocLabels(ctx)
	require.Equal(t, "排期", labels["docx/AbC123"].Title)

	v, _, _ = s.Store.GetState(ctx, KeyDocScanID)
	require.NotEmpty(t, v, "the cursor moves so later ticks cost an empty query")
}

func TestResolveDocLinks_WaitsBeforeAskingAgainAboutADocumentLeftUnanswered(t *testing.T) {
	s, f, clk := newSyncer(t)
	ctx := context.Background()
	f.DocsSilent["docx/AbC123"] = true
	docMessage(t, s, "om_1", "https://example.feishu.cn/docx/AbC123")

	n, err := s.resolveDocLinks(ctx, clk.t)
	require.NoError(t, err)
	require.Zero(t, n, "neither named nor refused is nothing to record")
	require.Equal(t, 1, callsTo(f, "doc-titles"))

	_, err = s.resolveDocLinks(ctx, clk.t.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, 1, callsTo(f, "doc-titles"),
		"a tick is three seconds; asking every one of them spends the drive quota on the same silence")

	f.Docs["docx/AbC123"] = larkcli.DocTitle{Type: "docx", Title: "排期"}
	delete(f.DocsSilent, "docx/AbC123")
	_, err = s.resolveDocLinks(ctx, clk.t.Add(docRetryEvery))
	require.NoError(t, err)
	labels, _ := s.Store.DocLabels(ctx)
	require.Equal(t, "排期", labels["docx/AbC123"].Title, "the document is still worth a title")
}
