// Package config loads ~/.larkim/config.yaml with defaults for every field.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	Resources   Resources     `yaml:"resources"`
	AI          AI            `yaml:"ai"`
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
func Load(path string) (Config, error) {
	cfg := Default()
	path = Resolve(path)
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
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

// DBPath is the SQLite file inside DataDir.
func (c Config) DBPath() string { return filepath.Join(c.DataDir, "larkim.db") }

// ResourcesDir is where lark-cli downloads attachments (it appends lark-im-resources/).
func (c Config) ResourcesDir() string { return filepath.Join(c.DataDir, "resources") }

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
