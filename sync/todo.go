package sync

import (
	"context"
	"encoding/json"
	"time"
)

// tasksEvery paces the todo checkbox refresh. Completion is the one change
// that never lands in a message body — Feishu does not rewrite one when its
// task finishes — so nothing but this pass can move a box; one listing of
// the user's tasks answers every todo message at once, and a minute of
// staleness is nothing against how rarely a box moves.
const tasksEvery = time.Minute

// refreshTodos re-renders the todo messages with the completion their tasks
// now hold. A task the listing stops naming keeps the box its rendering
// already shows: leaving the list means the task was closed or let go, not
// that it reopened. With no todo message held the listing is skipped, so a
// store without todos costs this step nothing.
func (s *Syncer) refreshTodos(ctx context.Context, now time.Time) (int, error) {
	msgs, err := s.Store.TodoMessages(ctx)
	if err != nil {
		return 0, err
	}
	if len(msgs) == 0 {
		return 0, s.setStateTime(ctx, KeyTasksAt, now)
	}
	tasks, err := s.Client.ListTasks(ctx)
	if err != nil {
		return 0, err
	}
	done := make(map[string]bool, len(tasks))
	for _, t := range tasks {
		done[t.GUID] = t.Done
	}
	n, err := s.rerenderTodos(ctx, done, now)
	if err != nil {
		return n, err
	}
	return n, s.setStateTime(ctx, KeyTasksAt, now)
}

// ToggleTodo completes or reopens the task a todo message carries, then
// re-renders every message holding it: the checkbox is the whole display of
// the state, and no message moves to say it changed. done is the state the
// task is being put into, not the one it is leaving.
func (s *Syncer) ToggleTodo(ctx context.Context, guid string, done bool) error {
	var err error
	if done {
		err = s.Client.CompleteTask(ctx, guid)
	} else {
		err = s.Client.ReopenTask(ctx, guid)
	}
	if err != nil {
		return err
	}
	now := s.now()
	_, err = s.rerenderTodos(ctx, map[string]bool{guid: done}, now)
	return err
}

// rerenderTodos rewrites the checkbox of every todo message whose task the
// map names, keeping the reaction summary each rendering holds. A task the
// map leaves out keeps the box its rendering shows.
func (s *Syncer) rerenderTodos(ctx context.Context, done map[string]bool, now time.Time) (int, error) {
	msgs, err := s.Store.TodoMessages(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, m := range msgs {
		var b todoBody
		if json.Unmarshal([]byte(m.ContentRaw), &b) != nil || b.TaskID == "" {
			continue
		}
		d, ok := done[b.TaskID]
		if !ok {
			continue
		}
		// The state is recorded whether or not the text changes: a rendering
		// from before the state lived in todo_done already spells the
		// completion, and skipping the record on it would leave the icon —
		// which reads the field, not the text — stuck undone forever.
		if err := s.Store.SetTodoDone(ctx, m.MessageID, d); err != nil {
			return n, err
		}
		if text := todoText(m.ContentRaw, d, time.Local); text != m.Content {
			if err := s.Store.UpdateRendered(ctx, m.MessageID, text, m.ReactionsJSON, now.UnixMilli()); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}
