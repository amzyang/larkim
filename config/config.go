// Package config loads ~/.larkim/config.yaml with defaults for every field.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/amzyang/larkim/applink"
	"github.com/amzyang/larkim/jev"
	"github.com/amzyang/larkim/larkweb"
	"github.com/amzyang/larkim/store"
	"gopkg.in/yaml.v3"
)

// Config is the user-editable configuration.
type Config struct {
	// DataDir holds the database, resources and lock; default ~/.larkim.
	DataDir string `yaml:"data_dir"`
	// LarkCLIPath is the lark-cli binary; empty tries /opt/homebrew/bin then $PATH.
	LarkCLIPath string `yaml:"lark_cli_path"`
	// PollIntervalMS is the pause in milliseconds between sweep ticks, and
	// discovery's pace; a TUI syncing in-process runs discovery back to back
	// while its window has focus. Milliseconds rather than a duration string for the same reason as
	// ApplinkPaceMS: the unit belongs in the key's name once a reader retunes
	// the value by hand.
	PollIntervalMS int `yaml:"poll_interval_ms"`
	// Overlap is how far each search window reaches behind the watermark to
	// absorb search-index latency (measured ≈10s).
	Overlap time.Duration `yaml:"overlap"`
	// BackfillDays bounds the initial history pull per chat.
	BackfillDays int `yaml:"backfill_days"`
	// ActiveTopK is how many most-active chats the slow path reconciles.
	ActiveTopK int `yaml:"active_top_k"`
	// ChatsRefreshEvery is the interval of the full chat listing.
	ChatsRefreshEvery time.Duration `yaml:"chats_refresh_every"`
	// SlowPathEvery is the interval of the active-chats reconciliation.
	SlowPathEvery time.Duration `yaml:"slow_path_every"`
	// RepairEvery is the interval of the 7-day edit/recall repair pass.
	RepairEvery time.Duration `yaml:"repair_every"`
	// ApplinkPaceMS is the gap in milliseconds the applink queue leaves
	// between two lark:// navigations. Milliseconds rather than a duration
	// string because this is the one key a reader retunes by hand, from the
	// TUI's :set and from --set, where 1500 beats "1500ms".
	ApplinkPaceMS int `yaml:"applink_pace_ms"`
	// MarkRead picks which lever drops the Feishu client's own red dot.
	MarkRead  MarkRead  `yaml:"mark_read"`
	Resources Resources `yaml:"resources"`
	AI        AI        `yaml:"ai"`
	// Silence keeps matching messages out of the unread badge and out of the
	// chat list's ordering; see docs/silence/PRD.md.
	Silence store.SilenceRules `yaml:"silence"`
}

// Mark-read modes.
const (
	// MarkReadApplink walks the desktop client onto the chat and lets it send
	// the receipt itself.
	MarkReadApplink = "applink"
	// MarkReadWeb posts the read watermark to the web client's gateway with
	// cookies borrowed from a browser.
	MarkReadWeb = "web"
)

// MarkRead configures how the Feishu client's own red dot comes down. Both
// modes take the chat as read locally the same way; they differ only in what
// they ask Feishu.
type MarkRead struct {
	// Mode is applink or web. applink drives the desktop client, so it needs
	// the client running and answers nothing about whether the dot fell; web
	// posts one request per chat and reports a status, but needs a browser
	// logged into Feishu.
	Mode string `yaml:"mode"`
	// Browser names the cookie jar web mode reads, as kooky registers it.
	Browser string `yaml:"browser"`
}

// ValidMarkReadMode names the accepted words, which YAML's own error would
// not: to the decoder any string is a string.
func ValidMarkReadMode(mode string) error {
	switch mode {
	case MarkReadApplink, MarkReadWeb:
		return nil
	}
	return fmt.Errorf("mark_read.mode: %q is not %s or %s", mode, MarkReadApplink, MarkReadWeb)
}

// Values is the fixed set a key takes, for the keys that have one, so
// completion offers the words the validators accept and nothing else. A key
// that takes any value answers nil.
func Values(key string) []string {
	switch key {
	case "mark_read.mode":
		return []string{MarkReadApplink, MarkReadWeb}
	case "mark_read.browser":
		return larkweb.Browsers
	}
	return nil
}

// ValidMarkReadBrowser names the jars web mode can read, so a browser it has
// no reader for is refused where it is typed rather than read as a browser
// with no Feishu login.
func ValidMarkReadBrowser(browser string) error {
	if slices.Contains(larkweb.Browsers, browser) {
		return nil
	}
	return fmt.Errorf("mark_read.browser: %q is not one of %s", browser, strings.Join(larkweb.Browsers, ", "))
}

