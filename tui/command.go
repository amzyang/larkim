package tui

import (
	"slices"
	"strings"
)

// argKind is what a command's first argument names, and so what the : line
// offers once the command itself stands. Anything past the first argument is
// prose — the text of a :send, the question of an :ai — and completes against
// nothing.
type argKind int

const (
	argNone      argKind = iota // no argument, or free text nothing narrows
	argChat                     // a chat in the list
	argTarget                   // a chat or a person, the pair :send takes
	argEmoji                    // the reaction set
	argEnum                     // the fixed words in command.enum
	argSetting                  // an option :set reaches, which is a live one
	argConfigKey                // any key of the configuration file
)

// command is one : command. This table is what runCommand dispatches on, what
// the : line completes against and what the ? panel documents, so the three
// cannot come to name different sets.
type command struct {
	name    string
	aliases []string
	arg     argKind
	enum    []string
	// usage spells the argument the way the panel and the completion row draw
	// it, the colon and the name apart. Empty where the command takes none.
	usage string
	help  string
	// noPanel keeps a command out of the generated COMMAND rows, for one the
	// panel documents in a section of its own.
	noPanel bool
}

// display is the command as the panel and the completion row name it.
func (c command) display() string {
	if c.usage == "" {
		return ":" + c.name
	}
	return ":" + c.name + " " + c.usage
}

// commands is ordered the way the panel reads it: what a reader reaches for
// first, first.
var commands = []command{
	{name: "copy", arg: argEnum, enum: []string{"200", "7d", "all"}, usage: "<200|7d|all>", help: "put that much of the chat on the clipboard as agent context"},
	{name: "goto", aliases: []string{"chat"}, arg: argChat, usage: "<chat>", help: "open a chat by name or id"},
	{name: "react", arg: argEmoji, usage: "<emoji>", help: "react to the selected message"},
	{name: "send", arg: argTarget, usage: "<chat|ou_> <text>", help: "send without opening the chat"},
	{name: "search", aliases: []string{"s"}, usage: "<text>", help: "the panel Ctrl+f opens, with the text already in it"},
	{name: "mentions", aliases: []string{"at"}, help: "every message that named you"},
	{name: "candidates", help: "pick a pending lark-watch reply draft into the composer"},
	{name: "unread", aliases: []string{"u"}, help: "every chat still waiting, parted by chat — the Unread row of the list"},
	{name: "read-all", help: "take every chat as read and clear the Feishu client's red dots"},
	{name: "set", arg: argSetting, usage: "[<option>[=<value>]]", help: "read or retune a runtime option for this session"},
	{name: "config", aliases: []string{"cfg"}, arg: argConfigKey, usage: "[<key>]", help: "edit the configuration file"},
	{name: "preview", help: "toggle the composer's preview"},
	{name: "sync", help: "sync now"},
	{name: "q", aliases: []string{"quit"}, help: "quit"},
	// The assistant's four forms need four rows, which the COMMAND list has no
	// room for; the ASSISTANT section carries them.
	{name: "ai", arg: argEnum, enum: []string{"summary", "draft", "todo"}, usage: "<summary|draft|todo> or <question>", help: "ask about this chat", noPanel: true},
}

// resolveCommand reads a typed name the way vim does: an exact name or alias
// stands for itself, and past that a prefix that reaches one command alone is
// that command — :co is :copy because nothing else starts with co. A prefix
// several commands answer to resolves to none of them.
func resolveCommand(name string) (command, bool) {
	if name == "" {
		return command{}, false
	}
	for _, c := range commands {
		if c.name == name || slices.Contains(c.aliases, name) {
			return c, true
		}
	}
	near := commandsWithPrefix(name)
	if len(near) == 1 {
		return near[0], true
	}
	return command{}, false
}

// commandsWithPrefix is every command a prefix reaches, in table order. A
// command is offered once however many of its spellings the prefix matched,
// which is what makes :got unambiguous rather than goto twice over.
func commandsWithPrefix(prefix string) []command {
	var out []command
	for _, c := range commands {
		if strings.HasPrefix(c.name, prefix) || slices.ContainsFunc(c.aliases, func(a string) bool {
			return strings.HasPrefix(a, prefix)
		}) {
			out = append(out, c)
		}
	}
	return out
}

// commandNames names a set of commands for a message the reader has to choose
// between them from.
func commandNames(cs []command) string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.name
	}
	return strings.Join(out, ", ")
}
