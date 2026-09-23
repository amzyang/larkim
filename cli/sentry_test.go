package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/resolve"
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
