// Package config loads ~/.larkim/config.yaml with defaults for every field.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/amzyang/larkim/applink"
	"github.com/amzyang/larkim/store"
	"gopkg.in/yaml.v3"
)

// Config is the user-editable configuration.
type Config struct {
	// DataDir holds the database, resources and lock; default ~/.larkim.
	DataDir string `yaml:"data_dir"`
	// LarkCLIPath is the lark-cli binary; empty tries /opt/homebrew/bin then $PATH.
	LarkCLIPath string `yaml:"lark_cli_path"`
	// PollInterval is the pause between daemon ticks.
	PollInterval time.Duration `yaml:"poll_interval"`
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
	ApplinkPaceMS int       `yaml:"applink_pace_ms"`
	Resources     Resources `yaml:"resources"`
	AI            AI        `yaml:"ai"`
	// Silence keeps matching messages out of the unread badge and out of the
	// chat list's ordering; see docs/silence/PRD.md.
	Silence store.SilenceRules `yaml:"silence"`
}

// AI configures the TUI assistant.
type AI struct {
	// Model is the Claude model id.
	Model string `yaml:"model"`
	// APIKeyEnv names the environment variable holding the Anthropic API key.
	APIKeyEnv string `yaml:"api_key_env"`
	// Context is how many recent messages are given to the assistant.
	Context int `yaml:"context"`
}

// Resources configures attachment downloads.
type Resources struct {
	// MaxBytes skips resources larger than this (0 = unlimited).
	MaxBytes int64 `yaml:"max_bytes"`
}

// Default returns the built-in configuration.
func Default() Config {
	return Config{
		DataDir:           filepath.Join(homeDir(), ".larkim"),
		PollInterval:      3 * time.Second,
		Overlap:           2 * time.Minute,
		BackfillDays:      30,
		ActiveTopK:        30,
		ChatsRefreshEvery: 10 * time.Minute,
		SlowPathEvery:     10 * time.Minute,
		RepairEvery:       6 * time.Hour,
		ApplinkPaceMS:     applink.DefaultPaceMS,
		Resources:         Resources{MaxBytes: 50 << 20},
		AI:                AI{Model: "claude-opus-5", APIKeyEnv: "ANTHROPIC_API_KEY", Context: 80},
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
// the PollInterval floor and the silence rules judge the value that will
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
	if cfg.PollInterval < time.Second {
		cfg.PollInterval = time.Second
	}
	if err := cfg.Silence.Validate(); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// applySets decodes each key=value as a one-key YAML document over cfg, so a
// value is parsed by the same decoder the file goes through — 3s is a
// duration, ~/x expands, 1000 is a number — rather than by a second
// hand-written parser that would drift from it.
//
// KnownFields is on here and off for the file: a mistyped flag is a mistake
// the reader is making right now and wants named, while a file may carry keys
// a past version knew.
func applySets(cfg *Config, sets []string) error {
	for _, set := range sets {
		key, value, ok := strings.Cut(set, "=")
		if !ok {
			return fmt.Errorf("--set %s: want key=value", set)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return fmt.Errorf("--set %s: want key=value", set)
		}
		dec := yaml.NewDecoder(strings.NewReader(setDoc(key, value)))
		dec.KnownFields(true)
		if err := dec.Decode(cfg); err != nil {
			return fmt.Errorf("--set %s: %w", key, err)
		}
	}
	return nil
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
	for f := range t.NumField() {
		tag, _, _ := strings.Cut(t.Field(f).Tag.Get("yaml"), ",")
		if tag == "" || tag == "-" {
			continue
		}
		key := prefix + tag
		// Only a struct has keys under it; a slice of them, like silence, is
		// one value written whole.
		if ft := t.Field(f).Type; ft.Kind() == reflect.Struct && ft != reflect.TypeFor[time.Time]() {
			out = append(out, keysOf(ft, key+".")...)
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
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(homeDir(), p[2:])
	}
	return p
}
