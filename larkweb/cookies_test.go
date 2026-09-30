package larkweb

import (
	"testing"

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
