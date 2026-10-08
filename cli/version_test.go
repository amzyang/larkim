package cli

import (
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/require"
)

func buildInfo(settings ...debug.BuildSetting) *debug.BuildInfo {
	return &debug.BuildInfo{Settings: settings}
}

func TestResolveVersion_InjectedReleaseWins(t *testing.T) {
	info := buildInfo(debug.BuildSetting{Key: "vcs.revision", Value: "4981d0d16503b4e650b18abb9d33e6f0d9a81730"})
	require.Equal(t, "0.21.7", resolveVersion("0.21.7", info))
}

func TestResolveVersion_LocalBuildNamesItsCommit(t *testing.T) {
	info := buildInfo(
		debug.BuildSetting{Key: "vcs.revision", Value: "4981d0d16503b4e650b18abb9d33e6f0d9a81730"},
		debug.BuildSetting{Key: "vcs.modified", Value: "false"},
	)
	require.Equal(t, "dev-4981d0d", resolveVersion("dev", info))
}

func TestResolveVersion_LocalBuildMarksUncommittedChanges(t *testing.T) {
	info := buildInfo(
		debug.BuildSetting{Key: "vcs.revision", Value: "4981d0d16503b4e650b18abb9d33e6f0d9a81730"},
		debug.BuildSetting{Key: "vcs.modified", Value: "true"},
	)
	require.Equal(t, "dev-4981d0d+dirty", resolveVersion("dev", info))
}

func TestResolveVersion_StaysDevWithoutVCSInfo(t *testing.T) {
	require.Equal(t, "dev", resolveVersion("dev", buildInfo()))
	require.Equal(t, "dev", resolveVersion("dev", nil))
}
