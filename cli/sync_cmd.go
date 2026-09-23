package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/sync"
	"github.com/spf13/cobra"
)

func (a *App) syncCmd() *cobra.Command {
	var backfillDays int
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Run one synchronization tick (discovery, chat refresh, backfill slice, rendering)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if backfillDays > 0 {
				a.cfg.BackfillDays = backfillDays
			}
			st, err := a.openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			lock, err := sync.TryLock(a.cfg.DataDir)
			if err != nil {
				return fmt.Errorf("%w (is the daemon running? use `larkim status`)", err)
			}
			defer lock.Unlock()
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			s := a.syncer(st)
			if _, err := s.EnsureIdentity(ctx); err != nil {
				return err
			}
			rep, err := s.Tick(ctx)
			s.SetStatus(ctx, err)
			if err != nil {
				return err
			}
			if a.json() {
				return a.printJSON(rep)
			}
			fmt.Fprintf(a.Out, "window %s → %s complete=%v\nhits=%d new=%d chats=%d slow_path=%d backfilled=%d rendered=%d\n",
				rep.Window.Start.Local().Format(time.RFC3339), rep.Window.End.Local().Format(time.RFC3339), rep.Complete,
				rep.Hits, rep.New, rep.Chats, rep.SlowPath, rep.Backfilled, rep.Rendered)
			return nil
		},
	}
	cmd.Flags().IntVar(&backfillDays, "backfill-days", 0, "override backfill depth for chats not yet backfilled")
	return cmd
}

func (a *App) daemonCmd() *cobra.Command {
	daemon := &cobra.Command{Use: "daemon", Short: "Background synchronization service"}
	run := &cobra.Command{
		Use:   "run",
		Short: "Run the sync loop in the foreground (what `brew services` starts)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := a.openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			lock, err := sync.TryLock(a.cfg.DataDir)
			if err != nil {
				return err
			}
			defer lock.Unlock()
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			s := a.syncer(st)
			s.Log.Info("larkim daemon starting", "version", a.Version, "data_dir", a.cfg.DataDir, "poll_interval", a.cfg.PollInterval)
			err = s.Run(ctx)
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		},
	}
	daemon.AddCommand(run)
	return daemon
}

type statusOut struct {
	Status          string           `json:"status"`
	LastError       string           `json:"last_error,omitempty"`
	Hint            string           `json:"hint,omitempty"`
	SelfOpenID      string           `json:"self_open_id,omitempty"`
	WatermarkMs     int64            `json:"watermark_ms"`
	LastTickMs      int64            `json:"last_tick_ms"`
	ChatsRefreshed  int64            `json:"chats_refreshed_ms"`
	DaemonLockHeld  bool             `json:"daemon_lock_held"`
	Counts          store.Counts     `json:"counts"`
	Unread          int64            `json:"unread"`
	Resources       map[string]int64 `json:"resources"`
	PendingBackfill int              `json:"pending_backfill"`
	Runs            []store.Run      `json:"recent_runs"`
}

func (a *App) statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show sync state from the database (works whether or not the daemon runs)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := a.openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := context.Background()
			var out statusOut
			out.Status, _, _ = st.GetState(ctx, sync.KeyStatus)
			if out.Status == "" {
				out.Status = "never_synced"
			}
			out.LastError, _, _ = st.GetState(ctx, sync.KeyLastError)
			out.SelfOpenID, _, _ = st.GetState(ctx, sync.KeySelfOpenID)
			out.WatermarkMs = stateInt(ctx, st, sync.KeyWatermark)
			out.LastTickMs = stateInt(ctx, st, sync.KeyLastTickAt)
			out.ChatsRefreshed = stateInt(ctx, st, sync.KeyChatsRefreshed)
			if out.Status == sync.StatusNeedsLogin {
				out.Hint = "run: lark-cli auth login"
			}
			if lock, err := sync.TryLock(a.cfg.DataDir); err == nil {
				lock.Unlock()
			} else if errors.Is(err, sync.ErrLocked) {
				out.DaemonLockHeld = true
			}
			out.Counts, _ = st.Counts(ctx)
			out.Unread, _ = st.UnreadCount(ctx)
			out.Resources, _ = st.ResourceCounts(ctx)
			pending, _ := st.ChatsNeedingBackfill(ctx, 100000)
			out.PendingBackfill = len(pending)
			out.Runs, _ = st.LastRuns(ctx, 5)
			if a.json() {
				return a.printJSON(out)
			}
			fmt.Fprintf(a.Out, "status:           %s\n", out.Status)
			if out.LastError != "" {
				fmt.Fprintf(a.Out, "last error:       %s\n", out.LastError)
			}
			if out.Hint != "" {
				fmt.Fprintf(a.Out, "hint:             %s\n", out.Hint)
			}
			fmt.Fprintf(a.Out, "daemon lock:      %v\nuser:             %s\nwatermark:        %s\nlast tick:        %s\nchats/messages:   %d / %d (rendered %d, unread %d)\nresources:        done %d, pending %d, failed %d, skipped %d\npending backfill: %d chats\n",
				out.DaemonLockHeld, out.SelfOpenID, fmtMs(out.WatermarkMs), fmtMs(out.LastTickMs), out.Counts.Chats, out.Counts.Messages, out.Counts.Rendered, out.Unread,
				out.Resources["done"], out.Resources["pending"], out.Resources["failed"], out.Resources["skipped"], out.PendingBackfill)
			if len(out.Runs) > 0 {
				rows := make([][]string, 0, len(out.Runs))
				for _, r := range out.Runs {
					rows = append(rows, []string{fmtMs(r.StartedAt), strconv.FormatInt(r.FinishedAt-r.StartedAt, 10) + "ms", strconv.FormatBool(r.OK), strconv.Itoa(r.Fetched), strconv.Itoa(r.Upserted), oneLine(r.Error, 60)})
				}
				fmt.Fprintln(a.Out, "recent runs:")
				table(a.Out, []string{"started", "took", "ok", "hits", "upserted", "error"}, rows)
			}
			return nil
		},
	}
}

func stateInt(ctx context.Context, st *store.Store, key string) int64 {
	v, _, _ := st.GetState(ctx, key)
	n, _ := strconv.ParseInt(v, 10, 64)
	return n
}
