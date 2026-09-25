package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// clipKind is the one form of the clipboard's contents the composer can use.
type clipKind int

const (
	clipEmpty clipKind = iota
	clipText
	clipFile
	clipImage
)

// clip is what the clipboard holds. clipImage carries a file larkim staged out
// of the clipboard's image data; clipFile one that was already on disk.
type clip struct {
	kind clipKind
	text string // clipText
	path string // clipFile, clipImage
}

// pastedDir holds images taken off the clipboard. It sits beside the
// downloaded ones, and nothing outside a live draft points at it, so deleting
// it costs nothing.
var pastedDir = filepath.Join("resources", "pasted")

// pastedTTL is how long a staged image is kept. The outbox lives in memory and
// dies with the process, so nothing older than the session can still be named
// by a draft; a week is far past that.
const pastedTTL = 7 * 24 * time.Hour

// imageExts are the pictures Feishu takes, which is what decides whether a
// file on the clipboard goes into the draft as an image or as its path.
var imageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".bmp": true}

func isImagePath(p string) bool { return imageExts[strings.ToLower(filepath.Ext(p))] }

// clipFlavour picks the branch the clipboard's own flavour list calls for.
// Dispatching on it is what makes the read safe: coercing blind is not, since
// asking the clipboard for a file URL succeeds on plain text and hands back a
// path that was never a file. A file is tested for first because a Finder copy
// carries the name as text beside it.
func clipFlavour(info string) clipKind {
	switch {
	case strings.Contains(info, "«class furl»"), strings.Contains(info, "alias,"):
		return clipFile
	case strings.Contains(info, "«class PNGf»"):
		return clipImage
	case strings.Contains(info, "«class utf8»"):
		return clipText
	}
	return clipEmpty
}

// readClipboard reports what the clipboard holds, staging image data into
// stageDir.
func readClipboard(stageDir string) (clip, error) {
	info, err := osa("clipboard info")
	if err != nil {
		return clip{}, err
	}
	switch clipFlavour(info) {
	case clipFile:
		path, err := osa("POSIX path of (the clipboard as «class furl»)")
		if err != nil {
			return clip{}, err
		}
		return clip{kind: clipFile, path: path}, nil
	case clipImage:
		path, err := stageClipboardImage(stageDir)
		if err != nil {
			return clip{}, err
		}
		return clip{kind: clipImage, path: path}, nil
	case clipText:
		out, err := exec.Command("pbpaste").Output()
		if err != nil {
			return clip{}, fmt.Errorf("pbpaste: %w", err)
		}
		if len(out) == 0 {
			return clip{}, nil
		}
		return clip{kind: clipText, text: string(out)}, nil
	}
	return clip{}, nil
}

// stageClipboardImage writes the clipboard's PNG data to a file of our own.
// AppleScript can only write to a file handle, so there is no way to take the
// bytes over a pipe.
func stageClipboardImage(stageDir string) (string, error) {
	if err := os.MkdirAll(stageDir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(stageDir, fmt.Sprintf("paste-%d.png", time.Now().UnixMilli()))
	// set eof truncates a path being reused rather than overwriting in place.
	if _, err := osa(
		"set png to (the clipboard as «class PNGf»)",
		`set f to open for access POSIX file "`+path+`" with write permission`,
		"set eof f to 0",
		"write png to f",
		"close access f",
	); err != nil {
		return "", err
	}
	return path, nil
}

// osa runs one osascript program. Its stderr is discarded rather than
// reported: every image-flavoured clipboard makes it print a JP2 colour-space
// warning while exiting 0, so stderr says nothing about success.
func osa(stmts ...string) (string, error) {
	args := make([]string, 0, len(stmts)*2)
	for _, s := range stmts {
		args = append(args, "-e", s)
	}
	out, err := exec.Command("osascript", args...).Output()
	if err != nil {
		return "", fmt.Errorf("osascript: %w", err)
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// prunePasted drops staged images past pastedTTL. A directory that cannot be
// read or cleaned is not a reason to fail starting the client.
func prunePasted(dataDir string, now time.Time) {
	dir := filepath.Join(dataDir, pastedDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) <= pastedTTL {
			continue
		}
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
}
