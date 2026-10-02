package tui

import (
	"os"

	tea "charm.land/bubbletea/v2"
)

// Kitty user-var passthrough for Cmd+V. While in_larkim is set, kitty.conf can
// unbind paste_from_clipboard for super+v only in this window:
//
//	map --when-focus-on var:in_larkim super+v
//
// (no action: the key is delivered to larkim as super+v instead of a text-only
// paste, so image-only clipboards still reach readClipboard.)
const (
	kittySetInLarkim   = "\x1b]1337;SetUserVar=in_larkim=MQ==\x07"
	kittyClearInLarkim = "\x1b]1337;SetUserVar=in_larkim\x07"
)

func kittyPastePassthrough() bool { return os.Getenv("KITTY_WINDOW_ID") != "" }

func kittyClaimPaste() tea.Cmd {
	if !kittyPastePassthrough() {
		return nil
	}
	return tea.Raw(kittySetInLarkim)
}

func kittyReleasePaste() tea.Cmd {
	if !kittyPastePassthrough() {
		return nil
	}
	return tea.Raw(kittyClearInLarkim)
}

// composerSmartPaste is true when a terminal paste should re-read the OS
// pasteboard (HTML, images, files) instead of inserting PasteMsg.Content.
func (m Model) composerSmartPaste() bool {
	if m.config.open || m.help.open {
		return false
	}
	return m.mode == modeInsert
}
