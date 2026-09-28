// Package applink is larkim's one lever on the Feishu desktop client: the
// lark:// URLs that navigate it, and the pace they may be fired at.
//
// Feishu has no mark-read call. Walking the client onto a chat is what makes
// it send the read receipt, so clearing a red dot means opening a URL
// (docs/read-sync/PRD.md). Both the TUI and the read-all command do that, and
// they share one desktop client, so they share the pace as well.
package applink

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/amzyang/larkim/larkcli"
)

// DefaultPaceMS is the gap in milliseconds between two applinks. The client
// renders the chat it was walked onto before it sends a receipt, so firing
// faster than it draws loses the chats it was hurried through. A client that
// has been sitting in the background draws slower than the active app and
// nothing on macOS reports the difference without cgo, so the default has to
// cover the slow case; config's applink_pace_ms is how a reader narrows it.
const DefaultPaceMS = 1000

// DefaultPace is DefaultPaceMS as a duration, for the callers that have no
// configuration to read.
const DefaultPace = DefaultPaceMS * time.Millisecond

// openTimeout bounds one invocation of the launcher.
const openTimeout = 20 * time.Second

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

// EventLink addresses a calendar event's detail page, and is empty for a body
// naming no event: the desktop client needs both ids to find one.
//
// The client builds this URL from an event's calendarId, key, originalTime and
// startTime, while the API names an event by a single id that is key and
// originalTime joined with an underscore, so the id is split back apart here.
// startTime is seconds, the unit every calendar applink takes.
func EventLink(calendarID, eventID string, startMs int64) string {
	if calendarID == "" || eventID == "" {
		return ""
	}
	key, original, ok := strings.CutLast(eventID, "_")
	if !ok {
		// A non-recurring event is written with a 0 original time rather
		// than without one, so the client is given one either way.
		key, original = eventID, "0"
	}
	q := url.Values{
		"calendarId":   {calendarID},
		"key":          {key},
		"originalTime": {original},
		"startTime":    {strconv.FormatInt(startMs/1000, 10)},
	}
	return "lark://applink.feishu.cn/client/calendar/event/detail?" + q.Encode()
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
	// argv exists whole. It carries at info because an applink moves the
	// Feishu client under the reader's hands and a call that lands leaves no
	// other trace. The argv is quoted and nothing is elided, so the line
	// pastes back into a shell to see what macOS was asked.
	log.Info("open", "argv", "open "+larkcli.ArgvLine(args))
	// open hands the URL to LaunchServices and returns, so a call still
	// running after this is one that will not return; the applink queue waits
	// on it, and a wait with no end would stop the sweep for good.
	ctx, cancel := context.WithTimeout(context.Background(), openTimeout)
	defer cancel()
	// open reports why it refused on stderr and nothing but a status to the
	// caller, so dropping stderr would leave every failure as "exit status 1".
	cmd := exec.CommandContext(ctx, "open", args...)
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
