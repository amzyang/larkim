package tui

import (
	"bytes"
	"fmt"
	"regexp"

	uv "github.com/charmbracelet/ultraviolet"
)

// graphicsQueryID is the image id the capability query asks about. It sits
// below every id the avatars and pictures take, so the reply is never theirs.
const graphicsQueryID = 31

// GraphicsQuery asks the terminal whether it draws the kitty graphics protocol,
// then asks for its device attributes. Every terminal answers the second, so a
// reply to the first arriving before it is a yes and its absence is a no; the
// environment cannot say, because a multiplexer inside kitty inherits kitty's
// variables whether or not it passes images through.
var GraphicsQuery = fmt.Sprintf("\x1b_Gi=%d,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\\x1b[c", graphicsQueryID)

// graphicsYes is the terminal's yes to GraphicsQuery, as its bytes arrive.
var graphicsYes = fmt.Appendf(nil, "\x1b_Gi=%d;OK\x1b\\", graphicsQueryID)

// graphicsOK says whether a graphics reply is the yes to GraphicsQuery.
func graphicsOK(ev uv.KittyGraphicsEvent) bool {
	return ev.Options.ID == graphicsQueryID && string(ev.Payload) == "OK"
}

var deviceAttributes = regexp.MustCompile(`\x1b\[\?[0-9;]*c`)

// GraphicsReply reads what the terminal wrote back to GraphicsQuery. done is
// whether the device attributes arrived, after which nothing more is coming;
// ok is whether the graphics query was answered yes before them.
func GraphicsReply(b []byte) (ok, done bool) {
	loc := deviceAttributes.FindIndex(b)
	if loc == nil {
		return false, false
	}
	return bytes.Contains(b[:loc[0]], graphicsYes), true
}
