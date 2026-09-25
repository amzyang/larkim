package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/resolve"
	"github.com/amzyang/larkim/store"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

func (a *App) sendCmd() *cobra.Command {
	var to, chat string
	var body outgoingFlags
	cmd := &cobra.Command{
		Use:   "send --to <ou_|email> | --chat <oc_|name> --text|--markdown|--image <body>",
		Short: "Send a text, markdown or image message to a user or a chat",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if (to == "") == (chat == "") {
				return fmt.Errorf("pass exactly one of --to or --chat")
			}
			if err := body.check(); err != nil {
				return err
			}
			st, err := a.openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := context.Background()
			client := a.client()
			r := &resolve.Resolver{Store: st, Client: client, Now: func() int64 { return time.Now().UnixMilli() }}
			var target larkcli.Target
			var label string
			if chat != "" {
				c, err := r.Chat(ctx, chat)
				if err != nil {
					return err
				}
				target.ChatID = c.ChatID
				label = c.Name
			} else {
				u, err := r.User(ctx, to)
				if err != nil {
					return err
				}
				target.UserID = u.OpenID
				label = u.Name
			}
			msg, err := body.outgoing(ctx, client)
			if err != nil {
				return err
			}
			sent, err := client.Send(ctx, target, msg, uuid.NewString())
			if err != nil {
				return err
			}
			a.ingestSent(ctx, st, sent)
			return a.printSent(sent, label)
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "recipient: open_id (ou_…), email or exact name")
	cmd.Flags().StringVar(&chat, "chat", "", "chat: id (oc_…) or exact name")
	body.register(cmd)
	return cmd
}

// outgoingFlags are the three bodies a send can carry. They are exclusive the
// way lark-cli's own content flags are, and --text stays verbatim: a script
// piping a changelog into it must not start sending rich text.
type outgoingFlags struct {
	text     string
	markdown string
	image    string
}

func (o *outgoingFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&o.text, "text", "", "plain text to send, verbatim")
	cmd.Flags().StringVar(&o.markdown, "markdown", "", "markdown to send as a rich-text post")
	cmd.Flags().StringVar(&o.image, "image", "", "image to send: a path, or an img_… key Feishu already holds")
}

func (o outgoingFlags) check() error {
	n := 0
	for _, v := range []string{o.text, o.markdown, o.image} {
		if strings.TrimSpace(v) != "" {
			n++
		}
	}
	if n != 1 {
		return fmt.Errorf("pass exactly one of --text, --markdown or --image")
	}
	return nil
}

// outgoing resolves the flags into a body. An image path is uploaded here
// because lark-cli's own --image refuses an absolute path.
func (o outgoingFlags) outgoing(ctx context.Context, client larkcli.Client) (larkcli.Outgoing, error) {
	switch {
	case strings.TrimSpace(o.markdown) != "":
		return larkcli.Markdown(o.markdown), nil
	case strings.TrimSpace(o.image) != "":
		if larkcli.IsImageKey(o.image) {
			return larkcli.Image(o.image), nil
		}
		path, err := expandPath(o.image)
		if err != nil {
			return larkcli.Outgoing{}, err
		}
		key, err := client.UploadImage(ctx, path)
		if err != nil {
			return larkcli.Outgoing{}, err
		}
		return larkcli.Image(key), nil
	default:
		return larkcli.Text(o.text), nil
	}
}

// expandPath resolves ~ and relative paths the way a shell would, so a path
// typed by hand reaches the same file the shell would have opened.
func expandPath(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("no home directory to expand %s against", p)
		}
		p = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
	}
	return filepath.Abs(p)
}

func (a *App) replyCmd() *cobra.Command {
	var body outgoingFlags
	var inThread bool
	cmd := &cobra.Command{
		Use:   "reply <message_id> --text|--markdown|--image <body>",
		Short: "Reply to a message, optionally inside its thread",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := body.check(); err != nil {
				return err
			}
			st, err := a.openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := context.Background()
			client := a.client()
			msg, err := body.outgoing(ctx, client)
			if err != nil {
				return err
			}
			sent, err := client.Reply(ctx, args[0], msg, inThread, uuid.NewString())
			if err != nil {
				return err
			}
			a.ingestSent(ctx, st, sent)
			return a.printSent(sent, "")
		},
	}
	body.register(cmd)
	cmd.Flags().BoolVar(&inThread, "in-thread", false, "reply in the message's thread instead of the main chat")
	return cmd
}

// ingestSent stores the message we just sent so it is visible before the next
// daemon tick; failures are reported but do not fail the send.
func (a *App) ingestSent(ctx context.Context, st *store.Store, sent larkcli.SentMessage) {
	if err := a.syncer(st).IngestIDs(ctx, []string{sent.MessageID}); err != nil {
		fmt.Fprintln(a.Err, "larkim: sent, but could not store the message locally:", err)
	}
}

func (a *App) printSent(sent larkcli.SentMessage, label string) error {
	if a.json() {
		return a.printJSON(sent)
	}
	if label != "" {
		fmt.Fprintf(a.Out, "sent %s to %s (%s)\n", sent.MessageID, label, sent.ChatID)
	} else {
		fmt.Fprintf(a.Out, "sent %s in %s\n", sent.MessageID, sent.ChatID)
	}
	return nil
}
