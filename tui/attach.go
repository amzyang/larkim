package tui

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/store"
)

const (
	// clipGlyph opens a video's badge. It is the same mark a video file's card
	// carries, and not the camera a meeting invite opens with: a clip somebody
	// sent and a call to join are not the same thing to click.
	clipGlyph = "🎬"
	// voiceGlyph marks a voice message, which the client draws as a recording
	// rather than as a clip to watch.
	voiceGlyph = "🎤"
)

// attachment is the one file a media, audio or file message is. It is read
// from the body Feishu sent rather than from the text lark-cli renders that
// body into: the cover picture and the duration are in the body alone.
type attachment struct {
	msgType  string
	key      string
	name     string
	coverKey string // the frame the client shows a video as
	durMs    int64
}

// attachmentOf reads the file a message carries, and false for everything
// else — a post's embedded pictures hang off its text and are placed there.
func attachmentOf(msgType, contentRaw string) (attachment, bool) {
	switch msgType {
	case "file", "audio", "media", "video":
	default:
		return attachment{}, false
	}
	var body struct {
		FileKey  string `json:"file_key"`
		FileName string `json:"file_name"`
		ImageKey string `json:"image_key"`
		Duration int64  `json:"duration"`
	}
	if json.Unmarshal([]byte(contentRaw), &body) != nil || body.FileKey == "" {
		return attachment{}, false
	}
	return attachment{msgType: msgType, key: body.FileKey, name: flatten(body.FileName),
		coverKey: body.ImageKey, durMs: body.Duration}, true
}

// attachGist names an attachment on the one line a chat summary or a quote
// has: the kind it is, and the file's own name where that names anything —
// a clip's is a serial number, which says less than the kind already did.
func attachGist(a attachment) string {
	if a.clip() || a.name == "" {
		return msgTypeLabel(a.msgType)
	}
	return msgTypeLabel(a.msgType) + " " + a.name
}

// clip reports whether the attachment is something that plays, which is drawn
// by its length rather than by its name.
func (a attachment) clip() bool {
	return a.msgType == "media" || a.msgType == "video" || a.msgType == "audio"
}

// attachRows card the file a message is, the way the client draws one: a
// video as its cover picture under the length it runs, a voice message as
// that length alone, and any other file as its name beside its size. What the
// card draws opens the downloaded file, so a click reaches the video the same
// way it reaches a call's join button.
func attachRows(a attachment, x store.Message, idx int, st msgStyle, g *leads) []msgRow {
	r := attachRes(a.key, x, st)
	open := ""
	if r.Status == "done" {
		open = dataPath(st.dataDir, r.LocalPath)
	}
	zone := func(x0, w int) clickZone {
		if open == "" {
			return clickZone{}
		}
		return clickZone{x0: x0, x1: x0 + w, url: open, note: "opening " + filepath.Base(open)}
	}
	line := func(s string) msgRow {
		row := msgRow{lead: g.take(), text: s, idx: idx}
		row.zone = zone(row.lead.cols(), lipgloss.Width(s))
		return row
	}

	var rows []msgRow
	if pic := placePicture(a.coverKey, x, st, st.inner()); pic.cols > 0 {
		for _, row := range picRows(pic, idx, g) {
			row.zone = zone(row.lead.cols(), pic.cols)
			rows = append(rows, row)
		}
	}
	if a.clip() {
		glyph := clipGlyph
		if a.msgType == "audio" {
			glyph = voiceGlyph
		}
		return append(rows, line(glyph+stDim.Render(" "+clipLength(a.durMs))))
	}

	name := a.name
	if name == "" {
		name = a.key
	}
	size := ""
	if r.SizeBytes > 0 {
		size = stDim.Render(humanBytes(r.SizeBytes))
	}
	head := fileGlyph(name) + " "
	style := stBold
	if open != "" {
		style = stLink
	}
	room := st.inner() - lipgloss.Width(head) - lipgloss.Width(size) - 1
	return append(rows, line(padBetween(head+style.Render(truncate(name, room)), size, st.inner())))
}

// clipLength spells how long a recording runs the way a player's badge does:
// mm:ss, and h:mm:ss once it passes the hour. Feishu sends milliseconds, and
// the tail is rounded up so a 25.05s clip does not read as a second shorter
// than the client says it is.
func clipLength(ms int64) string {
	if ms <= 0 {
		return "--:--"
	}
	s := (ms + 999) / 1000
	if h := s / 3600; h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, s%3600/60, s%60)
	}
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

// fileGlyph marks a file by the family its name puts it in, which is what the
// client's icon says at a glance. Every glyph here is a wide character, so a
// card's name starts in the same column whichever family it is.
func fileGlyph(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".zip", ".tar", ".gz", ".tgz", ".rar", ".7z", ".apk", ".dmg", ".pkg", ".jar":
		return "📦"
	case ".mp4", ".mov", ".mkv", ".avi", ".webm":
		return clipGlyph
	case ".mp3", ".wav", ".m4a", ".flac", ".aac":
		return "🎵"
	case ".xls", ".xlsx", ".csv":
		return "📊"
	case ".pdf":
		return "📕"
	default:
		return "📄"
	}
}

// attachRes is the download row of one of a message's attachment keys. The
// zero value is a key the store has never registered; a row that is not done
// still carries the size a skipped attachment was refused for.
func attachRes(key string, x store.Message, st msgStyle) store.Resource {
	for _, r := range st.res[x.MessageID] {
		if r.FileKey == key {
			return r
		}
	}
	return store.Resource{}
}

// dataPath resolves a stored attachment path against the data dir. An empty
// data dir leaves nothing to open: a relative path handed to the opener would
// be read against whatever directory the TUI happens to run in.
func dataPath(dataDir, local string) string {
	switch {
	case local == "":
		return ""
	case filepath.IsAbs(local):
		return local
	case dataDir == "":
		return ""
	}
	return filepath.Join(dataDir, local)
}
