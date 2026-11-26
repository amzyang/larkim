package cli

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"log/slog"
	"os"
	"time"

	"golang.org/x/term"

	"github.com/amzyang/larkim/applink"
	"github.com/amzyang/larkim/automation"
	"github.com/amzyang/larkim/cuacallback"
	"github.com/spf13/cobra"
)

func (a *App) quotaCallbackCmd() *cobra.Command {
	var chatID, senderID string
	var check bool
	cmd := &cobra.Command{
		Use: "claim-quota", Short: "Run the scoped Lingxi quota-card callback from a JSON stdin event", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if f, ok := a.In.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
				return fmt.Errorf("claim-quota requires a JSON event on stdin; use it through automation or pipe an event file")
			}
			started := time.Now()
			var event automation.Event
			out := automation.Outcome{Status: "failed"}
			defer func() {
				a.logger().InfoContext(cmd.Context(), "quota callback done", "job_id", event.JobID, "message_id", event.Message.MessageID, "check", check, "status", out.Status, "dur_ms", time.Since(started).Milliseconds())
			}()
			if err := json.UnmarshalRead(a.In, &event); err != nil {
				out.Result = fmt.Sprintf("invalid callback event: %v", err)
			} else if q, err := cuacallback.ParseQuota(event, chatID, senderID); err != nil {
				out.Result = err.Error()
			} else {
				out = a.claimQuota(cmd.Context(), q, check, fmt.Sprintf("larkim-quota-%d", event.JobID), a.logger().With("job_id", event.JobID, "message_id", event.Message.MessageID))
			}
			if err := json.MarshalWrite(a.Out, out); err != nil {
				return err
			}
			_, err := fmt.Fprintln(a.Out)
			return err
		},
	}
	cmd.Flags().StringVar(&chatID, "chat-id", "", "authorized quota bot conversation")
	cmd.Flags().StringVar(&senderID, "sender-id", "", "authorized quota bot sender")
	cmd.Flags().BoolVar(&check, "check", false, "find a claimable button without pressing it (foregrounds Lingxi)")
	mustWire(cmd.MarkFlagRequired("chat-id"))
	mustWire(cmd.MarkFlagRequired("sender-id"))
	completeNoFileDefault(cmd)
	return cmd
}

func (a *App) claimQuota(ctx context.Context, q cuacallback.Quota, check bool, session string, log *slog.Logger) automation.Outcome {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	transport := cuacallback.TimedTransport{Transport: cuacallback.CLI{Executable: "cua-driver"}, Log: log, Now: time.Now}
	driver := &cuacallback.CUA{Transport: transport, Session: session, OpenURL: func(ctx context.Context, u string) error {
		return applink.Open(ctx, a.logger(), []string{u})
	}}
	return (&cuacallback.Claim{Driver: driver}).Run(ctx, q, check)
}
