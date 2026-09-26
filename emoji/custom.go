package emoji

import (
	"encoding/json"
	"errors"
	"fmt"
	"image"
	// A picture is added from whatever the clipboard or Finder had, so every
	// format macOS hands over has to decode here.
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/amzyang/larkim/fuzzy"
	"golang.org/x/image/draw"
)

// MaxCustomSide is the longest a custom emoji's picture may be. Feishu draws a
// pasted picture at its own pixel size, so one that came off a screenshot would
// land in the chat as a screenshot rather than as an emoji.
const MaxCustomSide = 240

// CustomDir is where the pictures a person adds themselves live.
//
// It is deliberately not Dir: Sync writes that directory whole, removing what
// it finds first, so a picture filed there would be cut away the next time
// this binary carries a new sheet.
func CustomDir(dataDir string) string { return filepath.Join(dataDir, "emoji-custom") }

// customIndex names the file the entries live in. It is a file rather than a
// table because the daemon owns every table in the database and never reads
// this, and because a picture and its entry are lost or kept together.
func customIndex(dataDir string) string { return filepath.Join(dataDir, "emoji-custom.json") }

// Custom is an emoji a person added: a picture, the names it answers to, and
// nothing Feishu has ever heard of. It reaches a chat as a picture, which is
// the one way a picture Feishu has no key for reaches anybody.
type Custom struct {
	Name    string   `json:"name"`
	Aliases []string `json:"aliases,omitempty"`
	// Terms is what a query is matched against: every name, its pinyin and its
	// pinyin initials.
	Terms []string `json:"terms"`
	// Picture is the file's name inside CustomDir, never a path: the data dir
	// moves, and an index naming absolute paths would not move with it.
	Picture string `json:"picture"`
}

// Key is how a custom emoji is named where Feishu's own keys are, kept apart
// by a prefix no emoji_type carries.
func (c Custom) Key() string { return "custom:" + c.Name }

// Path is the picture this entry names.
func (c Custom) Path(dataDir string) string { return filepath.Join(CustomDir(dataDir), c.Picture) }

// ErrBadName rejects a name that would reach outside CustomDir.
var ErrBadName = errors.New("a name cannot be empty or hold a path separator")

func checkName(name string) error {
	if name == "" || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return fmt.Errorf("%q: %w", name, ErrBadName)
	}
	return nil
}

// LoadCustom reads the entries, dropping any whose picture has gone. Deleting
// the picture is how one of these is deleted by hand, so an entry pointing at
// nothing is an answer rather than a fault.
func LoadCustom(dataDir string) ([]Custom, error) {
	b, err := os.ReadFile(customIndex(dataDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var all []Custom
	if err := json.Unmarshal(b, &all); err != nil {
		return nil, fmt.Errorf("%s: %w", customIndex(dataDir), err)
	}
	out := make([]Custom, 0, len(all))
	for _, c := range all {
		if _, err := os.Stat(c.Path(dataDir)); err == nil {
			out = append(out, c)
		}
	}
	return out, nil
}

// AddCustom stores source as the emoji called name, replacing one of the same
// name. The picture is re-encoded as PNG and held to MaxCustomSide.
func AddCustom(dataDir, name string, aliases []string, source string) (Custom, error) {
	if err := checkName(name); err != nil {
		return Custom{}, err
	}
	if err := os.MkdirAll(CustomDir(dataDir), 0o700); err != nil {
		return Custom{}, err
	}
	c := Custom{Name: name, Aliases: aliases, Terms: customTerms(name, aliases), Picture: name + ".png"}
	if err := writePicture(source, c.Path(dataDir)); err != nil {
		return Custom{}, err
	}
	all, err := LoadCustom(dataDir)
	if err != nil {
		return Custom{}, err
	}
	all = slices.DeleteFunc(all, func(x Custom) bool { return x.Name == name })
	return c, saveCustom(dataDir, append(all, c))
}

// RemoveCustom drops one entry and its picture.
func RemoveCustom(dataDir, name string) error {
	if err := checkName(name); err != nil {
		return err
	}
	all, err := LoadCustom(dataDir)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(all, func(x Custom) bool { return x.Name == name })
	if i < 0 {
		return fmt.Errorf("no custom emoji called %q", name)
	}
	if err := os.Remove(all[i].Path(dataDir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return saveCustom(dataDir, slices.Delete(all, i, i+1))
}

func saveCustom(dataDir string, all []Custom) error {
	b, err := json.MarshalIndent(all, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(customIndex(dataDir), append(b, '\n'), 0o600)
}

// customTerms is what a query reaches this emoji by: every name it answers to,
// each with its pinyin and pinyin initials.
func customTerms(name string, aliases []string) []string {
	var out []string
	for _, n := range append([]string{name}, aliases...) {
		for _, t := range fuzzy.Terms(n) {
			if t = strings.ToLower(t); t != "" && !slices.Contains(out, t) {
				out = append(out, t)
			}
		}
	}
	return out
}

// writePicture decodes whatever source holds and writes it out as a PNG no
// longer than MaxCustomSide on its longest side. A picture already small
// enough is re-encoded rather than copied, so the file is a PNG whatever came
// in — the clipboard hands over JPEG and TIFF as readily as PNG.
func writePicture(source, dest string) error {
	f, err := os.Open(source)
	if err != nil {
		return err
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		return fmt.Errorf("%s: %w", source, err)
	}
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	return png.Encode(out, fit(src, MaxCustomSide))
}

// fit scales an image down so neither side passes side, keeping its
// proportions and leaving a smaller one alone.
func fit(src image.Image, side int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= side && h <= side {
		return src
	}
	if w > h {
		w, h = side, side*h/b.Dx()
	} else {
		w, h = side*w/b.Dy(), side
	}
	dst := image.NewRGBA(image.Rect(0, 0, max(w, 1), max(h, 1)))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	return dst
}
