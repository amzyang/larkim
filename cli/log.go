package cli

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// maxLogBytes caps the log before it is rolled over once. A --debug session
// writes a few lines a second, so the cap is what keeps a forgotten flag from
// filling the disk.
const maxLogBytes = 8 << 20

// altScreen marks commands that own the terminal: their logs go to the file
// only, because stderr is the alternate screen.
const altScreen = "larkim/alt-screen"

// initLog builds the one logger this process shares. Records always reach
// <data_dir>/larkim.log, so a failure that happened once stays readable
// afterwards instead of having to be reproduced; --debug adds the per-call
// detail on top.
func (a *App) initLog(cmd *cobra.Command) {
	level := slog.LevelInfo
	if a.debug {
		level = slog.LevelDebug
	}
	w, err := a.logWriter(cmd)
	if err != nil {
		// The log is a derived artifact; it must never be the thing that
		// breaks the tool.
		fmt.Fprintln(a.Err, "larkim: log file unavailable:", err)
		w = a.Err
	}
	a.log = slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})).
		With("pid", os.Getpid(), "cmd", cmd.Name())
	a.log.Debug("larkim", "version", a.Version, "data_dir", a.cfg.DataDir, "debug", a.debug)
}

func (a *App) logWriter(cmd *cobra.Command) (io.Writer, error) {
	f, err := openLog(a.cfg.LogPath())
	if err != nil {
		return nil, err
	}
	if cmd.Annotations[altScreen] != "" {
		return f, nil
	}
	return io.MultiWriter(a.Err, f), nil
}

// openLog appends to path, rolling the file over once when it is full. The
// handle is never closed: a record is one write() on an O_APPEND file, so
// nothing is buffered to lose at exit, and several larkim processes can share
// the file, which is why every line carries a pid.
func openLog(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if st, err := os.Stat(path); err == nil && st.Size() >= maxLogBytes {
		_ = os.Rename(path, path+".1")
	}
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
}
