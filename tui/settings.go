package tui

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/markread"
	"github.com/amzyang/larkim/sync"
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
	// once at startup by the store or the client, so a change to one waits
	// for the next run — :config writes it, :set will not pretend to.
	live bool
	// sweep narrows live to the process that owns the sweep: the options are
	// read per tick, so a change reaches a loop running here, while a daemon
	// that holds daemon.lock keeps its own set and picks the file up itself.
	sweep bool
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
	// summary draws a value its cell should not show as spelled: too long, or
	// an id the session can name. Nil draws the value.
	summary func(Model, config.Config) string
	// pickProject opens the Todoist project chooser on enter in place of the
	// line editor: the value is an id, and nobody types one from memory.
	pickProject bool
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
	live:  true,
	sweep: true,
	check: positiveMS,
	apply: retuneSweep,
}, {
	key:   "overlap",
	help:  "how far each search window reaches behind the watermark",
	live:  true,
	sweep: true,
	apply: retuneSweep,
}, {
	key:  "backfill_days",
	help: "bound on the initial history pull per chat; it bounds the chats not yet backfilled, so lengthening it does not re-pull the ones already done",
}, {
	key:   "active_top_k",
	help:  "how many of the most active chats the slow path reconciles",
	live:  true,
	sweep: true,
	apply: retuneSweep,
}, {
	key:   "chats_refresh_every",
	help:  "interval of the full chat listing, per-chat mute settings included",
	live:  true,
	sweep: true,
	apply: retuneSweep,
}, {
	key:   "slow_path_every",
	help:  "interval of the active chats' reconciliation from their cursors",
	live:  true,
	sweep: true,
	apply: retuneSweep,
}, {
	key:   "repair_every",
	help:  "interval of the re-listing that lands edits and recalls",
	live:  true,
	sweep: true,
	apply: retuneSweep,
}, {
	key:   "mark_read.browser",
	help:  "the browser whose Feishu login clears Feishu's own red dots, e.g. chrome or edge",
	live:  true,
	check: config.ValidMarkReadBrowser,
	apply: rebuildClearBadge,
}, {
	key:  "resources.max_bytes",
	help: "skip attachments larger than this; 0 means unlimited. A raised cap does not reach the attachments already marked too large",
}, {
	key:   "ai.agent",
	help:  "the command that starts the ACP agent the assistant asks",
	live:  true,
	check: nonEmpty,
	apply: rebuildAI,
}, {
	key:   "ai.model",
	help:  "the model the agent is asked to use; empty for its default",
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
	key:      "ai.snippets",
	help:     "the assistant panel's snippet offers, name and text; enter edits them in the config file",
	readOnly: true,
	summary:  func(_ Model, c config.Config) string { return plural(len(c.AI.Snippets), "snippet", "snippets") },
}, {
	key:  "ai.history",
	help: "let the agent read this chat's synced history itself, through the read-only larkim commands the prompt teaches; anything else it runs stops the answer. Run the agent with --tools bash when on, --no-tools when off",
	live: true,
}, {
	key:   "todoist.token",
	help:  "the Todoist API token behind the T key; empty leaves the key answering that it is not configured",
	live:  true,
	apply: retokenTodoist,
}, {
	key:         "todoist.project",
	help:        "the project T files tasks into, picked from the account's own; empty is Todoist's Inbox",
	live:        true,
	apply:       rebuildTodoist,
	pickProject: true,
	summary:     func(m Model, c config.Config) string { return m.projectCell(c.Todoist.Project) },
}, {
	key:      "silence",
	help:     "rules whose messages carry no unread badge; enter edits them in the Silence tab",
	readOnly: true,
	summary:  func(_ Model, c config.Config) string { return plural(len(c.Silence), "rule", "rules") },
}, {
	key:   "silence_sync",
	help:  "settle Feishu's own read watermark past silenced messages, so every client's dot follows the same rules",
	live:  true,
	sweep: true,
	apply: rebuildSettle,
}}

// nonEmpty judges a key with no default to fall back on: an empty command
// starts nothing.
func nonEmpty(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("want a value")
	}
	return nil
}

// positiveMS judges a key whose unit is in its own name, so 3s is the reader
// writing it twice.
func positiveMS(raw string) error {
	if ms, err := strconv.Atoi(raw); err != nil || ms <= 0 {
		return fmt.Errorf("want a positive count of milliseconds, not %q", raw)
	}
	return nil
}

