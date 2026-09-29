package larkmd

import "slices"

// lineTable is the offset every line of a source starts at, which is what
// turns the byte offsets goldmark reports into the line and column a reader
// counts to. It is built once per body: naming a position by scanning the
// source again for each one is quadratic on a body of any size.
type lineTable []int

func newLineTable(src string) lineTable {
	t := lineTable{0}
	for i := range len(src) {
		if src[i] == '\n' {
			t = append(t, i+1)
		}
	}
	return t
}

// at names off as a 1-based line and column. The column counts bytes rather
// than runes: it points an editor at the byte goldmark reported, and a body
// that is mostly CJK would have every column disagree with the offset behind
// it otherwise.
func (t lineTable) at(off int) (line, col int) {
	i, exact := slices.BinarySearch(t, off)
	if !exact {
		i--
	}
	return i + 1, off - t[i] + 1
}
