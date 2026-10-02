package tui

import (
	"encoding/hex"
	"strconv"
	"strings"
)

// display is what kitty says about the screen its window is on: the DPI it
// renders the font at and the OS it runs on. Pictures need the scale between
// an image's pixels and the screen's, which is what the Lark client draws
// them at, and the cell size alone cannot tell a retina screen from a large
// font.
type display struct {
	dpi float64
	os  string
}

// displayQuery asks kitty for both halves, through the XTGETTCAP names its
// query-terminal kitten uses. A terminal or multiplexer that does not know them
// answers nothing or a refusal, and the scale stays 1.
var displayQuery = termcapQuery("kitty-query-dpi_x") + termcapQuery("kitty-query-os_name")

func termcapQuery(name string) string {
	return "\x1bP+q" + hex.EncodeToString([]byte(name)) + "\x1b\\"
}

// read takes one XTGETTCAP answer, already decoded to name=value, and reports
// whether it was one of the two display asks for.
func (d *display) read(content string) bool {
	name, value, ok := strings.Cut(content, "=")
	if !ok {
		return false
	}
	switch name {
	case "kitty-query-dpi_x":
		dpi, err := strconv.ParseFloat(value, 64)
		if err != nil || dpi <= 0 {
			return false
		}
		d.dpi = dpi
	case "kitty-query-os_name":
		d.os = value
	default:
		return false
	}
	return true
}

// scale is how many screen pixels one image pixel takes. kitty derives its
// DPI from the window's backing scale against a base of 72 on macOS and 96
// everywhere else, and the base is the terminal's OS rather than this
// process's, which over ssh is another machine. Until both halves are known
// the answer is 1, today's size.
func (d display) scale() float64 {
	if d.dpi <= 0 || d.os == "" {
		return 1
	}
	base := 96.0
	if d.os == "macos" {
		base = 72
	}
	return max(1, d.dpi/base)
}
