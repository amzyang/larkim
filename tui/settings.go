package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/amzyang/larkim/applink"
)

// setting is one option :set reads and writes. The registry holds only what
// takes effect mid-session: a key the store or the syncer read once at
// startup would parse here and change nothing, and an option that answers a
// reader's keystroke with silence is worse than no option. Everything in the
// config file is reachable before the process starts, through --set.
//
// Names are the config file's own, so a value found worth keeping moves into
// the file under the spelling it was tried with.
type setting struct {
	name string
	help string
	// def is the value & restores, spelled the way get spells it.
	def string
	get func(Model) string
	set func(*Model, string) error
}

var settings = []setting{{
	name: "applink_pace_ms",
	help: "gap between two Feishu navigations, in milliseconds",
	def:  strconv.Itoa(applink.DefaultPaceMS),
	get:  func(m Model) string { return strconv.FormatInt(m.pace.Milliseconds(), 10) },
	set: func(m *Model, v string) error {
		ms, err := strconv.Atoi(v)
		if err != nil || ms <= 0 {
			return fmt.Errorf("want a positive count of milliseconds, not %q", v)
		}
		m.pace = time.Duration(ms) * time.Millisecond
		return nil
	},
}}

func lookupSetting(name string) (setting, bool) {
	i := slices.IndexFunc(settings, func(s setting) bool { return s.name == name })
	if i < 0 {
		return setting{}, false
	}
	return settings[i], true
}

// runSet reads or writes one option, in vim's four forms: a bare :set lists
// them, name? reports one, name& restores its default and name=value sets it.
//
// A change lasts the session. The config file is what persists one, which is
// also what keeps :set from being a second writer to it.
func (m Model) runSet(rest string) Model {
	if rest == "" {
		var parts []string
		for _, s := range settings {
			parts = append(parts, s.name+"="+s.get(m))
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
	if !ok {
		return m.notify("unknown option "+name, true)
	}
	switch {
	case query:
		return m.notify(s.name+"="+s.get(m), false)
	case reset:
		value = s.def
	case !assigned:
		// A bare name is vim's way of turning a boolean on. Every option here
		// takes a value, so the reader meant to ask rather than to set.
		return m.notify(s.name+"="+s.get(m), false)
	}
	if err := s.set(&m, strings.TrimSpace(value)); err != nil {
		return m.notify(s.name+": "+err.Error(), true)
	}
	return m.notify(s.name+"="+s.get(m), false)
}
