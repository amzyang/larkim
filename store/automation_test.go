package store

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func openAutomationTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "automation.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	return s
}

func automationRule() AutomationRule {
	return AutomationRule{ID: "build", Fingerprint: "fp_build", Enabled: true}
}

func automationMessage(id string) Message {
	return Message{MessageID: id, ChatID: "oc_builds", SenderID: "ou_bot", SenderType: "app",
		MsgType: "text", ContentRaw: `{"text":"build ready"}`, RawJSON: `{}`, CreateMs: 110, UpdateMs: 110}
}

func automationInsert(t *testing.T, s *Store, id string) AutomationDirty {
	t.Helper()
	_, err := s.UpsertMessages(t.Context(), []Message{automationMessage(id)}, 120)
	require.NoError(t, err)
	dirty, err := s.AutomationDirty(t.Context(), 100)
	require.NoError(t, err)
	for _, d := range dirty {
		if d.MessageID == id {
			return d
		}
	}
	t.Fatalf("message %s was not queued", id)
	return AutomationDirty{}
}

func automationJob(id string) AutomationJob {
	return AutomationJob{RuleID: "build", MessageID: id, Fingerprint: "fp_build",
		Payload: `{"message_id":"` + id + `"}`, Callback: `{"argv":["/bin/true"]}`, CreatedMs: 130}
}

func TestAutomationDirty_CapturesCrossConnectionChangesAndIgnoresOverlap(t *testing.T) {
	s := openAutomationTest(t)
	ctx := t.Context()
	_, err := s.UpsertMessages(ctx, []Message{automationMessage("om_before")}, 100)
	require.NoError(t, err)
	_, err = s.ReconcileAutomationRules(ctx, []AutomationRule{automationRule()}, 100)
	require.NoError(t, err)
	dirty, err := s.AutomationDirty(ctx, 100)
	require.NoError(t, err)
	require.Empty(t, dirty, "activation must not replay stored history")

	var path string
	var seq int
	var name string
	require.NoError(t, s.db.QueryRowContext(ctx, `PRAGMA database_list`).Scan(&seq, &name, &path))
	writer, err := Open(path)
	require.NoError(t, err)
	defer writer.Close()
	d := automationInsert(t, writer, "om_build")
	require.Equal(t, int64(1), d.Version)
	_, err = writer.UpsertMessages(ctx, []Message{automationMessage("om_build")}, 140)
	require.NoError(t, err)
	require.NoError(t, writer.UpdateReactions(ctx, "om_build", `[]`))
	_, err = writer.db.ExecContext(ctx, `UPDATE messages SET silenced = 1 WHERE message_id = 'om_build'`)
	require.NoError(t, err)
	dirty, err = s.AutomationDirty(ctx, 100)
	require.NoError(t, err)
	require.Equal(t, []AutomationDirty{d}, dirty)

	require.NoError(t, writer.UpdateRendered(ctx, "om_build", "build ready", `[]`, 150))
	dirty, err = s.AutomationDirty(ctx, 100)
	require.NoError(t, err)
	require.Equal(t, []AutomationDirty{{MessageID: "om_build", Version: 2}}, dirty)
	require.NoError(t, writer.UpdateRendered(ctx, "om_build", "build ready", `[]`, 160))
	dirty, err = s.AutomationDirty(ctx, 100)
	require.NoError(t, err)
	require.Equal(t, int64(2), dirty[0].Version, "render timestamp alone is not a content change")

	m := automationMessage("om_build")
	m.Deleted = true
	_, err = writer.UpsertMessages(ctx, []Message{m}, 170)
	require.NoError(t, err)
	dirty, err = s.AutomationDirty(ctx, 100)
	require.NoError(t, err)
	require.Equal(t, []AutomationDirty{{MessageID: "om_build", Version: 3}}, dirty)

	_, err = s.ReconcileAutomationRules(ctx, nil, 180)
	require.NoError(t, err)
	_, err = writer.UpsertMessages(ctx, []Message{automationMessage("om_disabled")}, 190)
	require.NoError(t, err)
	dirty, err = s.AutomationDirty(ctx, 100)
	require.NoError(t, err)
	require.Len(t, dirty, 1, "disabled rules must not capture writes")
}

