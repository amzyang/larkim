package emoji

import (
	"bytes"
	"image"
	"image/png"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTable_PicturesComplete audits the generated table against the sheet it
// ships with: every rectangle lies inside the sheet, so the picture a renderer
// draws is there for every emoji the table names. The rest of the completeness
// story — names, non-empty rectangles, tone variants resolving — is asserted
// by the tests beside the table itself; this one holds the pair (table,
// sheet) together, which is the part that breaks when one is regenerated
// without the other.
func TestTable_PicturesComplete(t *testing.T) {
	sheet, err := png.Decode(bytes.NewReader(sheetPNG))
	require.NoError(t, err)
	for _, e := range table {
		x, y, w, h := e.Rect[0], e.Rect[1], e.Rect[2], e.Rect[3]
		require.Truef(t, image.Rect(x, y, x+w, y+h).In(sheet.Bounds()),
			"%s names %v, which is outside the sprite %v", e.Key, e.Rect, sheet.Bounds())
	}
}
