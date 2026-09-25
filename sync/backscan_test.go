package sync

import (
	"context"
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

// A card stored before ExtractResources could read json_attachment has no
// other way back into the queue: the ingest path does not re-run for a message
// already in the database, and repair reaches recent history alone. The
// back-scan is that way, so it has to select cards.
func TestRegisterExistingResources_ReadsAStoredCardsAttachment(t *testing.T) {
	s, _, clk := newSyncer(t)
	ctx := context.Background()
	now := clk.t.UnixMilli()

	_, err := s.Store.UpsertMessages(ctx, []store.Message{{
		MessageID: "om_card", ChatID: "oc_a", MsgType: "interactive", SenderID: "cli_c",
		ContentRaw: `{"json_attachment":{"images":{"1":{"origin_key":"img_card"}}}}`,
		CreateMs:   now, UpdateMs: now,
	}}, now)
	require.NoError(t, err)

	require.NoError(t, s.registerExistingResources(ctx))

	rs, err := s.Store.ResourcesFor(ctx, "om_card")
	require.NoError(t, err)
	require.Len(t, rs, 1)
	require.Equal(t, "img_card", rs[0].FileKey)
	require.Equal(t, "image", rs[0].Type)
}
