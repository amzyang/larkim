package cli

import (
	"cmp"
	"context"
	"os"
	"strings"

	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/store"
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
func completeConfigKey(_ *cobra.Command, _ []string, prefix string) ([]cobra.Completion, cobra.ShellCompDirective) {
	// A value already typed is the reader's, not ours to narrow.
	if _, _, typed := strings.Cut(prefix, "="); typed {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []cobra.Completion
	for _, key := range config.Keys() {
		if hasPrefixFold(key, prefix) {
			out = append(out, key+"=")
		}
	}
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
				out = append(out, cobra.CompletionWithDesc(c.Name, c.ChatID))
			case hasPrefixFold(c.ChatID, prefix):
				out = append(out, cobra.CompletionWithDesc(c.ChatID, c.Name))
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
				out = append(out, cobra.CompletionWithDesc(c.Name, cmp.Or(c.Email, c.OpenID)))
			case hasPrefixFold(c.Email, prefix):
				out = append(out, cobra.CompletionWithDesc(c.Email, c.Name))
			case hasPrefixFold(c.OpenID, prefix):
				out = append(out, cobra.CompletionWithDesc(c.OpenID, c.Name))
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
				out = append(out, cobra.CompletionWithDesc(c.OpenID, c.Name))
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
				out = append(out, cobra.CompletionWithDesc(m.MessageID, oneLine(contentLabel(m), 48)))
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