// rebuildAI builds the assistant again from the agent command and the model now
// in m.cfg, which is what makes a change to either reach the next question.
// Deps without a builder — a test that injected a fake — keeps what it was
// given.
func rebuildAI(m *Model) {
	if m.deps.NewAI == nil {
		return
	}
	m.ai = m.deps.NewAI(m.cfg.AI.Agent, m.cfg.AI.Model)
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

// rebuildClearBadge builds the badge clearer again from the browser now in
// m.cfg, so a :set reaches the next chat the queue fires rather than the next
// session. Deps without a builder keeps what it was given.
func rebuildClearBadge(m *Model) {
	if m.deps.NewClearBadge == nil {
		return
	}
	m.deps.ClearBadge = m.deps.NewClearBadge(m.cfg.MarkRead)
}

// rebuildTodoist builds the task filer again from the token and project now
// in m.cfg, so a token pasted into :config answers the next T rather than the
// next session. An empty token leaves the key reporting that it is not
// configured, which is what nil here means. Deps without a builder keeps what
// it was given.
func rebuildTodoist(m *Model) {
	if m.deps.NewTodoist == nil {
		return
	}
	m.deps.Todoist = m.deps.NewTodoist(m.cfg.Todoist.Token, m.cfg.Todoist.Project)
}

// retokenTodoist is rebuildTodoist for a new token, which also forgets the
// projects listed under the old one: they were another account's.
func retokenTodoist(m *Model) {
	m.todoistProjects = nil
	rebuildTodoist(m)
}

// retuneSweep hands the sweep the options m.cfg now spells. Every option is
// read where it is used rather than captured when the loop starts, so this
// reaches a tick already running; a pause already underway still waits itself
// out. A sweep owned by a daemon is another process's and is not reached from
// here at all, which is what the sweep flag says on the key.
func retuneSweep(m *Model) {
	if !m.deps.Embedded || m.deps.Syncer == nil {
		return
	}
	m.deps.Syncer.SetOptions(sync.OptionsFrom(m.cfg))
}

// rebuildSettle puts the silenced-unread settle lever on the sweep, or takes
// it off, as silence_sync now says. It is built through the same factory the
// badge clearer is.
func rebuildSettle(m *Model) {
	if !m.deps.Embedded || m.deps.Syncer == nil || m.deps.NewClearBadge == nil {
		return
	}
	var settle markread.Clear
	if m.cfg.SilenceSync {
		settle = m.deps.NewClearBadge(m.cfg.MarkRead)
	}
	m.deps.Syncer.SetSettleSilenced(settle)
}

func lookupSetting(key string) (setting, bool) {
	i := slices.IndexFunc(settings, func(s setting) bool { return s.key == key })
	if i < 0 {
		return setting{}, false
	}
	return settings[i], true
}

// settingValue is what this session is running for a key, spelled the way the
// config file spells it.
func (m Model) settingValue(s setting) string {
	v, _ := m.cfg.Get(s.key)
	return v
}

// isLive is whether a change to the key reaches this session. A sweep key
// only does where the sweep runs: a daemon holding daemon.lock keeps options
// of its own and reloads them from the file itself, so nothing typed here
// moves them.
func (m Model) isLive(s setting) bool {
	return s.live && (!s.sweep || (m.deps.Embedded && m.deps.Syncer != nil))
}

// settingReach is how far a change to the key goes.
func (m Model) settingReach(s setting) string {
	switch {
	case s.readOnly:
		return "Silence tab"
	case m.isLive(s):
		return "takes effect now"
	case s.sweep:
		return "the daemon rereads it"
	}
	return "next start"
}

// settingInfo is what the box beside the : line's list says about a key: what
// it is for, then what it is set to under cfg and how far a change reaches.
func (m Model) settingInfo(s setting) []string {
	value := m.settingCell(m.cfg, s)
	if value != "" {
		value += " · "
	}
	return infoLines(s.help, value+m.settingReach(s))
}

// settingCell is what the General tab draws for a key under cfg.
func (m Model) settingCell(cfg config.Config, s setting) string {
	if s.summary != nil {
		return s.summary(m, cfg)
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
	if err := cfg.Set(s.key, strings.TrimSpace(value)); err != nil {
		return cfg, err
	}
	// The same floors and cross-key rules the file goes through, so a value
	// this session runs on is one the next start would load, and a pair that
	// only breaks together is refused at the key that breaks it rather than at
	// the next start.
	cfg.Normalize()
	return cfg, cfg.Validate()
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
// It reaches only the keys a change takes effect mid-session on, this session
// included: a sweep key is out of reach where a daemon owns the sweep. A key
// the store or the client read once at startup would parse here and change
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
			if m.isLive(s) {
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
	if !ok || !m.isLive(s) {
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