func TestAutomationDirty_MeaningfulUpdatesAreCoalesced(t *testing.T) {
	s := openAutomationTest(t)
	ctx := t.Context()
	_, err := s.ReconcileAutomationRules(ctx, []AutomationRule{automationRule()}, 100)
	require.NoError(t, err)
	d := automationInsert(t, s, "om_build")
	for _, change := range []string{
		`content = 'rendered'`, `content_raw = '{"text":"changed"}'`, `raw_json = '{"body":"changed"}'`,
		`rendered_at = 150`, `rendered_at = 0`, `create_ms = 111`, `update_ms = 112`,
		`chat_id = 'oc_other'`, `sender_id = 'ou_other'`, `sender_type = 'user'`, `msg_type = 'post'`, `deleted = 1`,
	} {
		t.Run(change, func(t *testing.T) {
			_, err := s.db.ExecContext(ctx, `UPDATE messages SET `+change+` WHERE message_id = 'om_build'`)
			require.NoError(t, err)
			d.Version++
			dirty, err := s.AutomationDirty(ctx, 100)
			require.NoError(t, err)
			require.Equal(t, []AutomationDirty{d}, dirty)
		})
	}
}

func TestCommitAutomationEvaluation_StaleVersionLeavesNewWorkUntouched(t *testing.T) {
	s := openAutomationTest(t)
	ctx := t.Context()
	_, err := s.ReconcileAutomationRules(ctx, []AutomationRule{automationRule()}, 100)
	require.NoError(t, err)
	d := automationInsert(t, s, "om_build")
	require.NoError(t, s.UpdateRendered(ctx, d.MessageID, "ready", `[]`, 120))
	require.NoError(t, s.CommitAutomationEvaluation(ctx, d, []AutomationJob{automationJob(d.MessageID)}))
	jobs, err := s.AutomationJobs(ctx, 100)
	require.NoError(t, err)
	require.Empty(t, jobs)
	dirty, err := s.AutomationDirty(ctx, 100)
	require.NoError(t, err)
	require.Equal(t, []AutomationDirty{{MessageID: d.MessageID, Version: d.Version + 1}}, dirty)
	require.NoError(t, s.CommitAutomationEvaluation(ctx, dirty[0], nil))
	dirty, err = s.AutomationDirty(ctx, 100)
	require.NoError(t, err)
	require.Empty(t, dirty)
	require.NoError(t, s.CommitAutomationEvaluation(ctx, d, []AutomationJob{automationJob(d.MessageID)}))
	jobs, err = s.AutomationJobs(ctx, 100)
	require.NoError(t, err)
	require.Empty(t, jobs, "an acknowledged row must not create jobs")
}

