// Command larkim syncs Feishu/Lark IM data to local SQLite and serves it to a CLI and TUI.
package main

import (
	"os"

	"github.com/amzyang/larkim/cli"
)

// version is injected by goreleaser via -ldflags "-X main.version=…".
var version = "dev"

func main() {
	os.Exit(cli.Execute(version))
}
