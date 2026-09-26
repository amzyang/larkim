// Package applink is larkim's one lever on the Feishu desktop client: the
// lark:// URLs that navigate it, and the pace they may be fired at.
//
// Feishu has no mark-read call. Walking the client onto a chat is what makes
// it send the read receipt, so clearing a red dot means opening a URL
// (docs/read-sync/PRD.md). Both the TUI and the read-all command do that, and
// they share one desktop client, so they share the pace as well.
package applink

import (
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/amzyang/larkim/larkcli"
)

// Pace is the gap between two applinks. The client renders the chat it was
// walked onto before it sends a receipt, so firing faster than it draws loses
// the chats it was hurried through.
const Pace = 250 * time.Millisecond

// ChatLink addresses a chat, optionally at a message position. The lark://
// scheme reaches the desktop client directly; the https applink form would
// first open a browser tab that only redirects here.
func ChatLink(chatID string, position int64) string {
	url := "lark://applink.feishu.cn/client/chat/open?openChatId=" + chatID
	if position > 0 {
		url += "&position=" + strconv.FormatInt(position, 10)
	}
	return url
}

// MeetingLink joins a meeting by its number. The lark:// scheme works on the
// vc host too, so the client goes straight into the call rather than through
// a browser redirect.
func MeetingLink(meetNumber string) string {
	return "lark://vc.feishu.cn/j/" + meetNumber
}

// Open hands targets to macOS. A keypress asking for the Feishu client wants
// the screen; an applink fired to clear a badge must leave the reader in the
// terminal, which is what background buys.
//
// Several targets go in one invocation rather than one each, because that is
// what puts a message's pictures in a single viewer window with the rest in
// its sidebar — the way the client opens them — instead of scattering them
// over as many windows as the message had pictures. Applinks are the opposite
// case and go one per call: the client can only be in one chat, so a set of
// them arrives as a single navigation and only the last one is answered.
func Open(log *slog.Logger, targets []string, background bool) error {
	args := targets
	if background {
		args = append([]string{"-g"}, targets...)
	}
	// -g is decided here, not by the caller, so this is the only place the
	// argv exists whole. It is logged the way lark-cli's is: quoted, nothing
	// elided, paste-able back into a shell to see what macOS was asked.
	log.Debug("open", "argv", "open "+larkcli.ArgvLine(args))
	// open reports why it refused on stderr and nothing but a status to the
	// caller, so dropping stderr would leave every failure as "exit status 1".
	cmd := exec.Command("open", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("open: %w: %s", err, msg)
		}
		return fmt.Errorf("open: %w", err)
	}
	return nil
}