// Validate refuses a mode nobody implements rather than falling back to one,
// so a typo is a message at startup instead of dots that never come down.
func (m MarkRead) Validate() error {
	if err := ValidMarkReadMode(m.Mode); err != nil {
		return err
	}
	if m.Mode == MarkReadWeb {
		return ValidMarkReadBrowser(m.Browser)
	}
	return nil
}

// AI configures the TUI assistant.
type AI struct {
	// Agent is the command that starts an ACP agent on stdio, split on
	// whitespace.
	Agent string `yaml:"agent"`
	// Model is the value of the agent's model option the assistant asks for;
	// empty keeps the agent's own default.
	Model string `yaml:"model"`
	// Context is how many recent messages are given to the assistant.
	Context int `yaml:"context"`
	// JevKeyEnv names the environment variable holding the TypeSafe API key,
	// which the reaction picker's contextual row is asked through. No key
	// there leaves the picker without the row.
	JevKeyEnv string `yaml:"jev_key_env"`
	// JevEndpoint is the evaluation endpoint that answers it.
	JevEndpoint string `yaml:"jev_endpoint"`
}

// Resources configures attachment downloads.
type Resources struct {
	// MaxBytes skips resources larger than this (0 = unlimited).
	MaxBytes int64 `yaml:"max_bytes"`
}

// minPollIntervalMS is the smallest pause LoadWith will hand the daemon.
const minPollIntervalMS = 100

// Default returns the built-in configuration.
func Default() Config {
	return Config{
		DataDir:           filepath.Join(homeDir(), ".larkim"),
		PollIntervalMS:    3000,
		Overlap:           2 * time.Minute,
		BackfillDays:      30,
		ActiveTopK:        30,
		ChatsRefreshEvery: 10 * time.Minute,
		SlowPathEvery:     10 * time.Minute,
		RepairEvery:       6 * time.Hour,
		ApplinkPaceMS:     applink.DefaultPaceMS,
		// Browser is filled even in applink mode, which does not read a jar, so
		// that switching modes is one key rather than two.
		MarkRead:  MarkRead{Mode: MarkReadApplink, Browser: "chrome"},
		Resources: Resources{MaxBytes: 50 << 20},
		AI: AI{Agent: "omp --mode acp", Model: "cursor/composer-2.5-fast", Context: 80,
			JevKeyEnv: "TYPESAFE_API_KEY", JevEndpoint: jev.DefaultEndpoint},
	}
}

// DefaultPath is where Load looks when no path is given.
func DefaultPath() string { return filepath.Join(homeDir(), ".larkim", "config.yaml") }

