package tui

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/strikethrough"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
	"golang.org/x/net/html/charset"
)

// richConverter stops at the markdown classify promotes and Feishu's md tag
// renders; anything richer would only be escaped back out at send time. It is
// built once because the library builds one per call otherwise.
var richConverter = converter.NewConverter(converter.WithPlugins(
	base.NewBasePlugin(),
	commonmark.NewCommonmarkPlugin(),
	strikethrough.NewStrikethroughPlugin(),
	table.NewTablePlugin(),
))

// osaData unwraps the «data HTML…» literal osascript prints for a raw
// clipboard coercion. AppleScript has no other spelling for bytes on stdout.
func osaData(out string) ([]byte, error) {
	hexed, ok := strings.CutPrefix(strings.TrimSpace(out), "«data HTML")
	if !ok {
		return nil, fmt.Errorf("not an HTML data literal: %.24q", out)
	}
	hexed, ok = strings.CutSuffix(hexed, "»")
	if !ok {
		return nil, fmt.Errorf("unterminated HTML data literal: %.24q", out)
	}
	return hex.DecodeString(hexed)
}

// htmlMarkdown turns one clipboard HTML flavour into the markdown the composer
// speaks. Decoding through charset is the library's own contract: it leaves
// encoding to the caller, and what an app writes to the pasteboard is its own
// business.
func htmlMarkdown(raw string) (string, error) {
	data, err := osaData(raw)
	if err != nil {
		return "", fmt.Errorf("clipboard html: %w", err)
	}
	r, err := charset.NewReader(bytes.NewReader(data), "text/html")
	if err != nil {
		return "", fmt.Errorf("clipboard html charset: %w", err)
	}
	md, err := richConverter.ConvertReader(r)
	if err != nil {
		return "", fmt.Errorf("clipboard html: %w", err)
	}
	return strings.TrimSpace(string(md)), nil
}

// pickPaste answers which flavour goes into the draft. The HTML one is only
// worth its escaping when it carries formatting the draft would be sent as a
// post for: an editor that copies with syntax highlighting puts styled spans
// on the pasteboard for what is only code, and converting those turns an
// ordinary copy into a wall of backslashes. Asking classify is what keeps the
// answer the same one the badge gives.
func pickPaste(md, text string) string {
	if classify(md) == kindPost {
		return md
	}
	return text
}
