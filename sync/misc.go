package sync

import (
	"cmp"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The renderings here are lark-cli's, to the character, for the same reason
// the attachment and rich-text ones are: a reading of a rendering matches
// these shapes, so a spelling of larkim's own would go unread.

// miscBody is what the one-line message types carry; each reads the fields it
// uses and leaves the rest zero.
type miscBody struct {
	ChatID   string `json:"chat_id"`
	UserID   string `json:"user_id"`
	Name     string `json:"name"`
	FileKey  string `json:"file_key"`
	FileName string `json:"file_name"`
	Text     string `json:"text"`
}

// voteBody is a poll: what was asked, what could be answered, and whether it
// is still open. Feishu spells open as zero.
type voteBody struct {
	Topic   string   `json:"topic"`
	Options []string `json:"options"`
	Status  int      `json:"status"`
}

// todoBody is a task card. Its summary's content is rich text, in the same
// paragraph shape a post body uses.
type todoBody struct {
	TaskID  string `json:"task_id"`
	Summary struct {
		Title   string       `json:"title"`
		Content [][]PostElem `json:"content"`
	} `json:"summary"`
	DueTime string `json:"due_time"`
}

// LocalMisc reports the msg_types miscText renders.
func LocalMisc(msgType string) bool {
	switch msgType {
	case "share_chat", "share_user", "location", "folder", "vote", "hongbao", "todo":
		return true
	}
	return false
}

// miscText renders the message types whose whole body is a handful of fields.
func miscText(msgType, contentRaw string) string {
	switch msgType {
	case "share_chat":
		b, bad := oneLiner(contentRaw, "chat card")
		return cmp.Or(bad, labelled("Chat card", b.ChatID))
	case "share_user":
		b, bad := oneLiner(contentRaw, "user card")
		return cmp.Or(bad, labelled("User card", b.UserID))
	case "location":
		b, bad := oneLiner(contentRaw, "location")
		return cmp.Or(bad, labelled("Location", b.Name))
	case "folder":
		b, bad := oneLiner(contentRaw, "folder")
		return cmp.Or(bad, folderText(b))
	case "hongbao":
		b, bad := oneLiner(contentRaw, "hongbao")
		return cmp.Or(bad, redPacketText(b))
	case "vote":
		return voteText(contentRaw)
	case "todo":
		return todoText(contentRaw, time.Local)
	}
	// LocalMisc and this switch are the same list read twice, so a type in one
	// and not the other names itself rather than arriving as another's card.
	return "[" + msgType + "]"
}

// oneLiner reads the body the single-field types share, handing back the
// placeholder to render instead when it will not parse.
func oneLiner(contentRaw, kind string) (miscBody, string) {
	var b miscBody
	if json.Unmarshal([]byte(contentRaw), &b) != nil {
		return b, invalidBody(kind)
	}
	return b, ""
}

// invalidBody is what a body larkim cannot read renders as, named by the kind
// of message it should have been.
func invalidBody(kind string) string { return "[Invalid " + kind + " JSON]" }

// labelled is a card that renders to its label, carrying the id of the thing
// it shares when the body names one.
func labelled(label, id string) string {
	if id == "" {
		return "[" + label + "]"
	}
	return "[" + label + ": " + id + "]"
}

// redPacketText renders a red packet's greeting. The quoting is not the
// attribute escaping the other tags use: lark-cli writes this one with %q,
// which also escapes the non-ASCII a greeting is usually written in.
func redPacketText(b miscBody) string {
	if b.Text == "" {
		return "<hongbao/>"
	}
	return fmt.Sprintf("<hongbao text=%q/>", b.Text)
}

// folderText renders a shared folder as the single line lark-cli falls back to
// when it cannot expand one. larkim never expands: the children need a call of
// their own, and a post's attachment zone already writes folders this way.
func folderText(b miscBody) string {
	if b.FileKey == "" {
		return "[Folder]"
	}
	if b.FileName == "" {
		return fmt.Sprintf(`<folder key="%s"/>`, attrEscape(b.FileKey))
	}
	return fmt.Sprintf(`<folder key="%s" name="%s"/>`, attrEscape(b.FileKey), attrEscape(b.FileName))
}

func voteText(contentRaw string) string {
	var b voteBody
	if json.Unmarshal([]byte(contentRaw), &b) != nil {
		return invalidBody("vote")
	}
	var lines []string
	if b.Topic != "" {
		lines = append(lines, b.Topic)
	}
	for _, o := range b.Options {
		if o != "" {
			lines = append(lines, "• "+o)
		}
	}
	if b.Status != 0 {
		lines = append(lines, "(Closed)")
	}
	return "<vote>\n" + xmlEscape(cmp.Or(strings.Join(lines, "\n"), "vote")) + "\n</vote>"
}

// todoText renders a task card. loc dates the deadline, which is the only
// wall-clock reading in it.
func todoText(contentRaw string, loc *time.Location) string {
	var b todoBody
	if json.Unmarshal([]byte(contentRaw), &b) != nil {
		return invalidBody("todo")
	}
	var lines []string
	if b.Summary.Title != "" {
		lines = append(lines, b.Summary.Title)
	}
	for _, para := range b.Summary.Content {
		var line strings.Builder
		for _, el := range para {
			line.WriteString(postElemText(el))
		}
		if line.Len() > 0 {
			lines = append(lines, line.String())
		}
	}
	if due := dueStamp(b.DueTime, loc); due != "" {
		lines = append(lines, "Due: "+due)
	}
	var attr string
	if b.TaskID != "" {
		attr = fmt.Sprintf(` task_id="%s"`, attrEscape(b.TaskID))
	}
	return "<todo" + attr + ">\n" + xmlEscape(cmp.Or(strings.Join(lines, "\n"), "todo")) + "\n</todo>"
}

// dueStamp dates a deadline. Feishu sends it as digits in a string, in seconds
// or in milliseconds with no field to tell them apart, so the count of digits
// is what decides: 13 of them is a millisecond reading well past 2001.
func dueStamp(ts string, loc *time.Location) string {
	n, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || n == 0 {
		return ""
	}
	if len(strings.TrimLeft(ts, "+-")) >= 13 {
		n /= 1000
	}
	return time.Unix(n, 0).In(loc).Format("2006-01-02 15:04:05")
}

// xmlEscape is the body escaping the tags that wrap several lines use, where
// an unescaped angle bracket would read as a tag of its own.
var xmlBodyEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func xmlEscape(s string) string { return xmlBodyEscaper.Replace(s) }
