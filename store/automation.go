package store

import (
	"context"
	"database/sql"
	"errors"
)

// AutomationRule identifies the configured behavior whose activation is persisted.
type AutomationRule struct {
	ID          string
	Fingerprint string
	Enabled     bool
}

// AutomationRuleState keeps the activation boundary stable across restarts.
type AutomationRuleState struct {
	ID          string
	Fingerprint string
	Enabled     bool
	SinceMs     int64
}

// AutomationDirty is a coalesced message change awaiting evaluation by the owner.
type AutomationDirty struct {
	MessageID string
	Version   int64
}

// AutomationJob persists a callback snapshot and its execution outcome.
type AutomationJob struct {
	ID          int64  `json:"id"`
	RuleID      string `json:"rule_id"`
	MessageID   string `json:"message_id"`
	Fingerprint string `json:"fingerprint"`
	Payload     string `json:"payload"`
	Callback    string `json:"callback"`
	Status      string `json:"status"`
	Result      string `json:"result"`
	CreatedMs   int64  `json:"created_ms"`
	StartedMs   int64  `json:"started_ms"`
	FinishedMs  int64  `json:"finished_ms"`
}

// ReconcileAutomationRules preserves unchanged activations without replaying history.
// Only the process holding daemon.lock reconciles rules and evaluates dirty rows.
func (s *Store) ReconcileAutomationRules(ctx context.Context, rules []AutomationRule, now int64) ([]AutomationRuleState, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	ids := make([]string, 0, len(rules))
	for _, rule := range rules {
		_, err := tx.ExecContext(ctx, `INSERT INTO automation_rules(id, fingerprint, enabled, since_ms)
	 VALUES (?, ?, ?, CASE WHEN ? THEN ? ELSE 0 END)
	 ON CONFLICT(id) DO UPDATE SET
	   since_ms = CASE WHEN excluded.enabled = 1 AND
	     (automation_rules.enabled = 0 OR automation_rules.fingerprint <> excluded.fingerprint)
	     THEN excluded.since_ms ELSE automation_rules.since_ms END,
	   fingerprint = excluded.fingerprint, enabled = excluded.enabled`,
			rule.ID, rule.Fingerprint, rule.Enabled, rule.Enabled, now)
		if err != nil {
			return nil, err
		}
		ids = append(ids, rule.ID)
	}
	remove := `DELETE FROM automation_rules`
	if len(ids) > 0 {
		remove += ` WHERE id NOT IN ` + inClause(len(ids))
	}
	if _, err := tx.ExecContext(ctx, remove, anySlice(ids)...); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE automation_jobs SET status = 'cancelled', finished_ms = ?
	 WHERE status = 'pending' AND NOT EXISTS (
	   SELECT 1 FROM automation_rules r WHERE r.id = automation_jobs.rule_id
	     AND r.enabled = 1 AND r.fingerprint = automation_jobs.fingerprint)`, now); err != nil {
		return nil, err
	}
	states, err := queryAll(ctx, tx, func(sc scanner) (AutomationRuleState, error) {
		var state AutomationRuleState
		err := sc.Scan(&state.ID, &state.Fingerprint, &state.Enabled, &state.SinceMs)
		return state, err
	}, `SELECT id, fingerprint, enabled, since_ms FROM automation_rules ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return states, tx.Commit()
}

func (s *Store) AutomationDirty(ctx context.Context, limit int) ([]AutomationDirty, error) {
	return queryAll(ctx, s.db, func(sc scanner) (AutomationDirty, error) {
		var dirty AutomationDirty
		err := sc.Scan(&dirty.MessageID, &dirty.Version)
		return dirty, err
	}, `SELECT message_id, version FROM automation_dirty ORDER BY rowid LIMIT ?`, limit)
}

// CommitAutomationEvaluation drops stale evaluations without acknowledging newer work.
// The single evaluator never retains a version after acknowledging its row.
func (s *Store) CommitAutomationEvaluation(ctx context.Context, dirty AutomationDirty, jobs []AutomationJob) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// A write takes SQLite's writer lock before comparing the version; a deferred
	// transaction's initial SELECT would otherwise race a different process.
	res, err := tx.ExecContext(ctx, `UPDATE automation_dirty SET version = version WHERE message_id = ? AND version = ?`,
		dirty.MessageID, dirty.Version)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return err
	}
	for _, job := range jobs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO automation_jobs(rule_id, message_id, fingerprint, payload, callback, created_ms)
	 VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(rule_id, message_id) DO NOTHING`,
			job.RuleID, job.MessageID, job.Fingerprint, job.Payload, job.Callback, job.CreatedMs); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM automation_dirty WHERE message_id = ? AND version = ?`,
		dirty.MessageID, dirty.Version); err != nil {
		return err
	}
	return tx.Commit()
}

const automationJobColumns = `id, rule_id, message_id, fingerprint, payload, callback, status, result,
 created_ms, started_ms, finished_ms`

func scanAutomationJob(sc scanner) (AutomationJob, error) {
	var job AutomationJob
	err := sc.Scan(&job.ID, &job.RuleID, &job.MessageID, &job.Fingerprint, &job.Payload, &job.Callback,
		&job.Status, &job.Result, &job.CreatedMs, &job.StartedMs, &job.FinishedMs)
	return job, err
}

// NextAutomationJob does not claim work: the caller revalidates before starting it.
// Rendering waits must not block ready jobs; missing or recalled messages remain
// eligible so revalidation can cancel their jobs.
func (s *Store) NextAutomationJob(ctx context.Context) (AutomationJob, bool, error) {
	job, err := scanAutomationJob(s.db.QueryRowContext(ctx, `SELECT `+automationJobColumns+`
	 FROM automation_jobs WHERE status = 'pending' AND NOT EXISTS (
	   SELECT 1 FROM messages m WHERE m.message_id = automation_jobs.message_id
	     AND m.deleted = 0 AND m.rendered_at = 0)
	 ORDER BY id LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return AutomationJob{}, false, nil
	}
	return job, err == nil, err
}

func (s *Store) StartAutomationJob(ctx context.Context, id, now int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE automation_jobs SET status = 'running', started_ms = ?
	 WHERE id = ? AND status = 'pending'`, now, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n != 0, err
}

// FinishAutomationJob also lets revalidation cancel a callback that has not started.
func (s *Store) FinishAutomationJob(ctx context.Context, id int64, status, result string, now int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE automation_jobs SET status = ?, result = ?, finished_ms = ?
	 WHERE id = ? AND (status = 'running' OR (status = 'pending' AND ? = 'cancelled'))`,
		status, result, now, id, status)
	return err
}

// RecoverAutomationJobs never retries a callback whose external effects are unknown.
func (s *Store) RecoverAutomationJobs(ctx context.Context, now int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE automation_jobs SET status = 'unknown', finished_ms = ? WHERE status = 'running'`, now)
	return err
}

func (s *Store) AutomationJobs(ctx context.Context, limit int) ([]AutomationJob, error) {
	return queryAll(ctx, s.db, scanAutomationJob, `SELECT `+automationJobColumns+` FROM automation_jobs ORDER BY id DESC LIMIT ?`, limit)
}
