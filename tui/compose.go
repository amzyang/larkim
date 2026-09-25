package tui

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/amzyang/larkim/larkcli"
)

// draftKind is the Feishu message type a draft will be sent as. The composer
// resolves it on every keystroke and shows it, so the rules below never have
// to be guessed at from the outside.
type draftKind int

const (
	kindText draftKind = iota
	kindPost
	kindImage
)

func (k draftKind) msgType() string {
	switch k {
	case kindPost:
		return "post"
	case kindImage:
		return "image"
	default:
		return "text"
	}
}

var (
	// mdImage is a markdown image reference. The target is left unconstrained
	// because the composer accepts a local path as readily as a key, and the
	// angle-bracket form is what carries a path holding spaces — macOS names
	// every screenshot with them.
	mdImage = regexp.MustCompile(`!\[([^\]\n]*)\]\((?:<([^>\n]+)>|([^)\s]+))\)`)
	// loneImage is a draft that is one image and nothing else, which is the
	// only shape that becomes an image message rather than a post.
	loneImage = regexp.MustCompile(`^!\[([^\]\n]*)\]\((?:<([^>\n]+)>|([^)\s]+))\)$`)

	mdQuote      = regexp.MustCompile(`^ {0,3}> `)
	mdBullet     = regexp.MustCompile(`^ {0,3}[-*+] \S`)
	mdOrdered    = regexp.MustCompile(`^ {0,3}\d{1,9}[.)] \S`)
	mdRule       = regexp.MustCompile(`^ {0,3}(-{3,}|\*{3,}|_{3,})$`)
	mdTableRow   = regexp.MustCompile(`^ {0,3}\|.*\|`)
	mdTableSplit = regexp.MustCompile(`^ {0,3}\|?[\s:|-]*-[\s:|-]*\|?$`)
	mdFence      = regexp.MustCompile("^ {0,3}(```|~~~)")

	// Inline runs worth promoting to a post. Italics are deliberately absent:
	// `_` is everywhere in identifiers and `*` in CJK prose, and larkim's own
	// list would show the delimiters literally, so a false positive is visible.
	mdInline = regexp.MustCompile(`\[[^\]\n]*\]\([^)\s]+\)|\*\*[^*\n]+\*\*|~~[^~\n]+~~|` + "`[^`\n]+`")
)

// classify picks the message type a draft is sent as. It is pure so the badge
// and the send path cannot disagree about what is about to happen.
func classify(draft string) draftKind {
	draft = strings.TrimSpace(draft)
	if draft == "" {
		return kindText
	}
	if loneImage.MatchString(draft) {
		return kindImage
	}
	lines := strings.Split(draft, "\n")
	// A single-line list marker is the commonest thing a person types in a
	// chat ("- 好的", "1. 我来"), so a list only counts across several lines.
	multi := len(lines) > 1
	for i, line := range lines {
		// A fence is decisive on its own, and answering here is also what
		// keeps the markers inside one from being read as markdown.
		if mdFence.MatchString(line) {
			return kindPost
		}
		switch {
		case headingLine.MatchString(line), mdQuote.MatchString(line):
			return kindPost
		case multi && (mdBullet.MatchString(line) || mdOrdered.MatchString(line)):
			return kindPost
		case mdRule.MatchString(line) && i > 0 && strings.TrimSpace(lines[i-1]) != "":
			return kindPost
		case mdTableRow.MatchString(line) && i+1 < len(lines) && mdTableSplit.MatchString(lines[i+1]):
			return kindPost
		case mdImage.MatchString(line), mdInline.MatchString(line):
			return kindPost
		}
	}
	return kindText
}

// maxImageBytes is Feishu's own cap on a message image.
const maxImageBytes = 10 << 20

// draftImage is one image reference in a draft. Exactly one of local and url
// says where the picture comes from; both are empty when the draft named a key
// Feishu already holds, which is the only case with nothing to upload.
type draftImage struct {
	ref   string // the target as typed
	local string // the resolved absolute path
	url   string // the remote address, fetched at send time
	key   string // the img_local_<n> placeholder the bubble draws by
}

// draftPlan is everything the composer knows about the draft in it: the type
// it will be sent as, the body on the wire, the body the bubble draws, and the
// files that have to reach Feishu before either can be sent.
type draftPlan struct {
	kind   draftKind
	send   larkcli.Outgoing
	body   string
	images []draftImage
}

// draftFiles resolves the paths a draft names. Both fields are injected so a
// test never reads the real home or the real filesystem.
type draftFiles struct {
	Home string
	Stat func(string) (os.FileInfo, error)
}

