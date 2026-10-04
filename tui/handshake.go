package tui

import (
	"errors"
	"fmt"
	"image/color"
	"os"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// Handshake is what the terminal answered about itself before the first
// frame: whether it draws the kitty graphics protocol, the cell size avatars
// are transmitted for, and the background the palette leans from. Every zero
// value is a fallback the first frame already drew — no pictures, the
// placer's own cell ratio, a dark background — so a terminal that said
// nothing still gets a frame, only not this terminal's own.
type Handshake struct {
	// Graphics says the terminal draws the kitty graphics protocol, which is
	// what avatars and message pictures are transmitted as.
	Graphics bool
	// Width and Height are the grid in cells. CellW and CellH are the cell
	// size in pixels, zero until something says.
	Width, Height int
	CellW, CellH  int
	// Display is the screen's scale as kitty states it, zero when it never
	// said — a multiplexer swallows the query — which draws at scale 1.
	Display display
	// BG is the terminal's background colour, nil when it never said, which
	// leaves the palette at Dark's fallback of dark.
	BG   color.Color
	Dark bool
}

// handshakeWait bounds how long the answers are waited on. It is a ceiling,
// not a wait: a terminal that has anything to say says it within a frame, so
// the ceiling only binds on one that answers nothing at all, and that one
// has no answers to give.
const handshakeWait = 100 * time.Millisecond

// graphicsQueryID is the image id the capability query asks about. It sits
// inside the avatars' range, which is safe because the probe completes before
// any image is transmitted and every later transmission is quiet (q=2), so no
// other reply can carry this id.
const graphicsQueryID = 31

// handshakeQuery asks everything the first frame wants to know in one write:
// graphics support, the cell size, the display scale, the background colour,
// and last the
// device attributes. Every terminal answers the last one, and terminals
// answer in the order they were asked, so its arrival closes the batch — a
// reply that never came by then is one this terminal had no way to give, not
// one still on its way.
var handshakeQuery = fmt.Sprintf("\x1b_Gi=%d,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\", graphicsQueryID) +
	ansi.WindowOp(ansi.RequestCellSizeWinOp) +
	displayQuery +
	"\x1b]11;?\x1b\\" +
	"\x1b[c"

// Probe asks the terminal about itself and waits for the answers, so the
// first frame is drawn the way the whole session keeps looking: no colour
// blocks turning into avatars a blink later, no palette turning over. Any
// failure — stdin not the terminal, no raw mode, no answer in time — leaves
// the fallbacks.
func Probe(in, out *os.File) Handshake {
	h := Handshake{Dark: true}
	// The window size needs no answer at all: one ioctl says the grid, and a
	// terminal that fills the pixel fields — kitty does — has said the cell
	// size too, without a query it has to answer.
	if ws, err := unix.IoctlGetWinsize(int(out.Fd()), unix.TIOCGWINSZ); err == nil && ws.Col > 0 {
		h.Width, h.Height = int(ws.Col), int(ws.Row)
		if ws.Xpixel > 0 {
			h.CellW, h.CellH = int(ws.Xpixel)/int(ws.Col), int(ws.Ypixel)/int(ws.Row)
		}
	}
	if !term.IsTerminal(int(in.Fd())) {
		return h
	}
	old, err := term.MakeRaw(int(in.Fd()))
	if err != nil {
		return h
	}
	defer term.Restore(int(in.Fd()), old)
	if _, err := out.WriteString(handshakeQuery); err != nil {
		return h
	}
	return probeAnswers(in, h)
}

// probeAnswers reads the answers until the device attributes close the batch,
// the reader fails, or handshakeWait runs out. Keys typed inside the window
// are read and dropped: the window is milliseconds, before there is anything
// to type into.
//
// The fd is polled rather than given an os.File deadline: darwin never
// registers a blocking tty with the runtime poller, so SetReadDeadline fails
// on it, and a reader goroutine abandoned mid-Read would race whatever owns
// the fd next — the TUI's own reader — for its keystrokes.
func probeAnswers(in *os.File, h Handshake) Handshake {
	fd := int(in.Fd())
	unix.SetNonblock(fd, true)
	defer unix.SetNonblock(fd, false)
	deadline := time.Now().Add(handshakeWait)
	var dec uv.EventDecoder
	pending := []byte{}
	buf := make([]byte, 256)
	for {
		left := time.Until(deadline)
		if left <= 0 {
			return h
		}
		ready, err := unix.Poll([]unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}, int(left.Milliseconds()))
		if err != nil || ready == 0 {
			return h
		}
		n, err := in.Read(buf)
		if n == 0 && errors.Is(err, unix.EAGAIN) {
			continue
		}
		pending = append(pending, buf[:n]...)
		var done bool
		h, pending, done = decodeHandshake(&dec, pending, h)
		if done || err != nil {
			return h
		}
	}
}

// decodeHandshake folds the answer bytes as they arrive into h, keeping what
// a later read has to finish. done says the device attributes closed the
// batch: nothing more is coming, so the caller stops waiting.
func decodeHandshake(dec *uv.EventDecoder, pending []byte, h Handshake) (Handshake, []byte, bool) {
	for len(pending) > 0 {
		size, ev := dec.Decode(pending)
		if size == 0 {
			break
		}
		pending = pending[size:]
		switch e := ev.(type) {
		case uv.KittyGraphicsEvent:
			h.Graphics = graphicsOK(e)
		case uv.BackgroundColorEvent:
			h.BG, h.Dark = e.Color, e.IsDark()
		case uv.CellSizeEvent:
			if e.Width > 0 && e.Height > 0 {
				h.CellW, h.CellH = e.Width, e.Height
			}
		case uv.CapabilityEvent:
			h.Display.read(e.Content)
		case uv.PrimaryDeviceAttributesEvent:
			return h, pending, true
		}
	}
	return h, pending, false
}

// graphicsOK says whether a graphics reply is the yes to handshakeQuery.
func graphicsOK(ev uv.KittyGraphicsEvent) bool {
	return ev.Options.ID == graphicsQueryID && string(ev.Payload) == "OK"
}
