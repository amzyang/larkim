package sync

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// stickerCopyPerTick bounds one tick's copying. The work is local file IO, so
// the bound is only there to keep a first run from stalling a tick.
const stickerCopyPerTick = 20

// stickerSubdir is where sticker pictures land below the data dir. A resource
// row holds a path relative to that dir, so this is also the prefix a reader
// joins onto its own copy of it.
const stickerSubdir = "resources/stickers"

// DefaultClientDir is where the macOS Lark client keeps its per-account
// storage. Every install and account below it is searched: a sticker is named
// by its own key, so whichever account cached it holds the same picture.
func DefaultClientDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Library", "Application Support")
}

// errStickerNotCached keeps its retries: the picture appears once the sticker
// is viewed in the Lark client.
var errStickerNotCached = errors.New("not in the Lark client's sticker storage")

// copyStickers fills in the sticker pictures of messages that are waiting for
// one. Feishu's resource API refuses a sticker's file_key (234002
// Unauthorized) whatever identity asks, so lark-cli never returns one and the
// Lark client's own storage is the only place the picture exists here.
func (s *Syncer) copyStickers(ctx context.Context, now time.Time) (int, error) {
	if s.Opt.DataDir == "" || s.Opt.ClientDir == "" {
		return 0, nil
	}
	due, err := s.Store.StickerResourcesDue(ctx, now.UnixMilli(), stickerCopyPerTick)
	if err != nil {
		return 0, err
	}
	done := 0
	for _, p := range due {
		src, size := findSticker(s.Opt.ClientDir, p.FileKey)
		if src == "" {
			if err := s.failResource(ctx, p, errStickerNotCached, now); err != nil {
				return done, err
			}
			continue
		}
		if s.oversize(size) {
			if err := s.skipResource(ctx, p, size); err != nil {
				return done, err
			}
			continue
		}
		rel, size, err := copySticker(src, s.Opt.DataDir, p.FileKey)
		if err != nil {
			if err := s.failResource(ctx, p, err, now); err != nil {
				return done, err
			}
			continue
		}
		if err := s.Store.MarkResourceDone(ctx, p.FileKey, rel, size); err != nil {
			return done, err
		}
		done++
	}
	return done, nil
}

// findSticker is the Lark client's copy of one sticker and its size, or "" while
// the client has never drawn it. Stickers somebody sent sit under
// resources/stickers as <key>.<ext>; the sets the user sends from sit under
// sticker_sets/<set>/<key> with no extension at all.
func findSticker(clientDir, key string) (string, int64) {
	for _, pat := range []string{
		filepath.Join(clientDir, "LarkShell*", "sdk_storage", "*", "resources", "stickers", key+".*"),
		filepath.Join(clientDir, "LarkShell*", "sdk_storage", "*", "sticker_sets", "*", key),
	} {
		matches, _ := filepath.Glob(pat)
		for _, m := range matches {
			if st, err := os.Stat(m); err == nil && st.Mode().IsRegular() && st.Size() > 0 {
				return m, st.Size()
			}
		}
	}
	return "", 0
}

// copySticker copies one picture below the data dir and returns its path
// relative to it. The client stores its own sets without an extension, so the
// format is read out of the bytes rather than taken from the source name.
func copySticker(src, dataDir, key string) (string, int64, error) {
	b, err := os.ReadFile(src)
	if err != nil {
		return "", 0, err
	}
	dir := filepath.Join(dataDir, stickerSubdir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", 0, err
	}
	name := key + stickerExt(b)
	rel, dst := filepath.Join(stickerSubdir, name), filepath.Join(dir, name)
	// The same sticker is due on every message that carries it, and a key
	// names one unchanging picture, so a copy already here is that picture.
	if st, err := os.Stat(dst); err == nil {
		return rel, st.Size(), nil
	}
	// Written aside and renamed: the TUI is another process, and it may be
	// decoding this very file for a message that already has it.
	tmp := filepath.Join(dir, ".tmp-"+key)
	defer os.Remove(tmp)
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return "", 0, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return "", 0, err
	}
	return rel, int64(len(b)), nil
}

// stickerExt names the format of the bytes, so a consumer holding nothing but
// the path knows what it has. An unrecognized picture keeps the bare key,
// which is what the client itself stores it as.
func stickerExt(b []byte) string {
	switch http.DetectContentType(b) {
	case "image/gif":
		return ".gif"
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	}
	return ""
}
