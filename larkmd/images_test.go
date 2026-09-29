package larkmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestImages_ReadsDestinationsInOrder(t *testing.T) {
	refs := Images("first ![a](./a.png) then ![b](img_kept)\n")
	require.Len(t, refs, 2)
	require.Equal(t, "./a.png", refs[0].Dest)
	require.Equal(t, "img_kept", refs[1].Dest)
	require.Less(t, refs[0].Start, refs[1].Start)
}

func TestImages_SkipsAFencedBlock(t *testing.T) {
	// A path inside a fence is something the sender is quoting, so nothing
	// should go looking for it on disk.
	require.Empty(t, Images("```\n![d](./missing.png)\n```\n"))
	require.Empty(t, Images("~~~sh\n![d](./missing.png)\n~~~\n"))
}

func TestImages_SkipsACodeSpan(t *testing.T) {
	require.Empty(t, Images("write `![d](./missing.png)` to embed one\n"))
}

func TestImages_SkipsAReferenceWithNoDestination(t *testing.T) {
	require.Empty(t, Images("![nothing]()\n"))
}

func TestImages_NamesTheLineTheReferenceOpensOn(t *testing.T) {
	refs := Images("one\n\n两个字 ![a](./a.png)\n")
	require.Len(t, refs, 1)
	require.Equal(t, 3, refs[0].Line)
	// The column counts bytes, so the CJK ahead of it is three bytes a rune.
	require.Equal(t, 1+len("两个字 "), refs[0].Column)
}

func TestImages_ReadsThroughTablesAndLists(t *testing.T) {
	refs := Images("- ![a](./a.png)\n  - ![b](./b.png)\n\n| x |\n| - |\n| ![c](./c.png) |\n")
	require.Len(t, refs, 3)
	require.Equal(t, []string{"./a.png", "./b.png", "./c.png"},
		[]string{refs[0].Dest, refs[1].Dest, refs[2].Dest})
	require.Less(t, refs[0].Start, refs[1].Start)
	require.Less(t, refs[1].Start, refs[2].Start)
}

func TestReplaceImages_WritesOnlyOverTheDestination(t *testing.T) {
	n := 0
	got := ReplaceImages("see ![the **shot**](./a.png) here", func(ImageRef) string {
		n++
		return "img_1"
	})
	require.Equal(t, 1, n)
	require.Equal(t, "see ![the **shot**](img_1) here", got)
}

func TestReplaceImages_KeepsTheAngleBracketFormAPathWithSpacesNeeds(t *testing.T) {
	got := ReplaceImages("![](<my shot.png>)", func(ImageRef) string { return "img_1" })
	require.Equal(t, "![](<img_1>)", got)
}

func TestReplaceImages_LeavesAFencedReferenceAlone(t *testing.T) {
	src := "![real](./a.png)\n\n```\n![quoted](./b.png)\n```\n"
	got := ReplaceImages(src, func(ImageRef) string { return "img_1" })
	require.Equal(t, "![real](img_1)\n\n```\n![quoted](./b.png)\n```\n", got)
}

func TestReplaceImages_AnswersEachReferenceOfTheSamePictureSeparately(t *testing.T) {
	n := 0
	got := ReplaceImages("![](./a.png) and ![](./a.png)", func(ImageRef) string {
		n++
		return "img_" + string(rune('0'+n))
	})
	require.Equal(t, "![](img_1) and ![](img_2)", got)
}

func TestReplaceImages_LeavesABodyWithNoPicturesAsItWas(t *testing.T) {
	src := "just **words** and a [link](https://example.com)\n"
	require.Equal(t, src, ReplaceImages(src, func(ImageRef) string { return "img_1" }))
}

func TestLineTable_NamesEveryOffset(t *testing.T) {
	lines := newLineTable("ab\ncd\n")
	for _, c := range []struct{ off, line, col int }{
		{0, 1, 1}, {1, 1, 2}, {2, 1, 3}, {3, 2, 1}, {4, 2, 2}, {6, 3, 1},
	} {
		line, col := lines.at(c.off)
		require.Equal(t, [2]int{c.line, c.col}, [2]int{line, col}, "offset %d", c.off)
	}
}
