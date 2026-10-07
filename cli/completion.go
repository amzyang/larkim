package cli

import (
	"cmp"
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/todoist"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// compCmdName is what cobra calls the script generator it adds to a root
// command; the constant behind it is not exported.
const compCmdName = "completion"

// completionLimit caps the rows a store-backed completion walks. The shell
// narrows the candidates again against the typed word, so this bounds how
// long a Tab press takes rather than how many candidates survive it.
const completionLimit = 200

// completionTimeout bounds the one completion that asks a remote API. A Tab
// that hangs is worse than one that offers nothing.
const completionTimeout = 3 * time.Second

// completionMessages is how many recent messages a message-id completion
// offers. An id says nothing by itself, so each carries the message's text as
// its description, and a screenful of those is already as much as reads.
const completionMessages = 30

// isCompletion reports whether cmd exists to serve shell completion rather
// than to do work: the two hidden commands a shell calls on Tab, and the
// per-shell script generators, which Homebrew runs inside its sandbox while
// installing. Neither may write a log or arm the crash reporter.
func isCompletion(cmd *cobra.Command) bool {
	switch cmd.Name() {
	case cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
		return true
	}
	parent := cmd.Parent()
	return parent != nil && parent.Name() == compCmdName
}

// mustWire panics on a completion wiring mistake — a flag name that does not
// exist, or a second registration for one. Both are typos, and left alone
// they surface much later as a flag quietly offering the working directory.
func mustWire(err error) {
	if err != nil {
		panic(err)
	}
}

// completeNoFileDefault turns cobra's fallback — the working directory's
// files — into nothing, for every argument and flag that has not asked for
// something else. Hardly anything larkim takes is a path: ids, names, emoji
// keys, counts and timestamps are all worse served by a file listing than by
// silence, and a flag added later inherits the useful default rather than the
// useless one.
func completeNoFileDefault(cmd *cobra.Command) {
	if cmd.ValidArgsFunction == nil && len(cmd.ValidArgs) == 0 {
		cmd.ValidArgsFunction = cobra.NoFileCompletions
	}
	mark := func(f *pflag.Flag) {
		if _, taken := cmd.GetFlagCompletionFunc(f.Name); taken {
			return
		}
		// MarkFlagFilename is the other way a flag asks for paths, and it
		// leaves this annotation even when it names no extension.
		if _, path := f.Annotations[cobra.BashCompFilenameExt]; path {
			return
		}
		mustWire(cmd.RegisterFlagCompletionFunc(f.Name, cobra.NoFileCompletions))
	}
	cmd.Flags().VisitAll(mark)
	cmd.PersistentFlags().VisitAll(mark)
	for _, sub := range cmd.Commands() {
		completeNoFileDefault(sub)
	}
}

// hasPrefixFold is how every completion below narrows its candidates: by
// prefix, not by the fuzzy match --search does. zsh and fish filter the
// returned candidates again against the typed word, so a pinyin-initials hit
// would be dropped by the shell before it was ever shown.
func hasPrefixFold(s, prefix string) bool {
	return s != "" && strings.HasPrefix(strings.ToLower(s), strings.ToLower(prefix))
}

// completeConfigKey offers what --set takes, each candidate already carrying
// its = so the next keystroke is the value. NoSpace keeps the cursor there:
// the shell would otherwise end the word and the value would land as a
// separate argument.
//
// The candidates come from the config struct's own yaml tags, so a key added
// to the file is offered here without a second list to remember.
func (a *App) completeConfigKey(_ *cobra.Command, _ []string, prefix string) ([]cobra.Completion, cobra.ShellCompDirective) {
	// Past the =, only a key with a fixed set of values has anything to
	// offer; any other value is the reader's, not ours to narrow. The one
	// exception is a Todoist project, whose values are the account's.
	if key, val, typed := strings.Cut(prefix, "="); typed {
		if key == "todoist.project" {
			return a.completeTodoistProject(val)
		}
		var out []cobra.Completion
		for _, v := range config.Values(key) {
			if hasPrefixFold(v, val) {
				out = append(out, key+"="+v)
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
	var out []cobra.Completion
	for _, key := range config.Keys() {
		if hasPrefixFold(key, prefix) {
			out = append(out, key+"=")
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
}

// completeTodoistProject offers the account's project ids under the token in
// the config file, each named in its description, since the name is what a
// reader recognises and the id is what the key takes. The Inbox is offered as
// the empty value, which is how the file spells it, and comes first.
//
// It is the one completion that leaves the machine, so it is bounded by
// completionTimeout and answers nothing rather than an error a shell would
// print over the prompt.
func (a *App) completeTodoistProject(typed string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if a.cfg.Todoist.Token == "" {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ctx, cancel := context.WithTimeout(context.Background(), completionTimeout)
	defer cancel()
	ps, err := todoist.New(a.cfg.Todoist.Token, "", a.todoistBase).Projects(ctx)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []cobra.Completion
	for _, p := range todoist.InboxFirst(ps) {
		// hasPrefixFold never matches an empty value, and the Inbox's is one:
		// it is offered until something is typed.
		if v := p.Value(); v == typed || hasPrefixFold(v, typed) {
			out = append(out, candidate("todoist.project="+v, p.Name))
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveKeepOrder
}

// completeBodySource completes the path in a body flag, but only once the
// word opens with @: an inline body is the common case and must not be
// shadowed by a directory listing. The @ is carried back on every candidate
// because the shell replaces the whole word with the one that is picked.
func completeBodySource(_ *cobra.Command, _ []string, prefix string) ([]cobra.Completion, cobra.ShellCompDirective) {
	pat, ok := strings.CutPrefix(prefix, "@")
	// @@ escapes a literal @, so there is no path to complete behind it.
	if !ok || strings.HasPrefix(pat, "@") {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	expanded, err := expandPath(cmp.Or(pat, "."))
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	// A word ending in a separator lists that directory; anything else is the
	// stem the entries in its parent are narrowed by.
	if strings.HasSuffix(pat, string(os.PathSeparator)) || pat == "" {
		expanded += string(os.PathSeparator)
	}
	matches, err := filepath.Glob(expanded + "*")
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	// The candidates are spelled the way the reader is typing — a relative
	// word stays relative — so only the last segment comes from the glob.
	dir, _ := filepath.Split(pat)
	var out []cobra.Completion
	for _, m := range matches {
		name := filepath.Base(m)
		if st, err := os.Stat(m); err == nil && st.IsDir() {
			name += string(os.PathSeparator)
		}
		out = append(out, "@"+dir+name)
	}
	// Directories must stay open for the next segment, and cobra applies the
	// directive to the whole answer, so no candidate gets a space.
	return out, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
}

// completeFromStore answers a completion out of the database, or answers
// nothing when there is no database yet: pressing Tab must not be what
// creates it, and openStore would make the directory and run the migrations.
func (a *App) completeFromStore(
	fn func(context.Context, *store.Store) ([]cobra.Completion, error),
) ([]cobra.Completion, cobra.ShellCompDirective) {
	if _, err := os.Stat(a.cfg.DBPath()); err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	st, err := store.Open(a.cfg.DBPath())
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	defer st.Close()
	out, err := fn(context.Background(), st)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// candidate is a completion whose value and description are both names and
// text Feishu filled in, which the shell prints to the terminal as they
// arrive. Each is scrubbed before they are joined: the tab between them is
// the protocol's own separator, and one inside either would split it wrong.
func candidate(value, desc string) cobra.Completion {
	return cobra.CompletionWithDesc(inline(value), inline(desc))
}

// completeChatRef offers what --chat accepts: a chat's name, or its id.
func (a *App) completeChatRef(_ *cobra.Command, _ []string, prefix string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return a.completeFromStore(func(ctx context.Context, st *store.Store) ([]cobra.Completion, error) {
		chats, err := st.ListChats(ctx, store.ChatQuery{Limit: completionLimit})
		if err != nil {
			return nil, err
		}
		var out []cobra.Completion
		for _, c := range chats {
			switch {
			case hasPrefixFold(c.Name, prefix):
				out = append(out, candidate(c.Name, c.ChatID))
			case hasPrefixFold(c.ChatID, prefix):
				out = append(out, candidate(c.ChatID, c.Name))
			}
		}
		return out, nil
	})
}

// completeContactRef offers what --to accepts: a person's name, their address
// or their open id.
func (a *App) completeContactRef(_ *cobra.Command, _ []string, prefix string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return a.completeFromStore(func(ctx context.Context, st *store.Store) ([]cobra.Completion, error) {
		contacts, err := st.ListContacts(ctx, completionLimit)
		if err != nil {
			return nil, err
		}
		var out []cobra.Completion
		for _, c := range contacts {
			switch {
			case hasPrefixFold(c.Name, prefix):
				out = append(out, candidate(c.Name, cmp.Or(c.Email, c.OpenID)))
			case hasPrefixFold(c.Email, prefix):
				out = append(out, candidate(c.Email, c.Name))
			case hasPrefixFold(c.OpenID, prefix):
				out = append(out, candidate(c.OpenID, c.Name))
			}
		}
		return out, nil
	})
}

// completeSenderID offers open ids, which is all --sender takes; the name is
// the description because it is not a value the flag would accept.
func (a *App) completeSenderID(_ *cobra.Command, _ []string, prefix string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return a.completeFromStore(func(ctx context.Context, st *store.Store) ([]cobra.Completion, error) {
		contacts, err := st.ListContacts(ctx, completionLimit)
		if err != nil {
			return nil, err
		}
		var out []cobra.Completion
		for _, c := range contacts {
			if hasPrefixFold(c.OpenID, prefix) {
				out = append(out, candidate(c.OpenID, c.Name))
			}
		}
		return out, nil
	})
}

// completeMessageID offers the newest message ids, narrowed to --chat where
// the command has one, since an id is only ever reached for inside the
// conversation it belongs to.
func (a *App) completeMessageID(cmd *cobra.Command, _ []string, prefix string) ([]cobra.Completion, cobra.ShellCompDirective) {
	out, directive := a.completeFromStore(func(ctx context.Context, st *store.Store) ([]cobra.Completion, error) {
		q := store.MessageQuery{Limit: completionMessages, Desc: true}
		if ref := flagValue(cmd, "chat"); ref != "" {
			// A --chat that resolves to nothing narrows nothing rather than
			// failing: it is not the word being completed.
			if id, err := resolveChatLocal(ctx, st, ref); err == nil {
				q.ChatID = id
			}
		}
		rows, err := st.ListMessages(ctx, q)
		if err != nil {
			return nil, err
		}
		var out []cobra.Completion
		for _, m := range rows {
			if hasPrefixFold(m.MessageID, prefix) {
				out = append(out, candidate(m.MessageID, oneLine(contentLabel(m), 48)))
			}
		}
		return out, nil
	})
	// Newest first is what makes a list of opaque ids navigable at all.
	return out, directive | cobra.ShellCompDirectiveKeepOrder
}

// flagValue reads a flag the command may not have, which is how a completion
// shared between commands narrows by one only where it exists.
func flagValue(cmd *cobra.Command, name string) string {
	f := cmd.Flags().Lookup(name)
	if f == nil {
		return ""
	}
	return f.Value.String()
}
