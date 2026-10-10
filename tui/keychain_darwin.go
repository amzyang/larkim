//go:build darwin

package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/larkcli"
)

const (
	keychainWarnOK  = "lark-cli keychain slow; ran config keychain-downgrade"
	keychainWarnBad = "lark-cli keychain slow → lark-cli config keychain-downgrade"
)

func keychainStartup(d Deps) tea.Cmd {
	if !larkcli.MasterKeyMissing() {
		return nil
	}
	return func() tea.Msg {
		warn := keychainWarnBad
		if exec, ok := d.Client.(*larkcli.ExecClient); ok {
			ctx := larkcli.WithLane(begin("keychain-downgrade"), larkcli.LaneBackground)
			if err := exec.KeychainDowngrade(ctx); err != nil {
				d.log().WarnContext(ctx, "keychain-downgrade", "err", err)
			} else if !larkcli.MasterKeyMissing() {
				warn = keychainWarnOK
			}
		}
		return statusWarnMsg{warn}
	}
}