func TestCommitAutomationEvaluation_DeduplicatesAndRollsBackAllJobsOnFailure(t *testing.T) {
	s := openAutomationTest(t)
	ctx := t.Context()
	_, err := s.ReconcileAutomationRules(ctx, []AutomationRule{automationRule()}, 100)
	require.NoError(t, err)
	d := automationInsert(t, s, "om_build")
	_, err = s.db.ExecContext(ctx, `CREATE TRIGGER reject_job BEFORE INSERT ON automation_jobs
	 WHEN NEW.rule_id = 'reject' BEGIN SELECT RAISE(ABORT, 'rejected'); END`)
	require.NoError(t, err)
	bad := automationJob(d.MessageID)
	bad.RuleID = "reject"
	require.ErrorContains(t, s.CommitAutomationEvaluation(ctx, d, []AutomationJob{automationJob(d.MessageID), bad}), "rejected")
	jobs, err := s.AutomationJobs(ctx, 100)
	require.NoError(t, err)
	require.Empty(t, jobs)
	dirty, err := s.AutomationDirty(ctx, 100)
	require.NoError(t, err)
	require.Equal(t, []AutomationDirty{d}, dirty)

	require.NoError(t, s.CommitAutomationEvaluation(ctx, d, []AutomationJob{automationJob(d.MessageID)}))
	require.NoError(t, s.UpdateRendered(ctx, d.MessageID, "ready", `[]`, 140))
	dirty, err = s.AutomationDirty(ctx, 100)
	require.NoError(t, err)
	changed := automationJob(d.MessageID)
	changed.Fingerprint = "fp_changed"
	require.NoError(t, s.CommitAutomationEvaluation(ctx, dirty[0], []AutomationJob{changed}))
	jobs, err = s.AutomationJobs(ctx, 100)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Equal(t, "fp_build", jobs[0].Fingerprint)
	require.Equal(t, "pending", jobs[0].Status)
	require.Equal(t, automationJob(d.MessageID).Payload, jobs[0].Payload)
	require.Equal(t, automationJob(d.MessageID).Callback, jobs[0].Callback)
	dirty, err = s.AutomationDirty(ctx, 100)
	require.NoError(t, err)
	require.Empty(t, dirty)
}

func TestReconcileAutomationRules_PreservesActivationAndCancelsOnlyPending(t *testing.T) {
	for _, change := range []string{"disabled", "changed", "removed"} {
		t.Run(change, func(t *testing.T) {
			s := openAutomationTest(t)
			ctx := t.Context()
			rule := automationRule()
			states, err := s.ReconcileAutomationRules(ctx, []AutomationRule{rule}, 100)
			require.NoError(t, err)
			require.Equal(t, []AutomationRuleState{{ID: rule.ID, Fingerprint: rule.Fingerprint, Enabled: true, SinceMs: 100}}, states)
			for _, id := range []string{"om_pending", "om_running"} {
				d := automationInsert(t, s, id)
				require.NoError(t, s.CommitAutomationEvaluation(ctx, d, []AutomationJob{automationJob(id)}))
			}
			jobs, err := s.AutomationJobs(ctx, 100)
			require.NoError(t, err)
			started, err := s.StartAutomationJob(ctx, jobs[0].ID, 150)
			require.NoError(t, err)
			require.True(t, started)
			d := automationInsert(t, s, "om_unprocessed")
			states, err = s.ReconcileAutomationRules(ctx, []AutomationRule{rule}, 200)
			require.NoError(t, err)
			require.Equal(t, int64(100), states[0].SinceMs)
			dirty, err := s.AutomationDirty(ctx, 100)
			require.NoError(t, err)
			require.Equal(t, []AutomationDirty{d}, dirty)

			rules := []AutomationRule{rule}
			switch change {
			case "disabled":
				rules[0].Enabled = false
			case "changed":
				rules[0].Fingerprint = "fp_changed"
			case "removed":
				rules = nil
			}
			states, err = s.ReconcileAutomationRules(ctx, rules, 300)
			require.NoError(t, err)
			if change == "changed" {
				require.Equal(t, int64(300), states[0].SinceMs)
			}
			if change == "removed" {
				require.Empty(t, states)
			}
			jobs, err = s.AutomationJobs(ctx, 100)
			require.NoError(t, err)
			require.Equal(t, "running", jobs[0].Status)
			require.Equal(t, "cancelled", jobs[1].Status)
			require.Equal(t, int64(300), jobs[1].FinishedMs)
			started, err = s.StartAutomationJob(ctx, jobs[1].ID, 310)
			require.NoError(t, err)
			require.False(t, started)
			states, err = s.ReconcileAutomationRules(ctx, []AutomationRule{rule}, 400)
			require.NoError(t, err)
			require.Equal(t, int64(400), states[0].SinceMs)
		})
	}
}

