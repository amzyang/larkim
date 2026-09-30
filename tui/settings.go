package tui

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/amzyang/larkim/config"
)

// setting is one key of the configuration as the TUI presents it: the line of
// prose the config panel shows under the cursor, and how far a change to it
// reaches.
//
// Names are the config file's own, so the vocabulary of the file, of --set, of
// :set and of :config is one.
type setting struct {
	key  string
	help string
	// live marks a key a change reaches this session on. The rest are read
	// once at startup by the store or the syncer, so a change to one waits for
	// the next run — :config writes it, :set will not pretend to.
	live bool
	// check judges what the reader typed ahead of the decoder, for a key whose
	// spelling is easy to get wrong in a way yaml's own message would not
	// name. Nil leaves the decoder as the only judge.
	check func(raw string) error
	// apply carries a change past m.cfg, for what something outside it holds.
	// Nil where reading m.cfg is enough.
	apply func(*Model)
	// readOnly marks a value no single line can carry. The General tab shows
	// it and hands it to the tab that edits it whole; no single-line edit or
	// reset reaches it.
	readOnly bool
	// summary draws a value too long for its cell. Nil draws the value.
	summary func(config.Config) string
}

// settings names every key of the configuration, in config.Keys() order, which
// TestSettings_NameEveryConfigKeyInOrder holds them to.
var settings = []setting{{
	key:  "data_dir",
	help: "the database, attachments and the daemon lock live here",
}, {
	key:  "lark_cli_path",
	help: "the lark-cli binary; empty tries /opt/homebrew/bin, then $PATH",
}, {
	key:   "poll_interval_ms",
	help:  "pause between sweep ticks, and between discovery cycles while larkim is unfocused, in milliseconds; under 100 is raised to 100",
	check: positiveMS,
}, {
	key:  "overlap",
	help: "how far each search window reaches behind the watermark",
}, {
	key:  "backfill_days",
	help: "bound on the initial history pull per chat",
}, {
	key:  "active_top_k",
	help: "how many of the most active chats the slow path reconciles",
}, {
	key:  "chats_refresh_every",
	help: "interval of the full chat listing, per-chat mute settings included",
}, {
	key:  "slow_path_every",
	help: "interval of the active chats' reconciliation from their cursors",
}, {
	key:  "repair_every",
	help: "interval of the re-listing that lands edits and recalls",
}, {
	key:   "applink_pace_ms",
	help:  "gap between two Feishu navigations, in milliseconds",
	live:  true,
	check: positiveMS,
}, {
	key:  "resources.max_bytes",
	help: "skip attachments larger than this; 0 means unlimited",
}, {
	key:   "ai.model",
	help:  "the Claude model the assistant talks to",
	live:  true,
	apply: rebuildAI,
}, {
	key:   "ai.api_key_env",
	help:  "environment variable holding the Anthropic API key",
	live:  true,
	apply: rebuildAI,
}, {
	key:  "ai.context",
	help: "recent messages handed to the assistant, 0 for all of them",
	live: true,
}, {
	key:   "ai.jev_key_env",
	help:  "environment variable holding the TypeSafe API key",
	live:  true,
	apply: rebuildSuggest,
}, {
	key:   "ai.jev_endpoint",
	help:  "the evaluation endpoint the reaction suggestions are asked of",
	live:  true,
	apply: rebuildSuggest,
}, {
	key:      "silence",
	help:     "rules whose messages carry no unread badge; enter edits them in the Silence tab",
	readOnly: true,
	summary:  func(c config.Config) string { return plural(len(c.Silence), "rule", "rules") },
}}

// positiveMS judges a key whose unit is in its own name, so 3s is the reader
// writing it twice. A gap of nothing is the bug the pacing exists to fix.
func positiveMS(raw string) error {
	if ms, err := strconv.Atoi(raw); err != nil || ms <= 0 {
		return fmt.Errorf("want a positive count of milliseconds, not %q", raw)
	}
	return nil
}

