// Package applink holds the lark:// URLs that address the desktop client and
// the one hand-over to macOS.
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

// openTimeout bounds one invocation of the launcher.
const openTimeout = 20 * time.Second

// ChatLink addresses a chat, optionally at a message position. The lark://
// scheme reaches the desktop client directly; the https applink form would
// first open a browser tab that only redirects here. messageId is not what
// the client navigates by — position is — but the links leave the client
// (yanked rows, tasks filed elsewhere) and their readers resolve the id back
// to a message, so it rides along whenever one is at hand.
func ChatLink(chatID, messageID string, position int64) string {
	url := "lark://applink.feishu.cn/client/chat/open?openChatId=" + chatID
	if position > 0 {
		url += "&position=" + strconv.FormatInt(position, 10)
	}
	if messageID != "" {
		url += "&messageId=" + messageID
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

// Open hands targets to macOS. The receiving app comes forward, because every
// caller is a keypress asking to see the thing.
//
// Several targets go in one invocation rather than one each, because that is
// what puts a message's pictures in a single viewer window with the rest in
// its sidebar — the way the client opens them — instead of scattering them
// over as many windows as the message had pictures.
func Open(log *slog.Logger, targets []string) error {
	// The argv exists whole only here. It carries at info because an open
	// moves the Feishu client under the reader's hands and a call that lands
	// leaves no other trace. The argv is quoted and nothing is elided, so the
	// line pastes back into a shell to see what macOS was asked.
	log.Info("open", "argv", "open "+larkcli.ArgvLine(targets))
	// open hands the URL to LaunchServices and returns, so a call still
	// running after this is one that will not return; the clear queue waits
	// on it, and a wait with no end would stop the sweep for good.
	ctx, cancel := context.WithTimeout(context.Background(), openTimeout)
	defer cancel()
	// open reports why it refused on stderr and nothing but a status to the
	// caller, so dropping stderr would leave every failure as "exit status 1".
	cmd := exec.CommandContext(ctx, "open", targets...)
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