func TestNextAutomationJob_SkipsRenderingWithoutBlockingRecallOrMissingMessages(t *testing.T) {
	s := openAutomationTest(t)
	ctx := t.Context()
	_, err := s.ReconcileAutomationRules(ctx, []AutomationRule{automationRule()}, 100)
	require.NoError(t, err)
	for _, id := range []string{"om_rendering", "om_ready", "om_recalled", "om_missing"} {
		d := automationInsert(t, s, id)
		require.NoError(t, s.CommitAutomationEvaluation(ctx, d, []AutomationJob{automationJob(id)}))
	}
	require.NoError(t, s.UpdateRendered(ctx, "om_ready", "ready", `[]`, 140))
	_, err = s.db.ExecContext(ctx, `UPDATE messages SET deleted = 1 WHERE message_id = 'om_recalled';
	 DELETE FROM messages WHERE message_id = 'om_missing'`)
	require.NoError(t, err)
	for _, id := range []string{"om_ready", "om_recalled", "om_missing"} {
		job, ok, err := s.NextAutomationJob(ctx)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, id, job.MessageID)
		require.NoError(t, s.FinishAutomationJob(ctx, job.ID, "cancelled", "revalidation", 150))
	}
	_, ok, err := s.NextAutomationJob(ctx)
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, s.UpdateRendered(ctx, "om_rendering", "ready", `[]`, 160))
	job, ok, err := s.NextAutomationJob(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "om_rendering", job.MessageID)
}

func TestAutomationJobs_ReadClaimFinishAndRecovery(t *testing.T) {
	s := openAutomationTest(t)
	ctx := t.Context()
	_, err := s.ReconcileAutomationRules(ctx, []AutomationRule{automationRule()}, 100)
	require.NoError(t, err)
	_, ok, err := s.NextAutomationJob(ctx)
	require.NoError(t, err)
	require.False(t, ok)
	for _, id := range []string{"om_first", "om_second", "om_third", "om_fourth"} {
		d := automationInsert(t, s, id)
		require.NoError(t, s.CommitAutomationEvaluation(ctx, d, []AutomationJob{automationJob(id)}))
	}
	_, err = s.db.ExecContext(ctx, `UPDATE messages SET rendered_at = 140`)
	require.NoError(t, err)
	first, ok, err := s.NextAutomationJob(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "om_first", first.MessageID)
	again, _, err := s.NextAutomationJob(ctx)
	require.NoError(t, err)
	require.Equal(t, first, again, "next is a read, not a claim")
	started, err := s.StartAutomationJob(ctx, first.ID, 200)
	require.NoError(t, err)
	require.True(t, started)
	started, err = s.StartAutomationJob(ctx, first.ID, 210)
	require.NoError(t, err)
	require.False(t, started)
	require.NoError(t, s.FinishAutomationJob(ctx, first.ID, "succeeded", "done", 220))
	second, _, err := s.NextAutomationJob(ctx)
	require.NoError(t, err)
	started, err = s.StartAutomationJob(ctx, second.ID, 230)
	require.NoError(t, err)
	require.True(t, started)
	third, _, err := s.NextAutomationJob(ctx)
	require.NoError(t, err)
	require.NoError(t, s.FinishAutomationJob(ctx, third.ID, "cancelled", "recalled", 240))
	require.NoError(t, s.RecoverAutomationJobs(ctx, 300))
	require.NoError(t, s.RecoverAutomationJobs(ctx, 310))
	jobs, err := s.AutomationJobs(ctx, 100)
	require.NoError(t, err)
	require.Len(t, jobs, 4)
	require.Equal(t, "pending", jobs[0].Status)
	require.Equal(t, "cancelled", jobs[1].Status)
	require.Equal(t, "recalled", jobs[1].Result)
	require.Equal(t, "unknown", jobs[2].Status)
	require.Equal(t, int64(300), jobs[2].FinishedMs)
	require.Equal(t, int64(230), jobs[2].StartedMs)
	require.Equal(t, "succeeded", jobs[3].Status)
	require.Equal(t, "done", jobs[3].Result)
	require.Equal(t, int64(220), jobs[3].FinishedMs)
	limited, err := s.AutomationJobs(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, jobs[:1], limited)
}
