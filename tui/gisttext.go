package tui

import (
	"regexp"
	"strings"

	"github.com/amzyang/larkim/sync"
)

// The shapes lark-cli renders a rich-text body's non-text elements into. A clip
// sits in the paragraph it was written in, while a file or a folder is the
// attachment zone written under the words, in the same tag form a standalone
// file message renders into. An attribute carries its quotes and backslashes
// escaped, so the name is read with those escapes allowed for.
var (
	mediaRef   = regexp.MustCompile(`\[Media(?:: file_[A-Za-z0-9_-]+)?\]`)
	folderTree = regexp.MustCompile(`(?s)<folder key="` + attrPat + `"(?: name="(` + attrPat + `)")?>.*?</folder>`)
	fileTag    = regexp.MustCompile(`<(file|folder) key="` + attrPat + `"(?: name="(` + attrPat + `)")?\s*/>`)
)

const attrPat = `(?:[^"\\]|\\.)*`

var attrUnescape = strings.NewReplacer(`\\`, `\`, `\"`, `"`, `\n`, "\n", `\r`, "\r", `\t`, "\t")

// gistBody is a rendered body on its way to one line: every element named the
// way the client names it, where it was written, and the markup around the
// words taken off. A summary stands in for the whole message, so an element
// dropped from it makes the sender look like they never sent one — "a [Image] b"
// read back as "a b".
//
// Mentions are left as they came. A chat row resolves them itself, to colour
// the one that reaches the reader.
func gistBody(s string) string {
	if strings.Contains(s, "[") {
		s = sync.ImageRef.ReplaceAllString(s, msgTypeLabel("image"))
		s = mediaRef.ReplaceAllString(s, msgTypeLabel("media"))
	}
	if strings.Contains(s, "<") {
		// The expanded form goes first: a folder listing its children ends in
		// the same tag its children are written with.
		s = folderTree.ReplaceAllStringFunc(s, func(m string) string {
			return fileLabel("folder", folderTree.FindStringSubmatch(m)[1])
		})
		s = fileTag.ReplaceAllStringFunc(s, func(m string) string {
			g := fileTag.FindStringSubmatch(m)
			return fileLabel(g[1], g[2])
		})
	}
	return inlineText(s)
}

// fileLabel names one attachment the way attachGist names the file a message
// is: the kind it is, and the file's own name where the tag carries one.
func fileLabel(tag, name string) string {
	if name == "" {
		return msgTypeLabel(tag)
	}
	return msgTypeLabel(tag) + " " + attrUnescape.Replace(name)
}