// rebuildAI builds the assistant again from the model and the key variable now
// in m.cfg, which is what makes a change to either reach the next question.
// Deps without a builder — a test that injected a fake — keeps what it was
// given.
func rebuildAI(m *Model) {
	if m.deps.NewAI == nil {
		return
	}
	m.ai = m.deps.NewAI(m.cfg.AI.Model, m.cfg.AI.APIKeyEnv)
}

// rebuildSuggest builds the reaction suggester again from the key variable and
// the endpoint now in m.cfg. Deps without a builder — a test that injected a
// fake — keeps what it was given.
func rebuildSuggest(m *Model) {
	if m.deps.NewSuggest == nil {
		return
	}
	m.suggester = m.deps.NewSuggest(m.cfg.AI.JevKeyEnv, m.cfg.AI.JevEndpoint)
}

func lookupSetting(key string) (setting, bool) {
	i := slices.IndexFunc(settings, func(s setting) bool { return s.key == key })
	if i < 0 {
		return setting{}, false
	}
	return settings[i], true
}

// applinkPace is the gap the applink queue leaves between two navigations. The
// queue re-arms per chat, so a change reaches a chain already running.
func (m Model) applinkPace() time.Duration {
	return time.Duration(m.cfg.ApplinkPaceMS) * time.Millisecond
}

// settingValue is what this session is running for a key, spelled the way the
// config file spells it.
func (m Model) settingValue(s setting) string {
	v, _ := m.cfg.Get(s.key)
	return v
}

// settingCell is what the General tab draws for a key under cfg.
func settingCell(cfg config.Config, s setting) string {
	if s.summary != nil {
		return s.summary(cfg)
	}
	v, _ := cfg.Get(s.key)
	return v
}

// nextValue judges a value and returns the configuration it would make. cfg
// is taken by value, so a refused value leaves the caller's own untouched and
// :config can ask what a value means before it writes the file.
func nextValue(cfg config.Config, s setting, value string) (config.Config, error) {
	if s.readOnly {
		return cfg, errors.New("edit it in the Silence tab")
	}
	if s.check != nil {
		if err := s.check(value); err != nil {
			return cfg, err
		}
	}
	err := cfg.Set(s.key, strings.TrimSpace(value))
	return cfg, err
}

// setValue puts a value in the session's configuration without writing the
// file. :set stops here, which is how a value is tried before it is kept.
func setValue(m *Model, s setting, value string) error {
	cfg, err := nextValue(m.cfg, s, value)
	if err != nil {
		return err
	}
	m.cfg = cfg
	if s.apply != nil {
		s.apply(m)
	}
	return nil
}

// runSet reads or retunes one option, in vim's four forms: a bare :set lists
// them, name? reports one, name& restores its default and name=value sets it.
//
// It reaches only the keys a change takes effect mid-session on. A key the
// store or the syncer read once at startup would parse here and change
// nothing, and an option that answers a reader's keystroke with silence is
// worse than no option; :config is where every key is reachable, because what
// it writes is what the next run reads.
//
// A change made here lasts the session and leaves the file alone, which is how
// a value is tried before it is kept.
func (m Model) runSet(rest string) Model {
	if rest == "" {
		var parts []string
		for _, s := range settings {
			if s.live {
				parts = append(parts, s.key+"="+m.settingValue(s))
			}
		}
		return m.notify(strings.Join(parts, "  "), false)
	}
	name, value, assigned := strings.Cut(rest, "=")
	name = strings.TrimSpace(name)
	query := strings.HasSuffix(name, "?")
	reset := strings.HasSuffix(name, "&")
	if query || reset {
		name = name[:len(name)-1]
	}
	s, ok := lookupSetting(name)
	if !ok || !s.live {
		return m.notify("unknown option "+name, true)
	}
	switch {
	case query:
		return m.notify(s.key+"="+m.settingValue(s), false)
	case reset:
		value, _ = config.Default().Get(s.key)
	case !assigned:
		// A bare name is vim's way of turning a boolean on. Every option here
		// takes a value, so the reader meant to ask rather than to set.
		return m.notify(s.key+"="+m.settingValue(s), false)
	}
	if err := setValue(&m, s, value); err != nil {
		return m.notify(s.key+": "+err.Error(), true)
	}
	return m.notify(s.key+"="+m.settingValue(s), false)
}