// Resolve is the absolute file Load reads for a given --config value, which
// callers need when they hand the path to a process with a working directory
// of its own.
func Resolve(path string) string {
	if path == "" {
		path = DefaultPath()
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

// Load reads path over the defaults; a missing file yields the defaults.
func Load(path string) (Config, error) { return LoadWith(path, nil) }

// LoadWith reads path over the defaults and then the sets over that. Each set
// is a key=value in the file's own vocabulary, so a key keeps whatever type
// and spelling it has there.
//
// The sets land before normalisation rather than on the returned Config, so
// the PollIntervalMS floor and the silence rules judge the value that will
// actually be used.
func LoadWith(path string, sets []string) (Config, error) {
	cfg := Default()
	path = Resolve(path)
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// A missing file is the default configuration, and --set still applies
		// to it: the flag is the way to run without writing one.
	case err != nil:
		return cfg, err
	default:
		if err := yaml.Unmarshal(b, &cfg); err != nil {
			return cfg, fmt.Errorf("%s: %w", path, err)
		}
	}
	if err := applySets(&cfg, sets); err != nil {
		return cfg, err
	}
	cfg.DataDir = expandHome(cfg.DataDir)
	cfg.LarkCLIPath = expandHome(cfg.LarkCLIPath)
	// A tick's own work measures in hundreds of milliseconds, so the floor is
	// not a rate limit — it only keeps 0 from turning the loop into a busy
	// spin over lark-cli.
	cfg.PollIntervalMS = max(cfg.PollIntervalMS, minPollIntervalMS)
	if err := cfg.Silence.Validate(); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	if err := cfg.MarkRead.Validate(); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

func applySets(cfg *Config, sets []string) error {
	for _, set := range sets {
		key, value, ok := strings.Cut(set, "=")
		if !ok {
			return fmt.Errorf("--set %s: want key=value", set)
		}
		if err := cfg.Set(key, value); err != nil {
			return fmt.Errorf("--set %s: %w", strings.TrimSpace(key), err)
		}
	}
	return nil
}

// Set decodes one key=value as a one-key YAML document over c, so a value is
// parsed by the same decoder the file goes through — 3s is a duration, ~/x
// expands, 1000 is a number — rather than by a second hand-written parser that
// would drift from it.
//
// KnownFields is on here and off for the file: a key named right now is a
// mistake the reader wants named, while a file may carry keys a past version
// knew.
func (c *Config) Set(key, value string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("want key=value")
	}
	dec := yaml.NewDecoder(strings.NewReader(setDoc(key, value)))
	dec.KnownFields(true)
	return dec.Decode(c)
}

// Get renders one key the way the file spells it, so what Get returns goes
// back through Set unchanged. The second result is false for a key Keys() does
// not name.
func (c Config) Get(key string) (string, bool) {
	v := reflect.ValueOf(c)
	for part := range strings.SplitSeq(key, ".") {
		f, ok := fieldByYAML(v, part)
		if !ok {
			return "", false
		}
		v = f
	}
	return formatValue(v)
}

// shortDuration spells a whole number of hours, minutes or seconds the way a
// reader writes it — 6h, not Go's 6h0m0s — so a value read out of the
// configuration and written back reads like one a hand put there.
func shortDuration(d time.Duration) string {
	switch {
	case d == 0:
		return "0s"
	case d%time.Hour == 0:
		return strconv.FormatInt(int64(d/time.Hour), 10) + "h"
	case d%time.Minute == 0:
		return strconv.FormatInt(int64(d/time.Minute), 10) + "m"
	case d%time.Second == 0:
		return strconv.FormatInt(int64(d/time.Second), 10) + "s"
	}
	return d.String()
}

func fieldByYAML(v reflect.Value, tag string) (reflect.Value, bool) {
	if v.Kind() != reflect.Struct {
		return reflect.Value{}, false
	}
	for f, fv := range v.Fields() {
		if name, _, _ := strings.Cut(f.Tag.Get("yaml"), ","); name == tag {
			return fv, true
		}
	}
	return reflect.Value{}, false
}

func formatValue(v reflect.Value) (string, bool) {
	if v.Type() == reflect.TypeFor[time.Duration]() {
		return shortDuration(time.Duration(v.Int())), true
	}
	switch v.Kind() {
	case reflect.String:
		return v.String(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10), true
	case reflect.Bool:
		return strconv.FormatBool(v.Bool()), true
	case reflect.Struct:
		// A section holds keys, not a value; Keys() does not name one either.
		return "", false
	}
	// Anything else — silence's list of rules — is spelled inline, which is the
	// one spelling a value both survives a single line and reads back.
	var n yaml.Node
	if err := n.Encode(v.Interface()); err != nil {
		return "", false
	}
	n.Style = yaml.FlowStyle
	b, err := yaml.Marshal(&n)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(b)), true
}

// setDoc nests a dotted key into the document shape the file uses, so
// resources.max_bytes reaches the same field the file's two lines do.
func setDoc(key, value string) string {
	parts := strings.Split(key, ".")
	var b strings.Builder
	for i, p := range parts[:len(parts)-1] {
		fmt.Fprintf(&b, "%s%s:\n", strings.Repeat("  ", i), p)
	}
	// The value goes in verbatim; quoting it here would turn every number and
	// duration into a string.
	fmt.Fprintf(&b, "%s%s: %s\n", strings.Repeat("  ", len(parts)-1), parts[len(parts)-1], value)
	return b.String()
}

// Keys names every key a file or a --set may carry, dotted through the nested
// sections, in declaration order. Completion offers this list.
func Keys() []string { return keysOf(reflect.TypeFor[Config](), "") }

func keysOf(t reflect.Type, prefix string) []string {
	var out []string
	for f := range t.Fields() {
		tag, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if tag == "" || tag == "-" {
			continue
		}
		key := prefix + tag
		// Only a struct has keys under it; a slice of them, like silence, is
		// one value written whole.
		if f.Type.Kind() == reflect.Struct {
			out = append(out, keysOf(f.Type, key+".")...)
			continue
		}
		out = append(out, key)
	}
	return out
}

// DBPath is the SQLite file inside DataDir.
func (c Config) DBPath() string { return filepath.Join(c.DataDir, "larkim.db") }

// ResourcesDir is where lark-cli downloads attachments (it appends lark-im-resources/).
func (c Config) ResourcesDir() string { return filepath.Join(c.DataDir, "resources") }

// LogPath is the diagnostic log inside DataDir.
func (c Config) LogPath() string { return filepath.Join(c.DataDir, "larkim.log") }

func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		return filepath.Join(homeDir(), rest)
	}
	return p
}
