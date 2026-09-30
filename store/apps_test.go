package store

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const appReacted = `{"counts":[{"reaction_type":"Typing","count":"2"}],"details":[
  {"emoji_type":"Typing","action_time":"1","operator":{"operator_id":"cli_c","operator_type":"app"}},
  {"emoji_type":"Typing","action_time":"2","operator":{"operator_id":"ou_a","operator_type":"user"}}]}`

func TestUpdateReactions_EnrollsTheAppsThatReacted(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	_, err := s.UpsertMessages(ctx, []Message{msgAt("om_1", "oc_a", 100, 1, "hi")}, 1)
	require.NoError(t, err)

	require.NoError(t, s.UpdateReactions(ctx, "om_1", appReacted))

	ids, err := s.AppsToResolve(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, []string{"cli_c"}, ids, "a user reactor is a contact, not an app")
}

func TestUpdateRendered_EnrollsTheAppsThatReacted(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	_, err := s.UpsertMessages(ctx, []Message{msgAt("om_1", "oc_a", 100, 1, "hi")}, 1)
	require.NoError(t, err)

	require.NoError(t, s.UpdateRendered(ctx, "om_1", "hi", appReacted, 2))

	ids, err := s.AppsToResolve(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, []string{"cli_c"}, ids)
}

func TestSetAppName_SettlesTheAppForGood(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	_, err := s.UpsertMessages(ctx, []Message{msgAt("om_1", "oc_a", 100, 1, "hi")}, 1)
	require.NoError(t, err)
	require.NoError(t, s.UpdateReactions(ctx, "om_1", appReacted))

	require.NoError(t, s.SetAppName(ctx, "cli_c", "构建机器人", 5))
	require.NoError(t, s.UpdateReactions(ctx, "om_1", `{"counts":[]}`))
	require.NoError(t, s.UpdateReactions(ctx, "om_1", appReacted))

	ids, err := s.AppsToResolve(ctx, 10)
	require.NoError(t, err)
	require.Empty(t, ids, "reacting again does not reopen an app already looked up")
	names, err := s.AppNames(ctx, []string{"cli_c", "ou_a"})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"cli_c": "构建机器人"}, names)
}

func TestAppNames_ListsAnAppTheTenantWillNotShowUnnamed(t *testing.T) {
	s := openTest(t)
	ctx := t.Context()
	_, err := s.UpsertMessages(ctx, []Message{msgAt("om_1", "oc_a", 100, 1, "hi")}, 1)
	require.NoError(t, err)
	require.NoError(t, s.UpdateReactions(ctx, "om_1", appReacted))

	names, err := s.AppNames(ctx, []string{"cli_c"})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"cli_c": ""}, names, "an unnamed app is still known to be an app")
}

func TestMigrate_EnrollsTheAppsAlreadyOnStoredReactions(t *testing.T) {
	dir := t.TempDir()
	ctx := t.Context()
	s, err := Open(filepath.Join(dir, "t.db"))
	require.NoError(t, err)
	_, err = s.UpsertMessages(ctx, []Message{msgAt("om_1", "oc_a", 100, 1, "hi"), msgAt("om_2", "oc_a", 200, 2, "yo")}, 1)
	require.NoError(t, err)
	// Pretend the database predates the apps table: the blocks are stored,
	// nothing has enrolled them, and one of them is not JSON at all.
	_, err = s.db.ExecContext(ctx, `UPDATE messages SET reactions_json = CASE message_id WHEN 'om_1' THEN ? ELSE 'garbled' END`, appReacted)
	require.NoError(t, err)
	_, err = s.db.ExecContext(ctx, `DROP TABLE apps; DELETE FROM schema_migrations WHERE version = 42`)
	require.NoError(t, err)
	s.Close()

	s, err = Open(filepath.Join(dir, "t.db"))
	require.NoError(t, err)
	defer s.Close()
	ids, err := s.AppsToResolve(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, []string{"cli_c"}, ids)
}
