//go:build !darwin

package tui

import tea "charm.land/bubbletea/v2"

func keychainStartup(Deps) tea.Cmd { return nil }
