package tui

import "github.com/amzyang/larkim/tui/component/confirm"

// confirmNoticeStyles is how a pending y/n reads in the status bar and on the
// Silence tab: warn body, underlined y, dim n.
func confirmNoticeStyles() confirm.Styles {
	return confirm.Styles{
		Body:   stConfirm,
		Affirm: stAccent.Bold(true).Underline(true),
		Deny:   stDim,
		Punct:  stConfirm,
	}
}

func formatConfirmNotice(text string, maxW int) string {
	return confirm.Format(text, maxW, confirmNoticeStyles())
}
