package triage

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/amzyang/larkim/applink"
	"github.com/amzyang/larkim/presence"
)

// Alerter shows banners through alerter (brew install vjeantet/tap/alerter):
// a notification-centre banner that does not take focus and reports what the
// reader did with it on stdout.
type Alerter struct {
	Path string
}

// Show raises b and returns what alerter printed: @CONTENTCLICKED for a click
// on the banner, an action's label, @CLOSED or @TIMEOUT otherwise.
func (a Alerter) Show(ctx context.Context, b Banner) (string, error) {
	out, err := exec.CommandContext(ctx, a.Path, alerterArgs(b)...).Output()
	if err != nil {
		return "", fmt.Errorf("alerter: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// alerterArgs spells b for alerter. A body clicked is the Open the client's
// own banner offers, so only a call gets a button: Join, which goes straight
// into it.
func alerterArgs(b Banner) []string {
	args := []string{"--title", noFlag(b.Title), "--message", noFlag(b.Body), "--close-label", "Dismiss",
		"--timeout", "60", "--ignore-dnd"}
	if b.JoinLink != "" {
		args = append(args, "--actions", ActionJoin)
	}
	if b.Icon != "" {
		args = append(args, "--app-icon", b.Icon)
	}
	return args
}

// noFlag keeps a value that starts with a dash from being read as a flag.
func noFlag(s string) string {
	if strings.HasPrefix(s, "-") {
		return " " + s
	}
	return s
}

// MacDesktop reads the machine through the tools that need no Automation
// permission, so it works from a daemon as well as from a TUI.
type MacDesktop struct {
	Log *slog.Logger
}

// LarkFrontmost reports whether a Feishu/Lark client is the frontmost app.
func (d MacDesktop) LarkFrontmost(ctx context.Context) bool {
	// info does not take "front" itself; the app's ASN is asked for first.
	asn, err := exec.CommandContext(ctx, "lsappinfo", "front").Output()
	if err != nil {
		return false
	}
	out, err := exec.CommandContext(ctx, "lsappinfo", "info", "-only", "bundleid", strings.TrimSpace(string(asn))).Output()
	if err != nil {
		return false
	}
	return isLarkBundle(parseBundleID(string(out)))
}

// parseBundleID reads the bundle id out of lsappinfo, which spells the key
// "CFBundleIdentifier" when asked for it alone and bundleID when it falls
// back to the whole record.
func parseBundleID(s string) string {
	for _, key := range []string{`"CFBundleIdentifier"="`, `bundleID="`} {
		if _, rest, ok := strings.Cut(s, key); ok {
			id, _, _ := strings.Cut(rest, `"`)
			return id
		}
	}
	return ""
}

// larkBundles are the bundle-id parts of the Feishu builds: the standard one,
// the international one, and the white-label builds, of which this machine's
// sagtjy516 is one (com.dancesuite.dance.ka.sagtjy516.mac).
var larkBundles = []string{"electron.lark", "larksuite", "dancesuite"}

func isLarkBundle(id string) bool {
	for _, b := range larkBundles {
		if strings.Contains(id, b) {
			return true
		}
	}
	return false
}

// Idle is how long since the last keyboard or mouse input, -1 when unknown.
func (d MacDesktop) Idle(ctx context.Context) time.Duration {
	out, err := exec.CommandContext(ctx, "ioreg", "-c", "IOHIDSystem").Output()
	if err != nil {
		return -1
	}
	return parseHIDIdle(string(out))
}

func parseHIDIdle(s string) time.Duration {
	_, rest, ok := strings.Cut(s, `"HIDIdleTime" = `)
	if !ok {
		return -1
	}
	f := strings.Fields(rest)
	if len(f) == 0 {
		return -1
	}
	ns, err := strconv.ParseInt(f[0], 10, 64)
	if err != nil {
		return -1
	}
	return time.Duration(ns)
}

// Open hands url to macOS.
func (d MacDesktop) Open(ctx context.Context, url string) error {
	return applink.Open(ctx, d.Log, []string{url})
}

// Sockets is the presence directory the TUIs answer in.
type Sockets struct {
	Dir string
}

// Peers lists the TUIs that answer.
func (s Sockets) Peers(ctx context.Context) ([]presence.State, error) {
	return presence.Find(ctx, s.Dir)
}

// Open asks p to open chat.
func (s Sockets) Open(ctx context.Context, p presence.State, chat string) error {
	return presence.Open(ctx, p, chat)
}

// Raise brings p's kitty window to the front.
func (s Sockets) Raise(ctx context.Context, p presence.State) error {
	return presence.Raise(ctx, p)
}
