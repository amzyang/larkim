package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/resolve"
	"github.com/amzyang/larkim/store"
	"github.com/getsentry/sentry-go"
	"github.com/spf13/cobra"
)

// DSN decision sources, shown by the hidden `sentry` command.
const (
	dsnSourceFlag       = "flag"
	dsnSourceDoNotTrack = "do-not-track"
	dsnSourceEnv        = "env"
	dsnSourceBuild      = "build"
	dsnSourceNone       = "none"
)

// resolveSentryDSN picks the effective DSN; empty disables telemetry.
// Precedence: --sentry-dsn (explicit empty disables) > DO_NOT_TRACK > SENTRY_DSN
// env (explicit empty disables) > build-time default.
func resolveSentryDSN(flagValue string, flagChanged bool, doNotTrack, envValue string, envSet bool, buildDSN string) (dsn, source string) {
	if flagChanged {
		return strings.TrimSpace(flagValue), dsnSourceFlag
	}
	if dnt := strings.TrimSpace(doNotTrack); dnt != "" && dnt != "0" && dnt != "false" {
		return "", dsnSourceDoNotTrack
	}
	if envSet {
		return strings.TrimSpace(envValue), dsnSourceEnv
	}
	if dsn := strings.TrimSpace(buildDSN); dsn != "" {
		return dsn, dsnSourceBuild
	}
	return "", dsnSourceNone
}

// scrubSentryEvent keeps host name and user identity out of every event.
func scrubSentryEvent(event *sentry.Event, _ *sentry.EventHint) *sentry.Event {
	event.ServerName = ""
	event.User = sentry.User{}
	return event
}

func initSentry(dsn, release string) {
	if dsn == "" {
		return
	}
	err := sentry.Init(sentry.ClientOptions{
		Dsn:              dsn,
		Release:          release,
		AttachStacktrace: true,
		SendDefaultPII:   false,
		BeforeSend:       scrubSentryEvent,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: sentry init failed: %v\n", err)
	}
}

// sentryRecoverRepanic reports a panic and re-raises it so the crash output
// and exit code are preserved. A no-op when telemetry is off.
func sentryRecoverRepanic() {
	if r := recover(); r != nil {
		sentry.CurrentHub().Recover(r)
		sentry.Flush(2 * time.Second)
		panic(r)
	}
}

// reportable decides whether an error is a defect worth a Sentry event.
// Expected operational outcomes (bad input, missing login, network, rate
// limits, API rejections, cancellation) are not.
func reportable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, store.ErrNotFound) {
		return false
	}
	var le *larkcli.Error
	if errors.As(err, &le) {
		return !(le.IsAuth() || le.IsNetwork() || le.IsRateLimit() || le.IsPermanent())
	}
	var amb *resolve.AmbiguousError
	var usage *usageError
	return !errors.As(err, &amb) && !errors.As(err, &usage)
}

// captureError sends a reportable error and waits briefly for delivery.
func captureError(err error) {
	if !reportable(err) || sentry.CurrentHub().Client() == nil {
		return
	}
	sentry.CaptureException(err)
	sentry.Flush(2 * time.Second)
}

// usageError marks input the user can fix (flags, arguments); never reported.
type usageError struct{ err error }

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }

func (a *App) sentryCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "sentry",
		Short:  "Show telemetry status and send a test event",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			labels := map[string]string{
				dsnSourceFlag: "--sentry-dsn flag", dsnSourceDoNotTrack: "DO_NOT_TRACK (opt-out)",
				dsnSourceEnv: "SENTRY_DSN env", dsnSourceBuild: "build-time default", dsnSourceNone: "none",
			}
			inited := sentry.CurrentHub().Client() != nil
			state := "disabled"
			if a.sentryDSN != "" {
				state = "enabled"
				if !inited {
					state = "configured but SDK init failed (see warning above)"
				}
			}
			fmt.Fprintf(a.Out, "telemetry: %s\nsource:    %s\nrelease:   %s\n", state, labels[a.sentrySource], a.Version)
			if !inited {
				return nil
			}
			var id *sentry.EventID
			sentry.WithScope(func(scope *sentry.Scope) {
				scope.SetTag("larkim.e2e", "1")
				id = sentry.CaptureMessage("larkim sentry e2e test")
			})
			sentry.Flush(5 * time.Second)
			if id == nil {
				fmt.Fprintln(a.Out, "test event: dropped by SDK")
				return nil
			}
			fmt.Fprintf(a.Out, "test event: %s\n", *id)
			return nil
		},
	}
}
