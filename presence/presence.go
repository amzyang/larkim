// Package presence lets the process that raises banners find the TUIs running
// on this machine: whether one has the reader's attention, which chat it shows,
// and the kitty window it lives in. Each TUI answers on a unix socket of its
// own under one directory; the banner side dials them and never the other way
// round, so a TUI never learns anything about the sweep from here.
//
// There is no authentication: the directory is 0700 and each socket 0600, so
// only this user reaches them, and nothing listens on the network.
package presence

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"
)

// ioTimeout bounds one request either way. Both ends are on this machine and
// the answer is a few atomics read, so anything slower is a TUI that hung.
const ioTimeout = time.Second

// State is what a TUI says about itself.
type State struct {
	PID     int    `json:"pid"`
	Focused bool   `json:"focused"`
	Chat    string `json:"chat,omitempty"`
	// KittyWindowID and KittyListenOn are the TUI's own KITTY_WINDOW_ID and
	// KITTY_LISTEN_ON: the window to raise and the kitty that can raise it.
	KittyWindowID string `json:"kitty_window_id,omitempty"`
	KittyListenOn string `json:"kitty_listen_on,omitempty"`
	path          string
}

// Live is the state a running TUI keeps current for its socket to report. The
// update loop writes it and the socket's goroutines read it.
type Live struct {
	KittyWindowID string
	KittyListenOn string
	focused       atomic.Bool
	chat          atomic.Pointer[string]
}

// Set records whether the TUI has focus and which chat it shows. It runs on
// every update, so an unchanged chat is not stored again.
func (l *Live) Set(focused bool, chat string) {
	l.focused.Store(focused)
	if c := l.chat.Load(); c == nil || *c != chat {
		l.chat.Store(&chat)
	}
}

// State is what the socket reports now.
func (l *Live) State() State {
	s := State{PID: os.Getpid(), Focused: l.focused.Load(), KittyWindowID: l.KittyWindowID, KittyListenOn: l.KittyListenOn}
	if c := l.chat.Load(); c != nil {
		s.Chat = *c
	}
	return s
}

type request struct {
	Op   string `json:"op"`
	Chat string `json:"chat,omitempty"`
}

const (
	opState = "state"
	opOpen  = "open"
)

// Server is one TUI's socket.
type Server struct {
	l *net.UnixListener
}

// Listen opens this process's socket under dir, creating dir 0700. A socket
// left by an earlier process that had this pid is replaced.
func Listen(dir string) (*Server, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	// MkdirAll leaves an existing directory's mode as it was.
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, strconv.Itoa(os.Getpid())+".sock")
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		l.Close()
		return nil, err
	}
	return &Server{l: l}, nil
}

// Serve answers requests until ctx is done, then closes the socket, which
// removes its file. open is called, on the socket's goroutine, with the chat
// an open request names.
func (s *Server) Serve(ctx context.Context, live *Live, open func(chat string)) error {
	stop := context.AfterFunc(ctx, func() { s.l.Close() })
	defer stop()
	defer s.l.Close()
	for {
		conn, err := s.l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go handle(conn, live, open)
	}
}

func handle(conn net.Conn, live *Live, open func(string)) {
	defer conn.Close()
	if conn.SetDeadline(time.Now().Add(ioTimeout)) != nil {
		return
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}
	var req request
	if json.Unmarshal(line, &req) != nil {
		return
	}
	if req.Op == opOpen && req.Chat != "" {
		open(req.Chat)
	}
	b, err := json.Marshal(live.State())
	if err != nil {
		return
	}
	_, _ = conn.Write(append(b, '\n'))
}

// Find asks every socket under dir what its TUI is doing. A socket nobody
// listens on is what a crashed TUI leaves, and is removed; any other failure
// skips that TUI and is returned beside the ones that answered.
func Find(ctx context.Context, dir string) ([]State, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.sock"))
	if err != nil {
		return nil, err
	}
	var out []State
	var errs []error
	for _, p := range paths {
		st, err := ask(ctx, p, request{Op: opState})
		switch {
		case err == nil:
			out = append(out, st)
		case errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, fs.ErrNotExist):
			if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, err)
			}
		default:
			errs = append(errs, err)
		}
	}
	return out, errors.Join(errs...)
}

// Open asks the TUI behind p to open chat.
func Open(ctx context.Context, p State, chat string) error {
	_, err := ask(ctx, p.path, request{Op: opOpen, Chat: chat})
	return err
}

func ask(ctx context.Context, path string, req request) (State, error) {
	ctx, cancel := context.WithTimeout(ctx, ioTimeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return State{}, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return State{}, err
		}
	}
	b, err := json.Marshal(req)
	if err != nil {
		return State{}, err
	}
	if _, err := conn.Write(append(b, '\n')); err != nil {
		return State{}, fmt.Errorf("presence %s: %w", path, err)
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return State{}, fmt.Errorf("presence %s: %w", path, err)
	}
	var st State
	if err := json.Unmarshal(line, &st); err != nil {
		return State{}, fmt.Errorf("presence %s: %w", path, err)
	}
	st.path = path
	return st, nil
}

// Raise brings the kitty window p lives in to the front. kitty's focus-window,
// through that kitty's own remote control, moves the focus inside kitty;
// activating the app is what brings kitty itself forward over whatever was in
// front.
func Raise(ctx context.Context, p State) error {
	if p.KittyWindowID == "" || p.KittyListenOn == "" {
		return errors.New("presence: the TUI is not in a kitty window that takes remote control")
	}
	out, err := exec.CommandContext(ctx, "kitten", "@", "--to", p.KittyListenOn,
		"focus-window", "--match", "id:"+p.KittyWindowID).CombinedOutput()
	if err != nil {
		return fmt.Errorf("kitten focus-window: %w: %s", err, bytes.TrimSpace(out))
	}
	if out, err := exec.CommandContext(ctx, "open", "-b", "net.kovidgoyal.kitty").CombinedOutput(); err != nil {
		return fmt.Errorf("open kitty: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}
