package sync

import (
	"cmp"
	"encoding/json"
	"fmt"
	"strings"
)

// attachBody is what every attachment message's body holds; each type reads
// the fields it uses and leaves the rest zero.
type attachBody struct {
	FileKey  string  `json:"file_key"`
	FileName string  `json:"file_name"`
	ImageKey string  `json:"image_key"`
	Duration float64 `json:"duration"`
}

// attachText renders an attachment message. The shapes are lark-cli's, to the
// character: gistBody, splitImages and the resource scan all read a rendering
// back by matching them, so a spelling of larkim's own would go unread.
func attachText(msgType, contentRaw string) string {
	var b attachBody
	if json.Unmarshal([]byte(contentRaw), &b) != nil {
		return fmt.Sprintf("[Invalid %s JSON]", cmp.Or(map[string]string{"media": "video"}[msgType], msgType))
	}
	switch msgType {
	case "image":
		if b.ImageKey == "" {
			return "[Image]"
		}
		return fmt.Sprintf("[Image: %s]", b.ImageKey)
	case "file":
		if b.FileKey == "" {
			return "[File]"
		}
		return fmt.Sprintf(`<file key="%s" name="%s"/>`, attrEscape(b.FileKey), attrEscape(cmp.Or(b.FileName, b.FileKey)))
	case "audio":
		if b.FileKey == "" {
			if b.Duration > 0 {
				return fmt.Sprintf("[Voice: %s]", seconds(b.Duration))
			}
			return "[Voice]"
		}
		return `<audio key="` + attrEscape(b.FileKey) + `"` + durationAttr(b.Duration) + "/>"
	}
	if b.FileKey == "" {
		return "[Video]"
	}
	out := fmt.Sprintf(`<video key="%s" name="%s"`, attrEscape(b.FileKey), attrEscape(cmp.Or(b.FileName, b.FileKey)))
	out += durationAttr(b.Duration)
	if b.ImageKey != "" {
		out += fmt.Sprintf(` cover_image_key="%s"`, attrEscape(b.ImageKey))
	}
	return out + "/>"
}

// LocalAttachment reports the msg_types attachText renders.
func LocalAttachment(msgType string) bool {
	switch msgType {
	case "image", "file", "audio", "media", "video":
		return true
	}
	return false
}

func durationAttr(ms float64) string {
	if ms <= 0 {
		return ""
	}
	return ` duration="` + seconds(ms) + `"`
}

// seconds is the duration as the rendering spells it, rounded to the nearest
// second: a 6961ms clip reads "7s".
func seconds(ms float64) string { return fmt.Sprintf("%.0fs", ms/1000) }

// attrEscape is the inverse of the unescaping a reader of these tags does.
var attrEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`)

func attrEscape(s string) string { return attrEscaper.Replace(s) }
