package automation

import (
	"context"
	"crypto/sha256"
	jsonv1 "encoding/json"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/amzyang/larkim/store"
)

type Engine struct {
	Store    *store.Store
	Rules    []Rule
	Executor Executor
	Now      func() time.Time
	Log      *slog.Logger
	Recover  func()
	states   map[string]store.AutomationRuleState
}

func (e *Engine) Initialize(ctx context.Context) error {
	defs := make([]store.AutomationRule, 0, len(e.Rules))
	for _, r := range e.Rules {
		b, err := json.Marshal(r, jsonv1.FormatDurationAsNano(true))
		if err != nil {
			return err
		}
		defs = append(defs, store.AutomationRule{ID: r.ID, Enabled: r.Enabled, Fingerprint: fmt.Sprintf("%x", sha256.Sum256(b))})
	}
	states, err := e.Store.ReconcileAutomationRules(ctx, defs, e.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("reconcile automation: %w", err)
	}
	e.states = make(map[string]store.AutomationRuleState, len(states))
	for _, s := range states {
		e.states[s.ID] = s
	}
	return e.Store.RecoverAutomationJobs(ctx, e.Now().UnixMilli())
}

func (e *Engine) Evaluate(ctx context.Context) error {
	dirty, err := e.Store.AutomationDirty(ctx, 100)
	if err != nil {
		return err
	}
	for _, d := range dirty {
		m, err := e.Store.GetMessage(ctx, d.MessageID)
		if err != nil {
			return err
		}
		var jobs []store.AutomationJob
		for _, r := range e.Rules {
			s := e.states[r.ID]
			if !s.Enabled || m.CreateMs < s.SinceMs {
				continue
			}
			text, match := r.Criteria.Match(m)
			if !match {
				continue
			}
			payload, err := json.Marshal(Event{Version: 1, RuleID: r.ID, Message: m, Text: text})
			if err != nil {
				return err
			}
			callback, err := json.Marshal(r.Callback, jsonv1.FormatDurationAsNano(true))
			if err != nil {
				return err
			}
			jobs = append(jobs, store.AutomationJob{RuleID: r.ID, MessageID: m.MessageID, Fingerprint: s.Fingerprint, Payload: string(payload), Callback: string(callback), CreatedMs: e.Now().UnixMilli()})
		}
		if err := e.Store.CommitAutomationEvaluation(ctx, d, jobs); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) Dispatch(ctx context.Context) error {
	job, ok, err := e.Store.NextAutomationJob(ctx)
	if err != nil || !ok {
		return err
	}
	m, err := e.Store.GetMessage(ctx, job.MessageID)
	if err != nil {
		return err
	}
	var matched bool
	var text string
	for _, r := range e.Rules {
		s := e.states[r.ID]
		if r.ID == job.RuleID && s.Enabled && s.Fingerprint == job.Fingerprint && m.CreateMs >= s.SinceMs {
			text, matched = r.Criteria.Match(m)
			break
		}
	}
	// A newer rendering may still be on its way. Its pending job stays owed.
	if m.RenderedAt == 0 && !m.Deleted {
		return nil
	}
	if !matched {
		return e.Store.FinishAutomationJob(ctx, job.ID, "cancelled", "message or rule no longer matches", e.Now().UnixMilli())
	}
	var cb Callback
	if err := json.Unmarshal([]byte(job.Callback), &cb, jsonv1.FormatDurationAsNano(true)); err != nil {
		return fmt.Errorf("decode callback %d: %w", job.ID, err)
	}
	started, err := e.Store.StartAutomationJob(ctx, job.ID, e.Now().UnixMilli())
	if err != nil || !started {
		return err
	}
	out := e.Executor.Execute(ctx, cb, Event{Version: 1, JobID: job.ID, RuleID: job.RuleID, Message: m, Text: text})
	// Cancellation must not erase the ambiguity of an action already submitted.
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return e.Store.FinishAutomationJob(finishCtx, job.ID, out.Status, out.Result, e.Now().UnixMilli())
}

func (e *Engine) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Go(func() { e.loop(ctx, "evaluate", e.Evaluate) })
	wg.Go(func() { e.loop(ctx, "dispatch", e.Dispatch) })
	wg.Wait()
}

func (e *Engine) loop(ctx context.Context, operation string, work func(context.Context) error) {
	if e.Recover != nil {
		defer e.Recover()
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if err := work(ctx); err != nil && !errors.Is(err, context.Canceled) {
			e.Log.ErrorContext(ctx, "automation", "operation", operation, "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
