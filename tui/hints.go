package tui

import (
	"github.com/amzyang/larkim/tui/component/keyhint"
)

// KeyBinding is one shortcut row for larkim hint bars and the help table.
type KeyBinding = keyhint.Binding

// Overlay footers use renderKeyHintBar; the status bar uses packKeyHints.

var (
	composerHintBar = []KeyBinding{
		{Keys: "Enter", Desc: "send"},
		{Keys: "Shift+Enter", Desc: "newline"},
	}
	writeHintBinding = KeyBinding{Keys: "i", Desc: "to write"}
	candHintBar      = []KeyBinding{
		{Keys: "Enter", Desc: "fill"},
		{Keys: "1-9", Desc: "pick"},
		{Keys: "Esc", Desc: "cancel"},
	}
	pumHintBar = []KeyBinding{
		{Keys: "Tab/Enter", Desc: "accept"},
		{Keys: "^n/^p", Desc: "move"},
		{Keys: "Esc", Desc: "dismiss"},
	}
	cmdCompHintBar = []KeyBinding{
		{Keys: "Tab/^n/^p", Desc: "match"},
		{Keys: "Esc", Desc: "dismiss"},
	}
	reactHintBar = []KeyBinding{
		{Keys: "Enter", Desc: "react"},
		{Keys: "1-9", Desc: "pick"},
		{Keys: "Esc", Desc: "cancel"},
	}
	targetsHintBar = []KeyBinding{
		{Keys: "j/k", Desc: "move"},
		{Keys: "enter", Desc: "open"},
		{Keys: "digits", Desc: "jump"},
		{Keys: "esc", Desc: "cancel"},
	}
	contextMenuHintBar = []KeyBinding{
		{Keys: "j/k", Desc: "move"},
		{Keys: "Enter", Desc: "run"},
		{Keys: "Esc", Desc: "close"},
	}
	helpOverlayHintBar = []KeyBinding{
		{Keys: "/", Desc: "filter"},
		{Keys: "j/k", Desc: "scroll"},
		{Keys: "Esc", Desc: "close"},
	}
	configHintBar = []KeyBinding{
		{Keys: "enter", Desc: "edit"},
		{Keys: "&", Desc: "default"},
		{Keys: "/", Desc: "filter"},
		{Keys: "tab", Desc: "Silence"},
		{Keys: "esc", Desc: "close"},
	}
	configEditHintBar = []KeyBinding{
		{Keys: "enter", Desc: "save"},
		{Keys: "esc", Desc: "cancel"},
	}
	replyBarHintBinding = KeyBinding{Keys: "^r", Desc: "drops the quote"}
)

func keyhintStyles() keyhint.Styles {
	return keyhint.Styles{Key: stHelpKey, Desc: stDim}
}

func macKeys(s string) string { return keyhint.MacKeys(s) }

func renderKey(s string) string { return keyhint.RenderKey(s, keyhintStyles()) }

func renderKeyDesc(b KeyBinding) string { return keyhint.RenderDesc(b, keyhintStyles()) }

// renderMenuKeyDesc is one context-menu row's shortcut, styled through
// keyhint the same way the status bar and overlay footers render theirs.
func renderMenuKeyDesc(b KeyBinding) string {
	if b.Keys == "" {
		b.Keys = "enter"
	}
	return renderKeyDesc(b)
}

func renderKeyHintBar(bindings []KeyBinding, w int) string {
	return keyhint.HintBar(bindings, w, keyhintStyles())
}

func packKeyHints(bindings []KeyBinding, availWidth int, gap string) string {
	return keyhint.Pack(bindings, availWidth, gap, keyhintStyles())
}
