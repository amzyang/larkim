package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/amzyang/larkim/store"
	"github.com/spf13/cobra"
)

func (a *App) chatsCmd() *cobra.Command {
	chats := &cobra.Command{Use: "chats", Short: "Chats (groups and direct messages)"}
	var q store.ChatQuery
	list := &cobra.Command{
		Use:   "list",
		Short: "List synced chats, most recently active first",
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := a.openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			rows, err := st.ListChats(context.Background(), q)
			if err != nil {
				return err
			}
			if a.json() {
				return a.printJSON(rows)
			}
			out := make([][]string, 0, len(rows))
			for _, c := range rows {
				out = append(out, []string{c.ChatID, c.ChatMode, oneLine(c.Name, 40), strconv.FormatInt(c.MessageCount, 10), fmtMs(c.LastMessageMs)})
			}
			table(a.Out, []string{"chat_id", "mode", "name", "msgs", "last message"}, out)
			return nil
		},
	}
	list.Flags().StringVar(&q.Mode, "type", "", "filter by chat mode: group | topic | p2p")
	list.Flags().StringVar(&q.Search, "search", "", "case-insensitive substring of the chat name")
	list.Flags().BoolVar(&q.IncludeLeft, "include-left", false, "include chats you are no longer in")
	list.Flags().IntVar(&q.Limit, "limit", 0, "max rows (default all)")
	chats.AddCommand(list)
	return chats
}

func (a *App) messagesCmd() *cobra.Command {
	messages := &cobra.Command{Use: "messages", Short: "Synced messages"}
	messages.AddCommand(a.messagesListCmd(), a.messagesShowCmd(), a.messagesThreadCmd())
	return messages
}

func (a *App) messagesListCmd() *cobra.Command {
	var q store.MessageQuery
	var since, until, order, query, around string
	var contextN int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List messages with filters, sorting and cursor pagination; --query searches rendered text",
		RunE: func(cmd *cobra.Command, _ []string) error {
			now := time.Now()
			var err error
			if cmd.Flags().Changed("context") && around == "" {
				return fmt.Errorf("--context only applies to --around")
			}
			if q.Offset != 0 && (q.BeforeID != "" || q.AfterID != "" || around != "") {
				return fmt.Errorf("--offset and the cursor flags are two ways to paginate; pick one")
			}
			if t, err := parseTime(since, now); err != nil {
				return err
			} else if !t.IsZero() {
				q.SinceMs = t.UnixMilli()
			}
			if t, err := parseTime(until, now); err != nil {
				return err
			} else if !t.IsZero() {
				q.UntilMs = t.UnixMilli()
			}
			switch order {
			case "asc":
			case "desc":
				q.Desc = true
			default:
				return fmt.Errorf("--order must be asc or desc")
			}
			st, err := a.openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := context.Background()
			if q.ChatID != "" {
				q.ChatID, err = resolveChatLocal(ctx, st, q.ChatID)
				if err != nil {
					return err
				}
			}
			var rows []store.Message
			switch {
			case query != "":
				rows, err = st.SearchMessages(ctx, query, q.ChatID, q.Limit)
			case around != "":
				rows, err = listAround(ctx, st, q, around, contextN)
			default:
				rows, err = st.ListMessages(ctx, q)
			}
			if err != nil {
				return err
			}
			if a.json() {
				return a.printJSON(rows)
			}
			a.printMessageTable(rows, q.ChatID == "")
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&query, "query", "", "full-text search over rendered content and sender names (every term must match; combinable with --chat and --limit)")
	f.StringVar(&q.ChatID, "chat", "", "chat id (oc_…) or exact chat name")
	f.StringVar(&q.SenderID, "sender", "", "sender open_id (ou_…)")
	f.StringVar(&q.MsgType, "type", "", "msg_type: text | post | image | file | interactive | system | …")
	f.StringVar(&since, "since", "", "lower bound: 2026-09-01, RFC 3339, or 24h")
	f.StringVar(&until, "until", "", "upper bound, same formats")
	f.StringVar(&q.BeforeID, "before", "", "page from just before this message id, exclusive; --order picks the direction")
	f.StringVar(&q.AfterID, "after", "", "page from just after this message id, exclusive")
	f.StringVar(&around, "around", "", "centre on this message id, in its own chat unless --chat says otherwise")
	f.IntVar(&contextN, "context", 20, "messages on each side of --around")
	f.BoolVar(&q.IncludeDeleted, "include-deleted", false, "include recalled messages")
	f.BoolVar(&q.Unread, "unread", false, "only messages Feishu reports as unread by you")
	f.BoolVar(&q.Unconsumed, "unconsumed", false, "only messages not yet marked consumed locally")
	f.StringVar(&order, "order", "desc", "asc | desc by create time")
	f.IntVar(&q.Limit, "limit", 50, "max rows")
	f.IntVar(&q.Offset, "offset", 0, "rows to skip")
	cmd.MarkFlagsMutuallyExclusive("around", "before")
	cmd.MarkFlagsMutuallyExclusive("around", "after")
	cmd.MarkFlagsMutuallyExclusive("around", "limit") // --around is sized by --context
	return cmd
}

