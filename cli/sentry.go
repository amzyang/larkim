package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
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

func initSentry(dsn, release string) {
	if dsn == "" {
		return
	}
	err := sentry.Init(sentry.ClientOptions{
		Dsn:              dsn,
		Release:          release,
		AttachStacktrace: true,
		// The only reader of these events is the person the data belongs to,
		// so host name, user and request context are all kept: they are what
		// a report is diagnosed from.
		SendDefaultPII: true,
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
	if le, ok := errors.AsType[*larkcli.Error](err); ok {
		return !(le.IsAuth() || le.IsNetwork() || le.IsRateLimit() || le.IsPermanent())
	}
	_, ambiguous := errors.AsType[*resolve.AmbiguousError](err)
	_, misused := errors.AsType[*usageError](err)
	return !ambiguous && !misused
}

// captureError sends a reportable error and waits briefly for delivery, with
// everything that could shorten a diagnosis attached. Nothing is redacted or
// clipped: see CLAUDE.md's Telemetry section.
func captureError(err error) {
	if !reportable(err) || sentry.CurrentHub().Client() == nil {
		return
	}
	sentry.WithScope(func(scope *sentry.Scope) {
		describeInvocation(scope)
		if le, ok := errors.AsType[*larkcli.Error](err); ok {
			describeLarkError(scope, le)
		}
		sentry.CaptureException(err)
	})
	sentry.Flush(2 * time.Second)
}

// describeInvocation records how larkim itself was called, which is the half
// of a repro that the error text never carries.
func describeInvocation(scope *sentry.Scope) {
	wd, _ := os.Getwd()
	scope.SetContext("larkim", sentry.Context{"argv": os.Args, "cwd": wd})
	if len(os.Args) > 1 {
		scope.SetTag("larkim.cmd", os.Args[1])
	}
}

// describeLarkError spreads a lark-cli refusal across the scope: the verdict
// as tags so events group and filter by it, the call and its whole output as
// a context so the failure can be replayed by hand.
func describeLarkError(scope *sentry.Scope, e *larkcli.Error) {
	scope.SetTag("lark.exit", strconv.Itoa(e.ExitCode))
	for name, value := range map[string]string{
		"lark.type": e.Type, "lark.subtype": e.Subtype, "lark.log_id": e.LogID,
	} {
		if value != "" {
			scope.SetTag(name, value)
		}
	}
	for name, value := range map[string]int{"lark.code": e.Code, "lark.api_code": e.APICode} {
		if value != 0 {
			scope.SetTag(name, strconv.Itoa(value))
		}
	}
	scope.SetContext("lark-cli", sentry.Context{
		"argv": larkcli.ArgvLine(e.Argv), "stderr": e.Stderr, "message": e.Message,
		"hint": e.Hint, "api_message": e.APIMessage, "retry_after": e.RetryAfter.String(),
	})
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
