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
	// while its window has focus. Milliseconds rather than a duration string
	// because the unit belongs in the key's name once a reader retunes the
	// value by hand.
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
	MarkRead    MarkRead      `yaml:"mark_read"`
	Resources   Resources     `yaml:"resources"`
	AI          AI            `yaml:"ai"`
	Todoist     Todoist       `yaml:"todoist"`
	// Silence keeps matching messages out of the unread badge and out of the
	// chat list's ordering; see docs/silence/PRD.md.
	Silence store.SilenceRules `yaml:"silence"`
	// SilenceSync settles the server-side read watermark past silenced
	// messages, so the Feishu clients' own dots follow the same rules. It is
	// one-way: dropping a rule never re-lights a dot anywhere.
	SilenceSync bool `yaml:"silence_sync"`
}

// MarkRead is how the Feishu client's own red dot comes down: by posting the
// chat's read watermark to the web client with the Feishu login of Browser's
// cookie jar.
type MarkRead struct {
	// Browser names the cookie jar, as kooky registers it.
	Browser string `yaml:"browser"`
}

// Values is the fixed set a key takes, for the keys that have one, so
// completion offers the words the validators accept and nothing else. A key
// that takes any value answers nil.
func Values(key string) []string {
	switch key {
	case "mark_read.browser":
		return larkweb.Browsers
	case "silence_sync":
		return []string{"true", "false"}
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

// Validate refuses a browser the jar reader cannot open at startup.
func (m MarkRead) Validate() error {
	return ValidMarkReadBrowser(m.Browser)
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
	// Snippets replaces the assistant panel's built-in snippet offers whole,
	// when it is set. Each is a scripted question the panel inserts into its
	// box under a digit and behind the / popup.
	Snippets SnippetList `yaml:"snippets"`
	// History lets the agent read the chat's synced history itself, through
	// the read-only larkim commands the system prompt teaches and the tool
	// gate allows nothing else than. Off means no tools at all.
	History bool `yaml:"history"`
}

// SnippetList reads an empty list as unset, so a config that carries
// `snippets: []` keeps the built-ins — the offers are replaced by what is
// written, not by the fact of writing. The nil it normalizes to is also what
// keeps an unset list round-tripping equal to itself.
type SnippetList []AISnippet

// UnmarshalYAML decodes the list and normalizes an empty one to nil.
func (s *SnippetList) UnmarshalYAML(n *yaml.Node) error {
	var list []AISnippet
	if err := n.Decode(&list); err != nil {
		return err
	}
	if len(list) > 0 {
		*s = list
	}
	return nil
}

// AISnippet is one scripted question the assistant panel offers.
type AISnippet struct {
	Name string `yaml:"name"`
	Text string `yaml:"text"`
}

// Resources configures attachment downloads.
type Resources struct {
	// MaxBytes skips resources larger than this (0 = unlimited).
	MaxBytes int64 `yaml:"max_bytes"`
}

// Todoist configures the T key, which files the selected message (or the chat
// under the cursor) as a Todoist task: the message's first line as the task,
// a lark:// link back to it as the description. The token sits here rather
// than behind an env-var name, unlike jev_key_env: the local config file is
// gitignored and this is a single-user tool, and a pasted token is one less
// indirection to keep working.
type Todoist struct {
	// Token is the Todoist API token. Empty disables the T key.
	Token string `yaml:"token"`
	// ProjectID is the project tasks land in; empty is Todoist's Inbox.
	ProjectID string `yaml:"project_id"`
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
		MarkRead:          MarkRead{Browser: "chrome"},
		Resources:         Resources{MaxBytes: 50 << 20},
		AI: AI{Agent: "omp --mode acp", Model: "cursor/composer-2.5-fast", Context: 10,
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
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// Normalize puts a value into the shape the running code expects, so a key
// retuned mid-session passes through the same floors and expansions the file
// does rather than only the ones the decoder applies.
func (c *Config) Normalize() {
	c.DataDir = expandHome(c.DataDir)
	c.LarkCLIPath = expandHome(c.LarkCLIPath)
	// A tick's own work measures in hundreds of milliseconds, so the floor is
	// not a rate limit — it only keeps 0 from turning the loop into a busy
	// spin over lark-cli.
	c.PollIntervalMS = max(c.PollIntervalMS, minPollIntervalMS)
	// Every process loads the same file; the rules must come out in the same
	// order everywhere or the silence fingerprints disagree.
	c.Silence.Sort()
}

// Validate refuses a configuration no run could honour, including the rules
// that span two keys: :config judges a value through this before it writes
// it, so a pair the next start would refuse is refused where it is typed.
func (c Config) Validate() error {
	if err := c.Silence.Validate(); err != nil {
		return err
	}
	if err := c.MarkRead.Validate(); err != nil {
		return err
	}
	return nil
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
	v, ok := fieldAt(reflect.ValueOf(c), key)
	if !ok {
		return "", false
	}
	return formatValue(v)
}

// fieldAt walks a dotted key down through the nested sections of v.
func fieldAt(v reflect.Value, key string) (reflect.Value, bool) {
	for part := range strings.SplitSeq(key, ".") {
		f, ok := fieldByYAML(v, part)
		if !ok {
			return reflect.Value{}, false
		}
		v = f
	}
	return v, true
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
	fmt.Fprintf(&b, "%s%s: %s\n", strings.Repeat("  ", len(parts)-1), parts[len(parts)-1], yamlScalar(key, value))
	return b.String()
}

// yamlScalar is the YAML text a value for key is parsed from. An empty value
// for a string key is spelled "": bare, it is YAML's null, which Set reads as
// "leave the field as it was" and Load as "fall back to the default", so
// clearing the key would never take. For any other kind an empty value is
// nothing typed, and null is right.
func yamlScalar(key, value string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	if v, ok := fieldAt(reflect.ValueOf(Config{}), key); ok && v.Kind() == reflect.String {
		return `""`
	}
	return value
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
