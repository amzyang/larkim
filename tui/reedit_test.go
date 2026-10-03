package tui

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// answerReEdit arms the re-edit and answers it, returning the model the answer
// left behind and the command it fired.
func answerReEdit(t *testing.T, m Model, key string) (Model, func() any) {
	t.Helper()
	next, _ := m.askReEdit()
	m = next.(Model)
	answered, cmd, ok := m.answerConfirm(key)
	require.True(t, ok)
	if cmd == nil {
		return answered.(Model), nil
	}
	return answered.(Model), func() any { return cmd() }
}

// Feishu's edit API takes bot identity only, so the message has to go before
// its text can come back — which is what the y/n is for.
func TestAskReEdit_AsksBeforeTakingAnythingBack(t *testing.T) {
	t.Parallel()
	m, f := recallModel(t)

	next, cmd := m.askReEdit()
	m = next.(Model)

	assert.Equal(t, confirmReEdit, m.confirm.kind)
	assert.Equal(t, "om_mine", m.confirm.messageID)
	assert.Contains(t, m.notice, "recall and re-edit?")
	assert.Nil(t, cmd, "nothing goes out until it is answered")
	assert.Empty(t, f.Recalled)
}

func TestAskReEdit_RefusesSomebodyElsesMessage(t *testing.T) {
	t.Parallel()
	m, f := recallModel(t)
	m.msgIdx = 1 // 张三's

	next, _ := m.askReEdit()
	m = next.(Model)

	assert.Equal(t, confirmNone, m.confirm.kind)
	assert.Contains(t, m.notice, "only your own")
	assert.Nil(t, m.reEdit)
	assert.Empty(t, f.Recalled)
}

func TestAskReEdit_RefusesAnAlreadyRecalledMessage(t *testing.T) {
	t.Parallel()
	m, _ := recallModel(t)
	m.msgs[0].Deleted = true

	next, _ := m.askReEdit()

	assert.Equal(t, confirmNone, next.(Model).confirm.kind)
	assert.Contains(t, next.(Model).notice, "already recalled")
}

// A picture or an attachment has no source the composer could hold, so the
// action covers the two types Feishu's own edit covers and no more.
func TestAskReEdit_RefusesATypeTheComposerCannotCarry(t *testing.T) {
	t.Parallel()
	m, _ := recallModel(t)
	m.msgs[0].MsgType = "image"

	next, _ := m.askReEdit()

	assert.Equal(t, confirmNone, next.(Model).confirm.kind)
	assert.Contains(t, next.(Model).notice, "text and post")
}

// Until the rendering lands there is nothing to put back: the raw body carries
// @_user_1 placeholders rather than the names the composer takes.
func TestAskReEdit_RefusesAMessageWhoseRenderingHasNotLanded(t *testing.T) {
	t.Parallel()
	m, _ := recallModel(t)
	m.msgs[0].Content, m.msgs[0].RenderedAt = "", 0

	next, _ := m.askReEdit()

	assert.Equal(t, confirmNone, next.(Model).confirm.kind)
	assert.Contains(t, next.(Model).notice, "no text to take back")
}

func TestReEdit_RecallsThenFillsTheComposer(t *testing.T) {
	t.Parallel()
	m, f := recallModel(t)

	m, run := answerReEdit(t, m, "y")
	require.NotNil(t, run)
	next, _ := m.update(run())
	m = next.(Model)

	assert.Equal(t, []string{"om_mine"}, f.Recalled)
	assert.Equal(t, "发错了", m.input.Value(), "the text comes back to be typed over")
	assert.Equal(t, modeInsert, m.mode)
	assert.Nil(t, m.reEdit, "the payload is spent")
}

// A recall Feishu refused leaves the message standing, so the composer must be
// as the reader left it.
func TestReEdit_LeavesTheComposerAloneWhenTheRecallFailed(t *testing.T) {
	t.Parallel()
	m, f := recallModel(t)
	f.Err = errors.New("message is too old to recall")

	m, run := answerReEdit(t, m, "y")
	require.NotNil(t, run)
	next, _ := m.update(run())
	m = next.(Model)

	assert.Contains(t, m.notice, "recall:")
	assert.Empty(t, m.input.Value())
	assert.Equal(t, modeNormal, m.mode)
	assert.Nil(t, m.reEdit)
}

// The resend belongs where the message was, so the quote is reinstated.
func TestReEdit_ReinstatesTheQuote(t *testing.T) {
	t.Parallel()
	m, _ := recallModel(t)
	m.msgs[0].ReplyTo = "om_theirs"

	m, run := answerReEdit(t, m, "y")
	require.NotNil(t, run)
	next, _ := m.update(run())
	m = next.(Model)

	require.NotNil(t, m.replyTo)
	assert.Equal(t, "om_theirs", m.replyTo.MessageID)
}

func TestReEdit_DropsThePayloadOnCancel(t *testing.T) {
	t.Parallel()
	m, f := recallModel(t)

	m, run := answerReEdit(t, m, "n")

	assert.Nil(t, run, "nothing goes out")
	assert.Nil(t, m.reEdit)
	assert.Equal(t, confirmNone, m.confirm.kind)
	assert.Empty(t, f.Recalled)
}
