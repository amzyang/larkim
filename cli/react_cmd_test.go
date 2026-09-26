package cli

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/emoji"
	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

func TestFormFor_TakesAnOrdinaryEmojiAsAReaction(t *testing.T) {
	require.Equal(t, formReaction, formFor("THUMBSUP"))
	require.Equal(t, formReaction, formFor("DONE"))
}

func TestFormFor_RepliesWithAnotherTenantsCultureEmojiRatherThanAPictureOfIt(t *testing.T) {
	// Feishu carries it inside a message fine, so the emoji itself reaches the
	// other side; a picture would only look like one.
	require.Equal(t, formEmotion, formFor("PursueUltimate"))
	require.Equal(t, formEmotion, formFor("CustomerSuccess"))
}

func TestFormFor_RepliesWithThePictureOfAWithdrawnEmoji(t *testing.T) {
	// Named in a message it arrives as "[Sensitive emoji]", so the picture is
	// the only thing left of it.
	require.Equal(t, formPicture, formFor("AWESOME"))
	require.Equal(t, formPicture, formFor("ATTENTION"))
}

func TestFormFor_LeavesAKeyThisBuildDoesNotKnowToFeishu(t *testing.T) {
	require.Equal(t, formReaction, formFor("EmojiShippedAfterThisBuild"))
}

func TestStayInThread_KeepsAReplyInsideTheThreadItAnswers(t *testing.T) {
	require.True(t, stayInThread(store.Message{ThreadID: "omt_a", MessagePosition: -3}),
		"a thread reply carries no position of its own")
	require.False(t, stayInThread(store.Message{ThreadID: "omt_a", MessagePosition: 12}),
		"a thread root stands in the chat's main flow")
	require.False(t, stayInThread(store.Message{MessagePosition: 12}))
}

func TestPrintReacted_SaysWhichFormReachedTheOtherSide(t *testing.T) {
	say := func(res reactResult) string {
		var out bytes.Buffer
		a := &App{Out: &out, Err: &out}
		require.NoError(t, a.printReacted(res))
		return out.String()
	}
	require.Equal(t, "reacted to om_elsewhere with Like\n",
		say(reactResult{MessageID: "om_elsewhere", Emoji: "THUMBSUP", Form: formReaction}))
	require.Equal(t, "om_elsewhere takes no AimForTheHighest reaction; replied with it instead (om_sent_1)\n",
		say(reactResult{MessageID: "om_elsewhere", Emoji: "PursueUltimate", Form: formEmotion, ReplyID: "om_sent_1"}))
	require.Equal(t, "om_elsewhere takes no 666 reaction; replied with its picture instead (om_sent_1)\n",
		say(reactResult{MessageID: "om_elsewhere", Emoji: "AWESOME", Form: formPicture, ReplyID: "om_sent_1"}))
}

func TestReactName_FallsBackToTheKeyOfAnEmojiThisBuildDoesNotKnow(t *testing.T) {
	require.Equal(t, "Like", reactName("THUMBSUP"))
	require.Equal(t, "EmojiShippedAfterThisBuild", reactName("EmojiShippedAfterThisBuild"))
}

// reactApp is an App whose Feishu boundary is a fake and whose store is a real
// SQLite file, so a test asserts on what reached Feishu and what was written.
func reactApp(t *testing.T) (*App, *larkcli.Fake, *store.Store) {
	t.Helper()
	f := larkcli.NewFake()
	dir := t.TempDir()
	a := &App{Out: &bytes.Buffer{}, Err: &bytes.Buffer{}, cfg: config.Config{DataDir: dir}, larkClient: f}
	st, err := store.Open(filepath.Join(dir, "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	return a, f, st
}

func TestReact_PutsAnOrdinaryEmojiOnTheMessageItself(t *testing.T) {
	a, f, st := reactApp(t)

	res, err := a.react(context.Background(), st, "om_elsewhere", "THUMBSUP")
	require.NoError(t, err)
	require.Equal(t, formReaction, res.Form)
	require.Empty(t, res.ReplyID, "a reaction is not a message")
	require.Equal(t, "THUMBSUP", f.Reacted["om_elsewhere"][0].EmojiType)
	require.Empty(t, f.Sent, "nothing is said in the chat")
}

func TestReact_RepliesWithAnotherTenantsCultureEmojiAsTheEmojiItself(t *testing.T) {
	a, f, st := reactApp(t)
	f.Messages["om_elsewhere"] = larkcli.RawMessage{MessageID: "om_elsewhere", ChatID: "oc_quiet"}

	res, err := a.react(context.Background(), st, "om_elsewhere", "PursueUltimate")
	require.NoError(t, err)
	require.Equal(t, formEmotion, res.Form)
	require.Equal(t, "om_sent_1", res.ReplyID)
	require.Empty(t, f.Reacted["om_elsewhere"], "Feishu would refuse it as a reaction")
	require.Equal(t, `{"zh_cn":{"content":[[{"tag":"emotion","emoji_type":"PursueUltimate"}]]}}`, f.Sent[0].Post)
	require.Empty(t, f.Uploads, "the emoji travels as itself, not as a picture of itself")
}

func TestReact_RepliesWithThePictureOfAWithdrawnEmoji(t *testing.T) {
	a, f, st := reactApp(t)
	f.Messages["om_elsewhere"] = larkcli.RawMessage{MessageID: "om_elsewhere", ChatID: "oc_quiet"}

	res, err := a.react(context.Background(), st, "om_elsewhere", "AWESOME")
	require.NoError(t, err)
	require.Equal(t, formPicture, res.Form)
	require.Equal(t, "img_fake_1", f.Sent[0].ImageKey)
	require.Equal(t, []string{emoji.Path(a.cfg.DataDir, "AWESOME")}, f.Uploads)
	require.FileExists(t, f.Uploads[0], "the picture is cut out of the sheet before it is uploaded")
}

func TestReact_KeepsAReplyToAThreadReplyInsideThatThread(t *testing.T) {
	a, f, st := reactApp(t)
	ctx := context.Background()
	f.Messages["om_in_thread"] = larkcli.RawMessage{MessageID: "om_in_thread", ChatID: "oc_quiet"}
	_, err := st.UpsertMessages(ctx, []store.Message{
		{MessageID: "om_in_thread", ChatID: "oc_quiet", MsgType: "text", ThreadID: "omt_a",
			MessagePosition: -3, CreateMs: 10, RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)

	_, err = a.react(ctx, st, "om_in_thread", "PursueUltimate")
	require.NoError(t, err)
	require.Equal(t, "omt_om_in_thread", f.Messages["om_sent_1"].ThreadID,
		"answering a thread reply in the main flow would drag the exchange out of its thread")
}

func TestReact_RepliesInTheMainFlowForAMessageTheStoreHasNeverSeen(t *testing.T) {
	a, f, st := reactApp(t)
	f.Messages["om_elsewhere"] = larkcli.RawMessage{MessageID: "om_elsewhere", ChatID: "oc_quiet"}

	_, err := a.react(context.Background(), st, "om_elsewhere", "PursueUltimate")
	require.NoError(t, err, "a message this store never synced is still answerable by id")
	require.Empty(t, f.Messages["om_sent_1"].ThreadID)
}

func TestReact_ReportsWhatFeishuRefused(t *testing.T) {
	a, f, st := reactApp(t)
	f.Err = errors.New("231001 invalid emoji_type")

	_, err := a.react(context.Background(), st, "om_elsewhere", "THUMBSUP")
	require.ErrorContains(t, err, "231001", "Feishu keeps the emoji list, so its refusal is the answer")
}
