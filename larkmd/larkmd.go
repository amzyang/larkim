// Package larkmd reads a markdown body the way a parser does rather than the
// way a regex does, so a reference inside a code span or a fenced block is
// text the sender is quoting rather than something to act on. It is where
// larkim answers what a body about to be sent actually says: the composer and
// the send commands resolve its pictures through it.
package larkmd

import (
	"github.com/yuin/goldmark/v2/extension"
	"github.com/yuin/goldmark/v2/parser"
)

// Parser reads a body the way larkim draws one: CommonMark plus the pipe tables
// Feishu renders. The send side and the message list share it, so a body parses
// the same going out as it does coming back, and a parser is safe to share, so
// one is built for the process rather than per message.
var Parser = parser.New(parser.WithExtensions(extension.NewTableParser()))
