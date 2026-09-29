package cli

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/resolve"
	"github.com/getsentry/sentry-go"
	"github.com/stretchr/testify/require"
)

func TestResolveSentryDSN_Precedence(t *testing.T) {
	cases := []struct {
		name                string
		flag                string
		flagChanged         bool
		dnt, env            string
		envSet              bool
		build               string
		wantDSN, wantSource string
	}{
		{"flag wins", "https://f@x/1", true, "1", "https://e@x/2", true, "https://b@x/3", "https://f@x/1", dsnSourceFlag},
		{"empty flag disables", "", true, "", "https://e@x/2", true, "https://b@x/3", "", dsnSourceFlag},
		{"do not track", "", false, "1", "https://e@x/2", true, "https://b@x/3", "", dsnSourceDoNotTrack},
		{"DO_NOT_TRACK=0 ignored", "", false, "0", "https://e@x/2", true, "", "https://e@x/2", dsnSourceEnv},
		{"empty env disables", "", false, "", "", true, "https://b@x/3", "", dsnSourceEnv},
		{"build default", "", false, "", "", false, " https://b@x/3 ", "https://b@x/3", dsnSourceBuild},
		{"none", "", false, "", "", false, "", "", dsnSourceNone},
	}
	for _, c := range cases {
		dsn, src := resolveSentryDSN(c.flag, c.flagChanged, c.dnt, c.env, c.envSet, c.build)
		require.Equal(t, c.wantDSN, dsn, c.name)
		require.Equal(t, c.wantSource, src, c.name)
	}
}

func TestReportable_SkipsExpectedFailures(t *testing.T) {
	require.False(t, reportable(nil))
	require.False(t, reportable(context.Canceled))
	require.False(t, reportable(&larkcli.Error{ExitCode: larkcli.ExitAuth}))
	require.False(t, reportable(&larkcli.Error{ExitCode: larkcli.ExitNetwork}))
	require.False(t, reportable(&larkcli.Error{ExitCode: larkcli.ExitAPI, Subtype: "rate_limit"}))
	require.False(t, reportable(&larkcli.Error{ExitCode: larkcli.ExitAPI, Code: 231203}))
	require.False(t, reportable(&resolve.AmbiguousError{Ref: "x"}))
	require.False(t, reportable(&usageError{errors.New("unknown flag")}))
	require.True(t, reportable(errors.New("sqlite: database disk image is malformed")))
	require.True(t, reportable(&larkcli.Error{ExitCode: 5, Type: "internal"}))
}

func TestDescribeLarkError_PutsTheVerdictInTagsAndTheCallInContext(t *testing.T) {
	scope := sentry.NewScope()
	describeLarkError(scope, &larkcli.Error{
		ExitCode: 1, Type: "api", Subtype: "permission_denied", Code: 99991672, APICode: 230002,
		Message: "bot is not in the chat", LogID: "log_abc",
		Argv:   []string{"im", "+messages-send", "--content", `{"text":"@张三 看一下"}`},
		Stderr: strings.Repeat("noise ", 200),
	})
	event := scope.ApplyToEvent(sentry.NewEvent(), nil, nil)

	require.Equal(t, map[string]string{
		"lark.exit": "1", "lark.type": "api", "lark.subtype": "permission_denied",
		"lark.code": "99991672", "lark.api_code": "230002", "lark.log_id": "log_abc",
	}, event.Tags)
	ctx := event.Contexts["lark-cli"]
	require.Equal(t, `im +messages-send --content '{"text":"@张三 看一下"}'`, ctx["argv"],
		"the message body rides along: it is what the failure is reproduced from")
	require.Equal(t, strings.Repeat("noise ", 200), ctx["stderr"], "nothing is clipped")
}

func TestDescribeInvocation_RecordsHowLarkimItselfWasCalled(t *testing.T) {
	scope := sentry.NewScope()
	describeInvocation(scope)
	event := scope.ApplyToEvent(sentry.NewEvent(), nil, nil)

	require.Equal(t, os.Args, event.Contexts["larkim"]["argv"])
	require.NotEmpty(t, event.Contexts["larkim"]["cwd"])
}
