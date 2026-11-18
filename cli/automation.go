package cli

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/amzyang/larkim/automation"
	"github.com/amzyang/larkim/store"
	"github.com/spf13/cobra"
)

func (a *App) automationCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "automation", Short: "Inspect message automation"}
	var limit int
	jobs := &cobra.Command{
		Use:   "jobs",
		Short: "List recent automation jobs and their outcomes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if limit <= 0 {
				return fmt.Errorf("limit must be positive")
			}
			st, err := a.openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			jobs, err := st.AutomationJobs(cmd.Context(), limit)
			if err != nil {
				return err
			}
			if a.json() {
				return a.printJSON(jobs)
			}
			rows := make([][]string, 0, len(jobs))
			for _, job := range jobs {
				rows = append(rows, []string{strconv.FormatInt(job.ID, 10), job.RuleID, job.MessageID, job.Status, fmtMs(job.CreatedMs)})
			}
			table(a.Out, []string{"job_id", "rule_id", "message_id", "status", "created"}, rows)
			return nil
		},
	}
	jobs.Flags().IntVar(&limit, "limit", 50, "maximum number of jobs (positive)")
	cmd.AddCommand(jobs, a.quotaCallbackCmd())
	completeNoFileDefault(cmd)
	return cmd
}

// startAutomation belongs to the daemon.lock owner. Even an empty rule set
// reconciles durable jobs left by a previous owner before polling begins.
func (a *App) startAutomation(ctx context.Context, st *store.Store) (func(), error) {
	engine := &automation.Engine{
		Store: st, Rules: a.cfg.Automation, Executor: automation.Exec{},
		Now: time.Now, Log: a.logger(), Recover: sentryRecoverRepanic,
	}
	if err := engine.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("initialize automation: %w", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() {
		defer sentryRecoverRepanic()
		engine.Run(ctx)
	})
	return func() {
		cancel()
		wg.Wait()
	}, nil
}
