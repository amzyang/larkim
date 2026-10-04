package tui

import (
	"bytes"
	"fmt"
	"image/color"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
	"unsafe"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// decode feeds one batch of bytes the way a read delivers it and returns the
// handshake it left, folding in what earlier batches said.
func decode(t *testing.T, h Handshake, batches ...string) (Handshake, bool) {
	var dec uv.EventDecoder
	pending := []byte{}
	var done bool
	for _, b := range batches {
		h, pending, done = decodeHandshake(&dec, pending, h)
		require.False(t, done, "an earlier batch must not close the batch")
		pending = append(pending, b...)
	}
	h, pending, done = decodeHandshake(&dec, pending, h)
	return h, done
}

func TestDecodeHandshake_TakesEveryAnswerFromOneRoundTrip(t *testing.T) {
	t.Parallel()
	// The answers in the order a terminal gives them: graphics, cell size,
	// background, and the device attributes closing the batch.
	h, done := decode(t, Handshake{},
		"\x1b_Gi=31;OK\x1b\\"+ // graphics: yes
			"\x1b[6;16;8t"+ // cell: 8 wide, 16 tall
			"\x1b]11;rgb:1e1e/1e1e/1e1e\x1b\\"+ // background: dark grey
			"\x1b[?62;22c")
	require.True(t, done)
	require.True(t, h.Graphics)
	require.Equal(t, 8, h.CellW)
	require.Equal(t, 16, h.CellH)
	require.NotNil(t, h.BG)
	require.True(t, h.Dark, "a dark grey background leans dark")
}

func TestDecodeHandshake_TakesALightBackgroundAnsweredByBell(t *testing.T) {
	t.Parallel()
	h, done := decode(t, Handshake{}, "\x1b]11;rgb:ffff/ffff/ffff\x07\x1b[?62c")
	require.True(t, done)
	require.False(t, h.Graphics, "a terminal that never answered the graphics query does not draw them")
	require.False(t, h.Dark, "white leans light")
}

func TestDecodeHandshake_DropsKeysTypedInsideTheWindow(t *testing.T) {
	t.Parallel()
	h, done := decode(t, Handshake{Dark: true}, "jkl\x1b[?62c")
	require.True(t, done)
	require.False(t, h.Graphics)
	require.True(t, h.Dark, "the fallback holds")
}

func TestDecodeHandshake_KeepsWhatWasSaidWhenTheTerminalGoesQuiet(t *testing.T) {
	t.Parallel()
	h, done := decode(t, Handshake{Dark: true}, "\x1b_Gi=31;OK\x1b\\")
	require.False(t, done)
	require.True(t, h.Graphics)
	require.Nil(t, h.BG)
	require.True(t, h.Dark)
}

// openPTY opens a pseudo terminal pair, the slave standing in for a terminal
// the program owns. Only a real tty pins the bound Probe reads with: darwin
// takes no os.File deadline on one, and on a socketpair the deadline works,
// which is exactly the difference that once kept every picture undrawn.
func openPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	require.NoError(t, err)
	for _, op := range []uintptr{unix.TIOCPTYGRANT, unix.TIOCPTYUNLK} {
		_, _, errno := unix.Syscall(unix.SYS_IOCTL, m.Fd(), op, 0)
		require.Zero(t, errno, "grant and unlock the slave")
	}
	var name [128]byte
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, m.Fd(), unix.TIOCPTYGNAME, uintptr(unsafe.Pointer(&name[0])))
	require.Zero(t, errno, "ask the slave's path")
	s, err := os.OpenFile(strings.TrimRight(string(name[:]), "\x00"), os.O_RDWR, 0)
	require.NoError(t, err)
	return m, s
}

