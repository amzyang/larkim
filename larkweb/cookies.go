package larkweb

import (
	"cmp"
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/browserutils/kooky"

	// Only the Chromium family is registered. Each browser package registers
	// its own finder, so leaving Safari out keeps a Full Disk Access prompt —
	// and a failure that has nothing to do with the browser being read — off
	// this path entirely.
	_ "github.com/browserutils/kooky/browser/brave"
	_ "github.com/browserutils/kooky/browser/chrome"
	_ "github.com/browserutils/kooky/browser/chromium"
	_ "github.com/browserutils/kooky/browser/edge"
)

// Browsers are the jars BrowserJar can read, named as kooky registers them:
// the list the imports above register, so config can refuse any other name
// rather than leave the reader with a missing-session error.
var Browsers = []string{"brave", "chrome", "chromium", "edge"}

// sessionCookie is the one cookie the gateway will not work without.
const sessionCookie = "session"

// cookieDomain bounds what is read out of the jar. Feishu's login state lives
// on the registrable domain, so this catches it along with the host-scoped
// cookies of the gateway itself.
const cookieDomain = "feishu.cn"

// CookieSource hands over the cookies a gateway call travels with.
type CookieSource interface {
	Cookies(ctx context.Context, host string) ([]*http.Cookie, error)
}

// BrowserJar reads a browser's own cookie jar. Nothing is copied out of it:
// the cookies are read per call and live only as long as the request.
type BrowserJar struct {
	// Browser names the jar, as kooky registers it, e.g. "chrome".
	Browser string
	Log     *slog.Logger
}

// Cookies reads the jar and keeps what would be sent to host.
//
// A store that cannot be read is skipped rather than returned: the finders
// answer for every profile of every registered browser, and one unreadable
// profile is not a reason to fail a call the named browser can serve. The
// absence that does matter — no session cookie — is reported as such, so a
// reader is told to log in rather than left to read a 401 off the gateway.
func (j BrowserJar) Cookies(ctx context.Context, host string) ([]*http.Cookie, error) {
	var out []*http.Cookie
	var haveSession bool
	// The browser is picked per store, before any cookie is read. Filtering
	// cookies afterwards would still decrypt every registered browser's jar,
	// and each Chromium-family browser has its own Keychain entry: a prompt
	// for Edge's key on a machine configured to read Chrome.
	for store, err := range kooky.TraverseCookieStores(ctx) {
		if err != nil {
			// Named at debug: a jar this call does not need is the common case.
			j.log().Debug("find cookie store", "err", err)
			continue
		}
		if store.Browser() != j.Browser {
			store.Close()
			continue
		}
		for c, err := range store.TraverseCookies(kooky.Valid, kooky.DomainHasSuffix(cookieDomain)) {
			if err != nil {
				j.log().Debug("read cookie store", "profile", store.Profile(), "err", err)
				continue
			}
			if !sentTo(c.Domain, host) {
				continue
			}
			if c.Name == sessionCookie {
				haveSession = true
			}
			out = append(out, &http.Cookie{Name: c.Name, Value: c.Value})
		}
		store.Close()
	}
	if !haveSession {
		return nil, &Error{
			Op:     "read cookies",
			Reason: "no Feishu web session in " + j.Browser + "; log in at feishu.cn",
		}
	}
	return out, nil
}

func (j BrowserJar) log() *slog.Logger {
	return cmp.Or(j.Log, discardLog)
}

// sentTo reports whether a jar cookie's domain would reach host. A leading dot
// makes it a domain cookie, good for the host and anything under it; without
// one it is host-scoped and only that host sees it.
func sentTo(domain, host string) bool {
	if d, ok := strings.CutPrefix(domain, "."); ok {
		return host == d || strings.HasSuffix(host, "."+d)
	}
	return domain != "" && domain == host
}
