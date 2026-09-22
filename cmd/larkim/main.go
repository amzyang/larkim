// Command larkim syncs Feishu/Lark IM data to local SQLite and serves it to a CLI and TUI.
package main

import (
	"os"

	"github.com/amzyang/larkim/cli"
)

// version and sentryDSN are injected by goreleaser via -ldflags -X. A local
// build leaves sentryDSN empty, which disables telemetry.
var (
	version   = "dev"
	sentryDSN string
)

func main() {
	os.Exit(cli.Execute(version, sentryDSN))
}
