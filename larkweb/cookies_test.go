package larkweb

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"testing"

	"github.com/browserutils/kooky"
	"github.com/stretchr/testify/require"
)

// The jar holds cookies for every Feishu host the browser has visited, and a
// tenant's document cookies are not the gateway's to receive.
func TestSentTo_KeepsTheCookiesTheGatewayWouldGet(t *testing.T) {
	for _, tc := range []struct {
		name   string
		domain string
		want   bool
	}{
		{"domain cookie covers a subdomain", ".feishu.cn", true},
		{"domain cookie covers the bare domain", ".internal-api-lark-api.feishu.cn", true},
		{"host cookie for this exact host", gatewayHost, true},
		{"host cookie for a tenant's own host", "gaotu.feishu.cn", false},
		{"domain cookie for a different subtree", ".accounts.feishu.cn", false},
		{"unrelated domain", ".example.com", false},
		{"no domain at all", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, sentTo(tc.domain, gatewayHost))
		})
	}
}

func cookieSeq(rows ...any) kooky.CookieSeq {
	return func(yield func(*kooky.Cookie, error) bool) {
		for _, r := range rows {
			var ok bool
			switch r := r.(type) {
			case error:
				ok = yield(nil, r)
			case *kooky.Cookie:
				ok = yield(r, nil)
			}
			if !ok {
				return
			}
		}
	}
}

func jarCookie(name string) *kooky.Cookie {
	return &kooky.Cookie{Cookie: http.Cookie{Name: name, Value: "v", Domain: ".feishu.cn"}}
}

func newJarRead() *jarRead {
	return &jarRead{browser: "chrome", host: gatewayHost, log: discardLog}
}

// Chrome profiles list a Network/Cookies path that older layouts never
// created; its absence is not a failure of the store that does exist.
var errMissingStore = &fs.PathError{Op: "open", Path: "/Users/linlan/Chrome/Default/Network/Cookies", Err: fs.ErrNotExist}

// A Keychain that will not hand over Chrome's key fails every row's
// decryption, session included. That is not a logged-out browser, and the
// error has to say what did fail.
func TestJarRead_ReportsWhyTheSessionCouldNotBeRead(t *testing.T) {
	keyErr := errors.New("exit status 51")
	r := newJarRead()
	r.add("Default", cookieSeq(errMissingStore))
	r.add("Default", cookieSeq(
		fmt.Errorf("decrypting cookie session: keyring password retrieval failed: %w", keyErr),
		fmt.Errorf("decrypting cookie lang: keyring password retrieval failed: %w", keyErr),
	))

	_, err := r.result(t.Context())

	require.ErrorIs(t, err, keyErr)
	require.ErrorContains(t, err, "Default")
	require.NotContains(t, err.Error(), "log in")
	require.NotContains(t, err.Error(), "no such file")
}

func TestJarRead_SendsToLogInWhenNoStoreHoldsASession(t *testing.T) {
	r := newJarRead()
	r.add("Default", cookieSeq(errMissingStore))
	r.add("Default", cookieSeq(jarCookie("lang")))

	_, err := r.result(t.Context())

	require.ErrorContains(t, err, "no Feishu web session in chrome; log in at feishu.cn")
	require.NotErrorIs(t, err, fs.ErrNotExist)
}

func TestJarRead_ASessionOutweighsRowsThatFailed(t *testing.T) {
	r := newJarRead()
	r.add("Default", cookieSeq(errMissingStore))
	r.add("Default", cookieSeq(
		jarCookie("session"),
		errors.New("decrypting cookie msToken: decryption failed"),
		&kooky.Cookie{Cookie: http.Cookie{Name: "tenant", Value: "v", Domain: "gaotu.feishu.cn"}},
	))

	cookies, err := r.result(t.Context())

	require.NoError(t, err)
	require.Equal(t, []*http.Cookie{{Name: "session", Value: "v"}}, cookies)
}