func osDraftFiles() draftFiles {
	home, _ := os.UserHomeDir()
	return draftFiles{Home: home, Stat: os.Stat}
}

// planDraft is what the badge shows and what submit sends, so the two cannot
// describe different messages. A refused path comes back as an error with the
// plan resolved as far as it got, because the badge shows both.
func (f draftFiles) planDraft(draft string) (draftPlan, error) {
	draft = strings.TrimSpace(draft)
	p := draftPlan{kind: classify(draft), body: draft}

	var err error
	wire := mdImage.ReplaceAllStringFunc(draft, func(m string) string {
		g := mdImage.FindStringSubmatch(m)
		img, ferr := f.resolveImage(cmp.Or(g[2], g[3]), len(p.images))
		if ferr != nil && err == nil {
			err = ferr
		}
		p.images = append(p.images, img)
		// The bubble draws the placeholder; the wire gets the real key, which
		// the send path substitutes once the file is up.
		p.body = strings.Replace(p.body, m, "!["+g[1]+"]("+img.key+")", 1)
		return "![" + g[1] + "](" + img.key + ")"
	})
	if err != nil {
		return p, err
	}

	switch p.kind {
	case kindImage:
		// A lone image is an image message, which names its key and nothing
		// else; the bubble draws what lark-cli renders that back into.
		p.body = "[Image: " + p.images[0].key + "]"
		p.send = larkcli.Image(p.images[0].key)
	case kindPost:
		p.send = larkcli.Markdown(wire)
	default:
		p.send = larkcli.Text(draft)
	}
	return p, nil
}

// resolveImage turns one reference into something sendable: a key passes
// through, a path is expanded and checked. The checks run here rather than at
// send time so a mistyped path is refused while the draft can still be fixed.
func (f draftFiles) resolveImage(ref string, n int) (draftImage, error) {
	img := draftImage{ref: ref, key: fmt.Sprintf("img_local_%d", n)}
	// A key Feishu already holds needs no upload, so naming one is how the
	// same picture is sent twice without going up twice.
	if larkcli.IsImageKey(ref) {
		img.key = ref
		return img, nil
	}
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		// Nothing is fetched here: resolveImage runs on every keystroke, and
		// the network belongs in the send, beside the uploads.
		img.url = ref
		return img, nil
	}
	path := ref
	switch {
	case ref == "~" || strings.HasPrefix(ref, "~/"):
		if f.Home == "" {
			return img, fmt.Errorf("no home directory to expand %s against", ref)
		}
		path = filepath.Join(f.Home, strings.TrimPrefix(strings.TrimPrefix(ref, "~"), "/"))
	case strings.HasPrefix(ref, "~"):
		// Another person's home is not something a chat draft ever means.
		return img, fmt.Errorf("cannot expand %s", ref)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return img, fmt.Errorf("bad path: %s", ref)
	}
	st, err := f.Stat(abs)
	if err != nil {
		return img, fmt.Errorf("no such file: %s", ref)
	}
	if st.IsDir() {
		return img, fmt.Errorf("%s is a directory", ref)
	}
	if st.Size() > maxImageBytes {
		return img, fmt.Errorf("%s is %s, over the %s limit",
			filepath.Base(abs), humanBytes(st.Size()), humanBytes(maxImageBytes))
	}
	img.local = abs
	return img, nil
}

// uploads are the images that still have to reach Feishu.
func (p draftPlan) uploads() []draftImage {
	var out []draftImage
	for _, img := range p.images {
		if img.local != "" || img.url != "" {
			out = append(out, img)
		}
	}
	return out
}

// detail is what the badge says beside the type: the file a lone image names,
// or how many pictures a post is carrying.
func (p draftPlan) detail() string {
	switch {
	case p.kind == kindImage && p.images[0].local != "":
		return filepath.Base(p.images[0].local)
	case p.kind == kindImage && p.images[0].url != "":
		return p.images[0].url
	case len(p.uploads()) == 1:
		return "· 1 image"
	case len(p.uploads()) > 1:
		return fmt.Sprintf("· %d images", len(p.uploads()))
	}
	return ""
}

// imageRef is a draft's reference to a picture on disk. A path holding a space
// takes markdown's angle-bracket form, which is the only one that survives
// being read back: macOS names every screenshot with spaces in it.
func imageRef(path string) string {
	if strings.ContainsAny(path, " \t") {
		return "![](<" + path + ">)"
	}
	return "![](" + path + ")"
}
