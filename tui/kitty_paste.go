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

// isClipboardPasteKey is ctrl+v or super+v (Cmd+V under kitty passthrough).
func isClipboardPasteKey(s string) bool {
	return s == "ctrl+v" || s == "super+v"
}

// clipboardPasteTarget is true when those keys should read deps.Clipboard
// instead of bubbles textinput, which only binds ctrl+v to the charm clipboard.
func (m Model) clipboardPasteTarget() bool {
	if m.config.open {
		return m.configTextInputLive()
	}
	if m.help.open {
		return m.help.filtering
	}
	switch m.mode {
	case modeInsert, modeCommand, modeFilter, modeSearch, modeEmoji, modeForward:
		return true
	}
	return false
}

// pasteIntoFocusedInput is true when a pastedMsg should reach forward() as
// tea.PasteMsg rather than the main composer textarea.
func (m Model) pasteIntoFocusedInput() bool {
	if m.mode == modeInsert {
		return false
	}
	if m.config.open {
		return m.configTextInputLive()
	}
	if m.help.open {
		return m.help.filtering
	}
	switch m.mode {
	case modeCommand, modeFilter, modeSearch, modeEmoji, modeForward:
		return true
	}
	return false
}

// configTextInputLive is whether the config panel has a one-line field taking input.
func (m Model) configTextInputLive() bool {
	if m.config.editing || m.config.filtering {
		return true
	}
	f := m.config.silence.form
	if !f.open {
		return false
	}
	return f.pick.open || f.field == fieldContains
}

// pasteTextFromClip is the string a paste inserts for the given clipboard kind.
func pasteTextFromClip(c clip) (text string, empty bool) {
	switch c.kind {
	case clipEmpty:
		return "", true
	case clipText:
		return c.text, false
	case clipImage:
		return imageRef(c.path), false
	case clipFile:
		text = fileRef(c.path)
		if isImagePath(c.path) {
			text = imageRef(c.path)
		}
		return text, false
	default:
		return "", true
	}
}