// listAround returns the anchor with contextN messages on either side. Two
// cursor queries beat one UNION: each side is an ordinary page, and the
// anchor is added back by hand because both cursors exclude it.
func listAround(ctx context.Context, st *store.Store, q store.MessageQuery, id string, n int) ([]store.Message, error) {
	anchor, err := st.GetMessage(ctx, id)
	if err != nil {
		return nil, err
	}
	if q.ChatID == "" {
		q.ChatID = anchor.ChatID
	}
	page := func(q store.MessageQuery) ([]store.Message, error) {
		q.Limit, q.Offset = n, 0
		return st.ListMessages(ctx, q)
	}
	older := q
	older.BeforeID, older.Desc = id, true
	before, err := page(older)
	if err != nil {
		return nil, err
	}
	slices.Reverse(before)
	newer := q
	newer.AfterID, newer.Desc = id, false
	after, err := page(newer)
	if err != nil {
		return nil, err
	}
	out := append(append(before, anchor), after...)
	if q.Desc {
		slices.Reverse(out)
	}
	return out, nil
}

func (a *App) printMessageTable(rows []store.Message, withChat bool) {
	header := []string{"message_id", "time", "sender", "type", "content"}
	if withChat {
		header = append([]string{"chat_id"}, header...)
	}
	out := make([][]string, 0, len(rows))
	for _, m := range rows {
		content := contentLabel(m)
		if m.RenderedAt == 0 {
			content = "(unrendered) " + content
		}
		if m.Deleted {
			content = "(recalled) " + content
		}
		r := []string{m.MessageID, fmtMs(m.CreateMs), oneLine(senderLabel(m), 16), m.MsgType, oneLine(content, 80)}
		if withChat {
			r = append([]string{m.ChatID}, r...)
		}
		out = append(out, r)
	}
	table(a.Out, header, out)
}

func (a *App) messagesShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <message_id>",
		Short: "Show one message with raw content and rendering",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := a.openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := context.Background()
			m, err := st.GetMessage(ctx, args[0])
			if err != nil {
				return err
			}
			resources, err := st.ResourcesFor(ctx, m.MessageID)
			if err != nil {
				return err
			}
			for i := range resources {
				if resources[i].LocalPath != "" && !filepath.IsAbs(resources[i].LocalPath) {
					resources[i].LocalPath = filepath.Join(a.cfg.DataDir, resources[i].LocalPath)
				}
			}
			if a.json() {
				return a.printJSON(struct {
					store.Message
					Resources []store.Resource `json:"resources"`
				}{m, resources})
			}
			fmt.Fprintf(a.Out, "message_id:  %s\nchat_id:     %s\ntype:        %s\nsender:      %s (%s)\ncreated:     %s\n",
				m.MessageID, m.ChatID, m.MsgType, m.SenderName, m.SenderID, fmtMs(m.CreateMs))
			if m.Updated {
				fmt.Fprintf(a.Out, "updated:     %s\n", fmtMs(m.UpdateMs))
			}
			if m.Deleted {
				fmt.Fprintf(a.Out, "recalled:    %s\n", fmtMs(m.DeletedSeenAt))
			}
			if m.ThreadID != "" {
				fmt.Fprintf(a.Out, "thread_id:   %s\n", m.ThreadID)
			}
			if m.ReplyTo != "" {
				fmt.Fprintf(a.Out, "reply_to:    %s\n", m.ReplyTo)
			}
			if m.IsReadRemote != nil {
				fmt.Fprintf(a.Out, "read (feishu): %v\n", *m.IsReadRemote)
			}
			if m.ConsumedAt != 0 {
				fmt.Fprintf(a.Out, "consumed:    %s\n", fmtMs(m.ConsumedAt))
			}
			fmt.Fprintf(a.Out, "\n%s\n\nraw: %s\n", m.Content, m.ContentRaw)
			for _, r := range resources {
				fmt.Fprintf(a.Out, "resource: %s %s %s", r.Type, r.FileKey, r.Status)
				if r.LocalPath != "" {
					fmt.Fprintf(a.Out, " %s (%d bytes)", r.LocalPath, r.SizeBytes)
				}
				if r.LastError != "" {
					fmt.Fprintf(a.Out, " [%s]", r.LastError)
				}
				fmt.Fprintln(a.Out)
			}
			return nil
		},
	}
}

func (a *App) messagesThreadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "thread <message_id|thread_id>",
		Short: "List a thread's root and replies in order",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := a.openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := context.Background()
			tid := args[0]
			if m, err := st.GetMessage(ctx, tid); err == nil {
				if m.ThreadID == "" {
					return fmt.Errorf("%s has no thread", tid)
				}
				tid = m.ThreadID
			}
			rows, err := st.ListMessages(ctx, store.MessageQuery{ThreadID: tid, Limit: 1000})
			if err != nil {
				return err
			}
			if a.json() {
				return a.printJSON(rows)
			}
			a.printMessageTable(rows, false)
			return nil
		},
	}
}

// resolveChatLocal accepts a chat id or an exact chat name known to the store.
func resolveChatLocal(ctx context.Context, st *store.Store, ref string) (string, error) {
	if strings.HasPrefix(ref, "oc_") {
		return ref, nil
	}
	chats, err := st.ListChats(ctx, store.ChatQuery{Search: ref})
	if err != nil {
		return "", err
	}
	var exact []store.Chat
	for _, c := range chats {
		if c.Name == ref {
			exact = append(exact, c)
		}
	}
	switch len(exact) {
	case 1:
		return exact[0].ChatID, nil
	case 0:
		if len(chats) == 1 {
			return chats[0].ChatID, nil
		}
		return "", fmt.Errorf("no chat named %q (%d partial matches; use `larkim chats list --search`)", ref, len(chats))
	default:
		return "", fmt.Errorf("%d chats named %q; pass the chat id", len(exact), ref)
	}
}
