package cli

import "runtime/debug"

// devVersion is what main.version holds when goreleaser did not inject one.
const devVersion = "dev"

// resolveVersion names the running build. A release keeps the version
// goreleaser injected; a local build says which commit it was built from,
// because a bare "dev" cannot tell two local binaries apart. The module
// version Go derives from the tag is not used: off a tag it is a
// pseudo-version for a release that does not exist yet.
func resolveVersion(injected string, info *debug.BuildInfo) string {
	if injected != devVersion || info == nil {
		return injected
	}
	var rev, dirty string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value[:min(7, len(s.Value))]
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "+dirty"
			}
		}
	}
	if rev == "" {
		return injected
	}
	return injected + "-" + rev + dirty
}

// buildVersion is resolveVersion applied to this binary.
func buildVersion(injected string) string {
	info, _ := debug.ReadBuildInfo()
	return resolveVersion(injected, info)
}
