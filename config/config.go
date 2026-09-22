// Package config loads ~/.larkim/config.yaml with defaults for every field.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

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
		PollInterval:      10 * time.Second,
		Overlap:           2 * time.Minute,
		BackfillDays:      30,
		ActiveTopK:        30,
		ChatsRefreshEvery: 10 * time.Minute,
		SlowPathEvery:     10 * time.Minute,
		RepairEvery:       6 * time.Hour,
		Resources:         Resources{MaxBytes: 50 << 20},
	}
}

// DefaultPath is where Load looks when no path is given.
func DefaultPath() string { return filepath.Join(homeDir(), ".larkim", "config.yaml") }

// Load reads path over the defaults; a missing file yields the defaults.
func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		path = DefaultPath()
	}
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
	if len(p) >= 2 && p[:2] == "~/" {
		return filepath.Join(homeDir(), p[2:])
	}
	return p
}
