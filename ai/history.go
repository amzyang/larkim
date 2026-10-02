package ai

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/amzyang/larkim/larkcli"
)

// History is the read-only reach into one chat's synced history an agent may
// be given. The system prompt teaches the exact larkim commands and nothing
// else; the tool gate passes only those commands, scoped to the chat. No MCP
// server, no new dependency — the CLI already exists.
type History struct {
	ChatID     string
	ConfigPath string // spelled into the taught commands, '' for none
}

// instructions is what the agent is taught, per turn, as data it can act on.
func (h History) instructions() string {
	cmd := "larkim"
	if h.ConfigPath != "" {
		cmd += " --config " + larkcli.ArgvLine([]string{h.ConfigPath})
	}
	flags := make([]string, len(HistoryFlags))
	for i, f := range HistoryFlags {
		flags[i] = "[" + f.Name + " " + f.Value + "]"
	}
	return fmt.Sprintf(`You may read this chat's synced history yourself, with your shell tool, using only this command:

  %s messages list --chat %s --json %s

--before pages from just before a message id you hold, oldest first by --limit; --after from just after one; --around centres on one message id with its own --context <n>; --query full-text searches this chat's rendered text. Any other command, or any other tool, ends this answer.`, cmd, h.ChatID, strings.Join(flags, " "))
}

// HistoryFlag is one optional flag of the history command.
type HistoryFlag struct {
	Name  string // with its leading --
	Value string // the placeholder the prompt shows after it
}

// HistoryFlags is the optional flag set of `larkim messages list` the agent
// gets, in the order the prompt teaches them. The taught prompt and the
// AllowCommand gate both read this list, so a flag cannot be accepted without
// being taught or taught without being accepted. The structural --chat and
// --json belong to the command itself, not this list.
var HistoryFlags = []HistoryFlag{
	{"--before", "<message id>"},
	{"--after", "<message id>"},
	{"--around", "<message id>"},
	{"--context", "<n>"},
	{"--limit", "<n>"},
	{"--order", "asc|desc"},
	{"--since", "<time>"},
	{"--until", "<time>"},
	{"--query", "<words>"},
}

// historyFlag reports whether name is one of HistoryFlags.
func historyFlag(name string) bool {
	return slices.ContainsFunc(HistoryFlags, func(f HistoryFlag) bool { return f.Name == name })
}

// AllowCommand reports whether argv is one of the read-only larkim
// invocations the history instructions teach, scoped to chatID. The gate
// reads the parsed command rather than any permission question, because the
// agent may never ask one — it can be configured to run its shell silently.
func AllowCommand(argv []string, chatID string) bool {
	if len(argv) < 4 || argv[0] != "larkim" {
		return false
	}
	i := 1
	if argv[i] == "--config" {
		// The path is whatever the reader's own configuration lives at; the
		// value is data, not a lever.
		if len(argv) < i+3 {
			return false
		}
		i += 2
	}
	if argv[i] != "messages" || len(argv) <= i+1 || argv[i+1] != "list" {
		return false
	}
	i += 2
	chat, haveChat := "", false
	for i < len(argv) {
		flag := argv[i]
		i++
		if flag == "--json" {
			continue
		}
		// --chat is spelled into the taught command itself; every other value
		// flag is one HistoryFlags names.
		if flag != "--chat" && !historyFlag(flag) {
			return false
		}
		if i >= len(argv) {
			return false
		}
		if flag == "--chat" {
			chat, haveChat = argv[i], true
		}
		if flag == "--order" && argv[i] != "asc" && argv[i] != "desc" {
			return false
		}
		i++
	}
	return haveChat && chat == chatID
}

// splitCommand splits a shell command into argv, honoring single and double
// quotes. It refuses anything that could run more than the one command — a
// semicolon, a pipe, a substitution — anywhere outside quotes: the gate
// passes one larkim invocation, not one larkim invocation and whatever
// follows it.
func splitCommand(cmd string) ([]string, bool) {
	var argv []string
	var cur strings.Builder
	started := false
	flush := func() {
		if started {
			argv = append(argv, cur.String())
			cur.Reset()
			started = false
		}
	}
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		switch c {
		case ' ', '\t', '\n':
			flush()
		case '\'', '"':
			// Quotes make one argument of what is inside them; the closing
			// mark does not end it. Inside double quotes a shell still runs
			// substitutions, and keeps a backslash only before $ ` " \ and
			// a newline: this parser must never read the command as tamer
			// than the shell that will run it.
			q := c
			i++
			for i < len(cmd) && cmd[i] != q {
				if q == '"' && (cmd[i] == '$' || cmd[i] == '`') {
					return nil, false
				}
				if q == '"' && cmd[i] == '\\' && i+1 < len(cmd) &&
					strings.IndexByte("$`\"\\n", cmd[i+1]) >= 0 {
					i++
				}
				cur.WriteByte(cmd[i])
				started = true
				i++
			}
			if i >= len(cmd) {
				return nil, false
			}
		case ';', '|', '&', '>', '<', '$', '`', '(', ')':
			return nil, false
		case '\\':
			if i+1 >= len(cmd) {
				return nil, false
			}
			i++
			cur.WriteByte(cmd[i])
			started = true
		default:
			cur.WriteByte(c)
			started = true
		}
	}
	flush()
	return argv, len(argv) > 0
}

// commandOf reads the command out of a shell tool call's raw input, and the
// kind the agent names the call as. Only the shell is ever allowed: every
// other tool the agent could reach reads this machine, which is what the
// gate exists to keep it from.
func commandOf(kind string, rawInput any) (string, bool) {
	if kind != "execute" && kind != "" {
		return "", false
	}
	m, ok := rawInput.(map[string]any)
	if !ok {
		return "", false
	}
	cmd, _ := m["command"].(string)
	return cmd, cmd != ""
}

// traceLine is what a history call leaves in the answer: the command it ran
// and, when its output can be counted, how many rows that was.
func traceLine(title string, rawOutput any) string {
	cmd := strings.TrimPrefix(title, "$ ")
	line := "⌕ " + cmd
	if n, ok := rowsOf(rawOutput); ok {
		line += fmt.Sprintf(" · %d rows", n)
	}
	return line
}

// rowsOf counts the rows a history call returned: a JSON array's length, or
// the lines of anything else.
func rowsOf(rawOutput any) (int, bool) {
	var out string
	switch v := rawOutput.(type) {
	case string:
		out = v
	case map[string]any:
		s, _ := v["output"].(string)
		out = s
	default:
		return 0, false
	}
	var arr []any
	if json.Unmarshal([]byte(out), &arr) == nil {
		return len(arr), true
	}
	if out == "" {
		return 0, false
	}
	return strings.Count(out, "\n") + 1, true
}
