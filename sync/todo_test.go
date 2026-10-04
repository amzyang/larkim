package sync

import (
	"errors"
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

// bodyFromChat is a todo body the AI Assistant chat's task cards carry: the
// title is empty and the summary lives in the content paragraphs.
const bodyFromChat = `{"task_id":"task_a","summary":{"title":"","content":[[{"tag":"text","text":"From chat with AI Assistant"}]]}}`

func TestRefreshTodos_RendersTheCompletionTheTaskListHolds(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	_, err := s.Store.UpsertMessages(ctx, []store.Message{
		{MessageID: "om_a", ChatID: "oc_team", MsgType: "todo", CreateMs: clk.Now().UnixMilli(),
			ContentRaw: bodyFromChat, RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)
	_, err = s.renderLocal(ctx, nil, 10, clk.Now())
	require.NoError(t, err)
	got, err := s.Store.GetMessage(ctx, "om_a")
	require.NoError(t, err)
	require.Equal(t, "☐ From chat with AI Assistant", got.Content, "the first render is the unchecked box")
	require.NoError(t, s.Store.UpdateReactions(ctx, "om_a", `{"counts":[{"reaction_type":"OK","count":"1"}]}`))

	f.Tasks = map[string]bool{"task_a": true}
	n, err := s.refreshTodos(ctx, clk.Now())
	require.NoError(t, err)
	require.Equal(t, 1, n)

	got, err = s.Store.GetMessage(ctx, "om_a")
	require.NoError(t, err)
	require.Equal(t, "☑ From chat with AI Assistant", got.Content)
	require.NotEmpty(t, got.ReactionsJSON, "the re-render keeps the reaction summary it held")

	n, err = s.refreshTodos(ctx, clk.Now())
	require.NoError(t, err)
	require.Zero(t, n, "a rendering that already says what the list says is left alone")
}

func TestRefreshTodos_LeavesATaskTheListDoesNotNameAlone(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	_, err := s.Store.UpsertMessages(ctx, []store.Message{
		{MessageID: "om_a", ChatID: "oc_team", MsgType: "todo", CreateMs: clk.Now().UnixMilli(),
			ContentRaw: bodyFromChat, RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)
	_, err = s.renderLocal(ctx, nil, 10, clk.Now())
	require.NoError(t, err)
	// A done task the next listing stops naming keeps the box it had: absent
	// means the task left the list, not that it reopened.
	f.Tasks = map[string]bool{"task_a": true}
	_, err = s.refreshTodos(ctx, clk.Now())
	require.NoError(t, err)
	f.Tasks = map[string]bool{}
	n, err := s.refreshTodos(ctx, clk.Now())
	require.NoError(t, err)
	require.Zero(t, n)
	got, err := s.Store.GetMessage(ctx, "om_a")
	require.NoError(t, err)
	require.Equal(t, "☑ From chat with AI Assistant", got.Content)
}

func TestRefreshTodos_SkipsTheListingWhenNoTodoMessageIsHeld(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	// Err fails every call; a pass that stays quiet is a pass that made none.
	f.Err = errors.New("boom")
	n, err := s.refreshTodos(ctx, clk.Now())
	require.NoError(t, err)
	require.Zero(t, n)
}

func TestParseTodo_ReadsTheGuidAndRefusesABodyItCannot(t *testing.T) {
	t.Parallel()
	guid, ok := ParseTodo(bodyFromChat)
	require.True(t, ok)
	require.Equal(t, "task_a", guid)
	_, ok = ParseTodo("not json")
	require.False(t, ok)
}

func TestTodoText_RendersTheUncheckedCard(t *testing.T) {
	t.Parallel()
	require.Equal(t, "☐ From chat with AI Assistant", TodoText(bodyFromChat))
}

func TestToggleTodo_CompletesTheTaskAndRendersItsMessages(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	_, err := s.Store.UpsertMessages(ctx, []store.Message{
		{MessageID: "om_a", ChatID: "oc_team", MsgType: "todo", CreateMs: clk.Now().UnixMilli(),
			ContentRaw: bodyFromChat, RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)
	_, err = s.renderLocal(ctx, nil, 10, clk.Now())
	require.NoError(t, err)

	require.NoError(t, s.ToggleTodo(ctx, "task_a", true))
	require.True(t, f.Tasks["task_a"])
	got, err := s.Store.GetMessage(ctx, "om_a")
	require.NoError(t, err)
	require.Equal(t, "☑ From chat with AI Assistant", got.Content)

	require.NoError(t, s.ToggleTodo(ctx, "task_a", false))
	require.False(t, f.Tasks["task_a"])
	got, err = s.Store.GetMessage(ctx, "om_a")
	require.NoError(t, err)
	require.Equal(t, "☐ From chat with AI Assistant", got.Content)
}

func TestToggleTodo_LeavesTheRenderingAloneWhenFeishuRefuses(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	_, err := s.Store.UpsertMessages(ctx, []store.Message{
		{MessageID: "om_a", ChatID: "oc_team", MsgType: "todo", CreateMs: clk.Now().UnixMilli(),
			ContentRaw: bodyFromChat, RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)
	_, err = s.renderLocal(ctx, nil, 10, clk.Now())
	require.NoError(t, err)
	f.Err = errors.New("refused")
	require.Error(t, s.ToggleTodo(ctx, "task_a", true))
	got, err := s.Store.GetMessage(ctx, "om_a")
	require.NoError(t, err)
	require.Equal(t, "☐ From chat with AI Assistant", got.Content)
}

func TestRenderLocal_KeepsTheBoxAReingestedRenderingCarries(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	seed := store.Message{MessageID: "om_a", ChatID: "oc_team", MsgType: "todo",
		CreateMs: clk.Now().UnixMilli(), UpdateMs: 1000, ContentRaw: bodyFromChat, RawJSON: "{}"}
	_, err := s.Store.UpsertMessages(ctx, []store.Message{seed}, 1)
	require.NoError(t, err)
	_, err = s.renderLocal(ctx, nil, 10, clk.Now())
	require.NoError(t, err)
	f.Tasks = map[string]bool{"task_a": true}
	require.NoError(t, s.ToggleTodo(ctx, "task_a", true))

	// Completing a task makes Feishu rewrite its message, and the sweep
	// re-ingests it with a newer update_ms — which re-queues it for a
	// rendering that must not lose the box the write left behind.
	again := seed
	again.UpdateMs = 2000
	_, err = s.Store.UpsertMessages(ctx, []store.Message{again}, 1)
	require.NoError(t, err)
	_, err = s.renderLocal(ctx, nil, 10, clk.Now())
	require.NoError(t, err)
	got, err := s.Store.GetMessage(ctx, "om_a")
	require.NoError(t, err)
	require.Equal(t, "☑ From chat with AI Assistant", got.Content,
		"a re-render inherits the completion the rendering it replaces carried")
}

func TestRefreshTodos_RecordsTheStateEvenWhenTheRenderingAlreadySaysIt(t *testing.T) {
	t.Parallel()
	s, f, clk := newSyncer(t)
	ctx := t.Context()
	// A rendering from before the state moved into todo_done: its text
	// already spells the completion, while the table has no row to read.
	_, err := s.Store.UpsertMessages(ctx, []store.Message{
		{MessageID: "om_a", ChatID: "oc_team", MsgType: "todo", CreateMs: clk.Now().UnixMilli(),
			ContentRaw: bodyFromChat, RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)
	require.NoError(t, s.Store.UpdateRendered(ctx, "om_a", "☑ From chat with AI Assistant", "", 1))

	f.Tasks = map[string]bool{"task_a": true}
	n, err := s.refreshTodos(ctx, clk.Now())
	require.NoError(t, err)
	require.Zero(t, n, "the rendering needed no rewrite")
	got, err := s.Store.GetMessage(ctx, "om_a")
	require.NoError(t, err)
	require.True(t, got.TodoDone, "the state is recorded anyway, so the icon and the next render read it")
}
