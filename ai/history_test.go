package ai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAllowCommand_PassesOnlyTheTaughtShapes(t *testing.T) {
	const chat = "oc_quiet"
	good := [][]string{
		{"larkim", "messages", "list", "--chat", chat, "--json"},
		{"larkim", "messages", "list", "--chat", chat, "--json", "--limit", "40"},
		{"larkim", "messages", "list", "--chat", chat, "--before", "om_1", "--limit", "40", "--json"},
		{"larkim", "messages", "list", "--chat", chat, "--around", "om_1", "--context", "20", "--json"},
		{"larkim", "messages", "list", "--chat", chat, "--query", "发布单", "--json"},
		{"larkim", "messages", "list", "--chat", chat, "--order", "asc", "--since", "24h", "--json"},
		{"larkim", "--config", "/Users/linlan/dev.yaml", "messages", "list", "--chat", chat, "--json"},
	}
	for _, argv := range good {
		require.True(t, AllowCommand(argv, chat), "%v", argv)
	}
	bad := [][]string{
		// Another chat's history is not this chat's.
		{"larkim", "messages", "list", "--chat", "oc_elsewhere", "--json"},
		// No chat at all reaches every chat.
		{"larkim", "messages", "list", "--json"},
		// A flag the taught command does not carry.
		{"larkim", "messages", "list", "--chat", chat, "--query", "x", "--order", "up", "--json"},
		{"larkim", "messages", "list", "--chat", chat, "--include-deleted", "--json"},
		// Anything that is not the one read-only listing.
		{"larkim", "messages", "show", "om_1"},
		{"larkim", "send", "--chat", chat, "hi"},
		{"larkim", "--config", "x", "db", "wipe"},
		{"cat", "/etc/passwd"},
		{"larkim"},
	}
	for _, argv := range bad {
		require.False(t, AllowCommand(argv, chat), "%v", argv)
	}
}

func TestSplitCommand_RefusesAnythingThatRunsMoreThanOneCommand(t *testing.T) {
	for _, cmd := range []string{
		"larkim messages list --chat oc_quiet --json; rm -rf ~",
		"larkim messages list --chat oc_quiet --json && say pwned",
		"larkim messages list --chat oc_quiet --json | tee /tmp/leak",
		"echo $(cat ~/.ssh/id_rsa)",
		"larkim messages list --chat oc_quiet --json > /tmp/leak",
		"larkim messages list --chat oc_quiet --json `whoami`",
	} {
		_, ok := splitCommand(cmd)
		require.False(t, ok, "%q", cmd)
	}
	// Quoted words are one argument, and the quotes are not part of it.
	argv, ok := splitCommand(`larkim messages list --chat oc_quiet --query "发布 单" --json`)
	require.True(t, ok)
	require.Equal(t, []string{"larkim", "messages", "list", "--chat", "oc_quiet", "--query", "发布 单", "--json"}, argv)
	argv, ok = splitCommand(`larkim --config '/Users/linlan/my dev.yaml' messages list --chat oc_quiet --json`)
	require.True(t, ok)
	require.Equal(t, "/Users/linlan/my dev.yaml", argv[2])
}

func TestTraceLine_CountsTheRowsTheCallReturned(t *testing.T) {
	require.Equal(t, "⌕ larkim messages list --chat oc_quiet --json · 4 rows",
		traceLine("$ larkim messages list --chat oc_quiet --json", map[string]any{"output": "[{},{},{},{}]"}))
	require.Equal(t, "⌕ larkim messages list --json · 3 rows",
		traceLine("$ larkim messages list --json", "a\nb\nc"))
	require.Equal(t, "⌕ larkim messages list --json",
		traceLine("$ larkim messages list --json", nil), "nothing countable is left off")
}

func TestHistoryInstructions_TeachTheExactCommands(t *testing.T) {
	h := History{ChatID: "oc_quiet", ConfigPath: "/Users/linlan/dev.yaml"}
	s := h.instructions()
	require.Contains(t, s, "larkim --config /Users/linlan/dev.yaml messages list --chat oc_quiet --json")
	require.Contains(t, s, "--before", "the continuation paging is taught")
	require.Contains(t, s, "--query", "the search is taught")
	require.Contains(t, s, "Any other command, or any other tool, ends this answer")
}