func TestProbe_ReadsAnswersFromATerminal(t *testing.T) {
	t.Parallel()
	master, slave := openPTY(t)
	defer master.Close()
	defer slave.Close()
	go func() {
		// Answer in the order the queries were asked, the device attributes
		// closing the batch.
		var query []byte
		buf := make([]byte, 256)
		for !bytes.Contains(query, []byte("\x1b[c")) {
			n, err := master.Read(buf)
			if err != nil {
				return
			}
			query = append(query, buf[:n]...)
		}
		master.WriteString("\x1b_Gi=31;OK\x1b\\" + "\x1b[6;30;14t" + "\x1b]11;rgb:1e1e/1e1e/1e1e\x1b\\" + "\x1b[?62c")
	}()
	h := Probe(slave, slave)
	require.True(t, h.Graphics, "the terminal answered yes and the probe must read it off a tty")
	require.Equal(t, 14, h.CellW)
	require.Equal(t, 30, h.CellH)
	require.True(t, h.Dark)
}

func TestProbe_LeavesTheFallbacksWhenNothingAnswers(t *testing.T) {
	t.Parallel()
	master, slave := openPTY(t)
	defer master.Close()
	defer slave.Close()
	start := time.Now()
	h := Probe(slave, slave)
	require.Less(t, time.Since(start), 5*time.Second, "a terminal that answers nothing is waited on briefly")
	require.False(t, h.Graphics)
	require.True(t, h.Dark, "the fallback holds")
}

// probeChildEnv marks the process whose stdin and stdout are the terminal the
// parent wired, standing in for one a shell handed the program.
const probeChildEnv = "LARKIM_PROBE_CHILD"

// TestProbe_ReadsAnswersOffATerminalItInherited is the regression a
// process-opened pty cannot pin: darwin registers an fd this process opened
// with the runtime poller, so SetReadDeadline works on it, while an inherited
// tty — stdin as a shell gives it — takes no deadline at all and once made
// every probe give up before it asked.
func TestProbe_ReadsAnswersOffATerminalItInherited(t *testing.T) {
	if os.Getenv(probeChildEnv) != "" {
		h := Probe(os.Stdin, os.Stdout)
		fmt.Fprintf(os.Stderr, "probe graphics=%v cellw=%d cellh=%d dark=%v\n", h.Graphics, h.CellW, h.CellH, h.Dark)
		os.Exit(0)
	}
	t.Parallel()
	master, slave := openPTY(t)
	defer master.Close()
	defer slave.Close()
	go func() {
		var query []byte
		buf := make([]byte, 256)
		for !bytes.Contains(query, []byte("\x1b[c")) {
			n, err := master.Read(buf)
			if err != nil {
				return
			}
			query = append(query, buf[:n]...)
		}
		master.WriteString("\x1b_Gi=31;OK\x1b\\" + "\x1b[6;30;14t" + "\x1b]11;rgb:1e1e/1e1e/1e1e\x1b\\" + "\x1b[?62c")
	}()
	cmd := exec.Command(os.Args[0], "-test.run", "^TestProbe_ReadsAnswersOffATerminalItInherited$")
	cmd.Env = append(os.Environ(), probeChildEnv+"=1")
	cmd.Stdin, cmd.Stdout = slave, slave
	var status bytes.Buffer
	cmd.Stderr = &status
	require.NoError(t, cmd.Run(), "the child runs on the pty")
	require.Contains(t, status.String(), "graphics=true",
		"the child read the answers off a tty it inherited: %s", status.String())
}

func TestNew_DrawsTheFirstFrameTheHandshakeDescribed(t *testing.T) {
	t.Parallel()
	m := New(Deps{DataDir: t.TempDir(), Term: Handshake{Graphics: true, CellW: 8, CellH: 16, BG: color.Black, Dark: true}})
	require.IsType(t, &kittyAvatars{}, m.avatars)
	require.NotNil(t, m.pics)
	require.Equal(t, 8, m.cellW)
	require.Equal(t, 16, m.cellH)
}

func TestNew_KeepsTheStandInsForATerminalThatDrewNoPictures(t *testing.T) {
	t.Parallel()
	m := New(Deps{})
	require.IsType(t, textAvatars{}, m.avatars)
	require.Nil(t, m.pics)
	require.True(t, m.dark, "the palette stays at the dark fallback")
}
