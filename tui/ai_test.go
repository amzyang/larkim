package tui

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/amzyang/larkim/ai"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/sync"
	"github.com/stretchr/testify/require"
)

func aiModel(t *testing.T) Model {
	t.Helper()
	m := newRefreshModel(t)
	m.aiOpen, m.aiBusy, m.aiChan = true, true, make(chan ai.Chunk)
	return m
}

// Walking away from an answer must not leave the assistant marked busy: the
// guard in startAI would refuse every later :ai for the rest of the session.
func TestCloseAI_ClearsBusySoTheAssistantCanBeAskedAgain(t *testing.T) {
	m := aiModel(t)
	m = m.closeAI()
	require.False(t, m.aiOpen)
	require.False(t, m.aiBusy)
	require.Nil(t, m.aiChan)
}

// The wait is already in flight when the pane closes, so one more chunk
// arrives for a stream nobody reads. Re-arming on it would park a command on
// the channel the close just dropped.
func TestOnAIChunk_IgnoresAStreamTheReaderClosed(t *testing.T) {
	m := aiModel(t)
	gen := m.aiGen
	m = m.closeAI()
	got, cmd := m.onAIChunk(aiChunkMsg{gen: gen, chunk: ai.Chunk{Text: "late"}})
	require.Nil(t, cmd)
	require.Empty(t, got.(Model).aiText)
}

func TestOnAIChunk_FollowsTheStreamItIsWaitingOn(t *testing.T) {
	m := aiModel(t)
	got, cmd := m.onAIChunk(aiChunkMsg{gen: m.aiGen, chunk: ai.Chunk{Text: "hi"}})
	require.NotNil(t, cmd)
	require.Equal(t, "hi", got.(Model).aiText)
	require.True(t, got.(Model).aiBusy)
}

func TestOnAIChunk_ClearsBusyOnTheLastChunk(t *testing.T) {
	m := aiModel(t)
	got, _ := m.onAIChunk(aiChunkMsg{gen: m.aiGen, chunk: ai.Chunk{Done: true}})
	require.False(t, got.(Model).aiBusy)
}

// fakeAI stands in for the Anthropic client: it hands out a channel the test
// owns and reports whether the request was cancelled.
type fakeAI struct {
	ch        chan ai.Chunk
	cancelled chan struct{}
}

func newFakeAI() *fakeAI {
	return &fakeAI{ch: make(chan ai.Chunk, 4), cancelled: make(chan struct{})}
}

func (f *fakeAI) Stream(ctx context.Context, transcript, prompt string) <-chan ai.Chunk {
	go func() {
		<-ctx.Done()
		close(f.cancelled)
	}()
	return f.ch
}

func askingModel(t *testing.T, f *fakeAI) Model {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	m := New(Deps{Store: st, Syncer: &sync.Syncer{Store: st}, AI: f})
	m.chatID = "oc_quiet"
	m.msgs = []store.Message{{MessageID: "om_a", ChatID: "oc_quiet", SenderName: "张三", Content: "hi", CreateMs: 100}}
	return m
}

// Walking away mid-answer is the ordinary case: Esc is what closes the pane.
// It has to cancel the request, drop the chunk still in flight, and leave the
// assistant ready to be asked again.
func TestStartAI_SurvivesTheReaderClosingThePaneMidAnswer(t *testing.T) {
	f := newFakeAI()
	m := askingModel(t, f)

	started, cmd := m.startAI("summary")
	m = started.(Model)
	require.NotNil(t, cmd)
	require.True(t, m.aiBusy)
	gen := m.aiGen

	m = m.closeAI()
	select {
	case <-f.cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("closing the pane left the request running")
	}

	late, lateCmd := m.onAIChunk(aiChunkMsg{gen: gen, chunk: ai.Chunk{Text: "half an answer"}})
	m = late.(Model)
	require.Nil(t, lateCmd)

	again, cmd := m.startAI("summary")
	require.NotNil(t, cmd)
	require.True(t, again.(Model).aiBusy)
	require.NotEqual(t, "assistant is still answering", again.(Model).notice)
}
