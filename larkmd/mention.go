package larkmd

import "unicode"

// AtBoundary reports whether the @ at i opens a mention rather than sitting
// inside a word, which is what keeps an email address from being read as one.
func AtBoundary(s string, i int) bool {
	if i == 0 {
		return true
	}
	r := lastRune(s[:i])
	return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '.' && r != '-'
}

func lastRune(s string) rune {
	var last rune
	for _, r := range s {
		last = r
	}
	return last
}

// mentionName is the run a mention names, which ends where the sentence goes
// on: at a space, or at the punctuation that closes the clause. It is empty
// when the @ names nobody, a lone @ or one in front of punctuation being a
// character the sender typed rather than somebody to reach.
func mentionName(rest string) string {
	end := 0
	for i, r := range rest {
		if unicode.IsSpace(r) || isNamePunct(r) {
			break
		}
		end = i + len(string(r))
	}
	return rest[:end]
}

// isNamePunct reports punctuation no name carries. The three it lets through
// are the ones that do: an account name is written zhang.san or zhang-san,
// and _ holds identifiers together.
func isNamePunct(r rune) bool {
	return unicode.IsPunct(r) && r != '_' && r != '-' && r != '.'
}
