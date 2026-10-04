package tui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveCommand_AUniquePrefixReachesTheWholeName(t *testing.T) {
	t.Parallel()
	for typed, want := range map[string]string{
		"cop":      "copy",
		"cf":       "config",
		"g":        "goto",
		"got":      "goto",
		"pr":       "preview",
		"sy":       "sync",
		"me":       "mentions",
		"reac":     "react",
		"sil":      "silence",
		"tod":      "todoist",
		"read":     "read-all",
		"read-all": "read-all",
	} {
		c, ok := resolveCommand(typed)
		require.True(t, ok, ":%s reaches nothing", typed)
		require.Equal(t, want, c.name, ":%s", typed)
	}
}

func TestResolveCommand_AnExactSpellingBeatsALongerName(t *testing.T) {
	t.Parallel()
	// s, u and q are names in their own right, so they stand for themselves
	// however many longer commands share their first letter.
	for typed, want := range map[string]string{
		"s":    "search",
		"u":    "unread",
		"q":    "q",
		"quit": "q",
		"chat": "goto",
		"at":   "mentions",
	} {
		c, ok := resolveCommand(typed)
		require.True(t, ok, ":%s reaches nothing", typed)
		require.Equal(t, want, c.name, ":%s", typed)
	}
}

func TestResolveCommand_RefusesAPrefixSeveralCommandsAnswerTo(t *testing.T) {
	t.Parallel()
	for _, typed := range []string{"", "co", "re", "se", "zz"} {
		_, ok := resolveCommand(typed)
		require.False(t, ok, ":%s resolved to something", typed)
	}
}

func TestCommandsWithPrefix_OffersACommandOnceHoweverManySpellingsMatched(t *testing.T) {
	t.Parallel()
	// goto answers to both "goto" and the alias "chat", and c reaches it
	// through the second — but it is one command and so one offer.
	require.Equal(t, []string{"copy", "goto", "candidates", "config"}, names(commandsWithPrefix("c")))
	require.Equal(t, []string{"react", "read-all"}, names(commandsWithPrefix("re")))
	require.Empty(t, commandsWithPrefix("zz"))
}

func TestRunCommand_AnAmbiguousPrefixNamesTheCandidates(t *testing.T) {
	t.Parallel()
	m, _ := newOutboxModel(t)

	out, cmd := m.runCommand("re")

	require.Nil(t, cmd, "a prefix the reader has to choose between runs nothing")
	m = out.(Model)
	require.True(t, m.noticeErr)
	require.Contains(t, m.notice, "react")
	require.Contains(t, m.notice, "read-all")
}

func TestRunCommand_StillRejectsWhatMatchesNothing(t *testing.T) {
	t.Parallel()
	m, _ := newOutboxModel(t)

	out, cmd := m.runCommand("zzz now")

	require.Nil(t, cmd)
	require.Equal(t, "unknown command :zzz", out.(Model).notice)
}

// The ? panel is the in-app reference, so a command missing from it is a
// command nobody finds, and one listed there that : does not answer to is a
// promise the line cannot keep.
func TestHelpEntries_NameEveryCommand(t *testing.T) {
	t.Parallel()
	listed := map[string]bool{}
	for _, e := range helpEntries {
		if e.mode != "COMMAND" || e.keys == "" {
			continue
		}
		name := strings.TrimPrefix(strings.Fields(e.keys)[0], ":")
		c, ok := resolveCommand(name)
		require.True(t, ok, "the panel lists :%s, which : does not answer to", name)
		listed[c.name] = true
	}
	for _, c := range commands {
		require.Equal(t, !c.noPanel, listed[c.name], ":%s", c.name)
	}
}

func names(cs []command) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.name
	}
	return out
}

func TestResolveCommand_SeReachesSearchSendAndSet(t *testing.T) {
	t.Parallel()
	// Three commands share it, so it stands for none of them; :s is still
	// :search, because an exact alias beats every longer name.
	_, ok := resolveCommand("se")
	require.False(t, ok)
	require.Equal(t, "send, search, set", commandNames(commandsWithPrefix("se")))
}
