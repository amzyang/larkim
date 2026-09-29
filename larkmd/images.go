package larkmd

import (
	"strings"

	"github.com/yuin/goldmark/v2/ast"
)

// ImageRef is one image reference a body carries: where it points, where it
// says so, and the bytes the destination occupies. A caller writing a key
// over that span leaves the alt text and the angle-bracket form a path with
// spaces needs exactly as they were.
type ImageRef struct {
	Dest         string
	Line, Column int
	Start, Stop  int
}

// Images is every picture a body really names, in the order it names them.
// A reference with no destination is left out: there is nothing to resolve,
// and nothing to write a key over either.
func Images(src string) []ImageRef {
	if !strings.Contains(src, "![") {
		return nil
	}
	b := []byte(src)
	var lines lineTable
	var out []ImageRef
	_ = ast.Walk(mdParser.Parse(b), func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		img, ok := n.(*ast.Image)
		if !ok || !entering {
			return ast.WalkContinue, nil
		}
		idx := img.Destination.Index()
		if idx.Start >= idx.Stop {
			return ast.WalkContinue, nil
		}
		if lines == nil {
			lines = newLineTable(src)
		}
		// The reference reads from its `!`, which is where a reader looking
		// for it would start; the span to write over is the destination's.
		line, col := lines.at(max(img.Pos(), 0))
		out = append(out, ImageRef{
			Dest: img.Destination.Value(b), Line: line, Column: col,
			Start: idx.Start, Stop: idx.Stop,
		})
		return ast.WalkContinue, nil
	})
	return out
}

// ReplaceImages writes each destination repl answers with over the one that
// was there. Only the destination moves, so everything the sender spelled
// around it survives.
func ReplaceImages(src string, repl func(ImageRef) string) string {
	refs := Images(src)
	if len(refs) == 0 {
		return src
	}
	var b strings.Builder
	b.Grow(len(src))
	at := 0
	for _, ref := range refs {
		b.WriteString(src[at:ref.Start])
		b.WriteString(repl(ref))
		at = ref.Stop
	}
	b.WriteString(src[at:])
	return b.String()
}
