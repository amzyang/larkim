package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/larkmd"
	"github.com/amzyang/larkim/resolve"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/sync"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

func (a *App) sendCmd() *cobra.Command {
	var to, chat, idemKey string
	var body outgoingFlags
	cmd := &cobra.Command{
		Use:   "send --to <ou_|email> | --chat <oc_|name> --text|--markdown|--image|--file <body>",
		Short: "Send a text, markdown, image or file message to a user or a chat",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if (to == "") == (chat == "") {
				return fmt.Errorf("pass exactly one of --to or --chat")
			}
			if err := body.check(); err != nil {
				return &usageError{err}
			}
			if err := body.resolve(a.In); err != nil {
				return &usageError{err}
			}
			st, err := a.openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := context.Background()
			client := a.client()
			r := &resolve.Resolver{Store: st, Client: client, Now: func() int64 { return time.Now().UnixMilli() }, Log: a.logger()}
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
			msg, err := body.outgoing(ctx, client, sync.HTTPFetch)
			if err != nil {
				return err
			}
			a.warnMarkdown(msg)
			sent, err := client.Send(ctx, target, msg, idempotencyKey(idemKey))
			if err != nil {
				return err
			}
			a.ingestSent(ctx, st, sent)
			return a.printSent(sent, label)
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "recipient: open_id (ou_…), email or exact name")
	cmd.Flags().StringVar(&chat, "chat", "", "chat: id (oc_…) or exact name")
	mustWire(cmd.RegisterFlagCompletionFunc("to", a.completeContactRef))
	mustWire(cmd.RegisterFlagCompletionFunc("chat", a.completeChatRef))
	body.register(cmd)
	registerIdempotencyKey(cmd, &idemKey)
	return cmd
}

func registerIdempotencyKey(cmd *cobra.Command, v *string) {
	cmd.Flags().StringVar(v, "idempotency-key", "",
		"key Feishu deduplicates this send by for an hour (default: a fresh uuid)")
}

// idempotencyKey is what Feishu deduplicates a send by for an hour. A caller
// that can issue the same action twice — a notification banner clicked again,
// a retried script — derives a key from the action so the repeat lands as one
// message, and picks different keys for actions it wants delivered separately
// even when their text is identical. Without one, every send is its own.
func idempotencyKey(supplied string) string {
	if s := strings.TrimSpace(supplied); s != "" {
		return s
	}
	return uuid.NewString()
}

// outgoingFlags are the bodies a send can carry. They are exclusive the way
// lark-cli's own content flags are, and --text stays verbatim: a script piping
// a changelog into it must not start sending rich text.
type outgoingFlags struct {
	text     string
	markdown string
	image    string
	file     string
}

func (o *outgoingFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&o.text, "text", "", "plain text to send, verbatim; @file reads a file, - reads stdin")
	cmd.Flags().StringVar(&o.markdown, "markdown", "", "markdown to send as a rich-text post; @file reads a file, - reads stdin")
	mustWire(cmd.RegisterFlagCompletionFunc("text", completeBodySource))
	mustWire(cmd.RegisterFlagCompletionFunc("markdown", completeBodySource))
	cmd.Flags().StringVar(&o.image, "image", "", "image to send: a path, or an img_… key Feishu already holds")
	cmd.Flags().StringVar(&o.file, "file", "", "file to send: a path, or a file_… key Feishu already holds")
	// The two that really do take a path, which is what keeps
	// completeNoFileDefault from turning file completion off for them.
	mustWire(cmd.MarkFlagFilename("image"))
	mustWire(cmd.MarkFlagFilename("file"))
}

func (o outgoingFlags) check() error {
	n := 0
	for _, v := range []string{o.text, o.markdown, o.image, o.file} {
		if strings.TrimSpace(v) != "" {
			n++
		}
	}
	if n != 1 {
		return fmt.Errorf("pass exactly one of --text, --markdown, --image or --file")
	}
	return nil
}

// resolve replaces a body that names a source with what that source holds.
// It runs after check has reduced the bodies to one, so stdin can only ever
// have a single claimant and needs no bookkeeping to keep them off each other.
func (o *outgoingFlags) resolve(in io.Reader) error {
	if err := resolveBody("--text", &o.text, in); err != nil {
		return err
	}
	return resolveBody("--markdown", &o.markdown, in)
}

// resolveBody rewrites one body in place: @path is a file, @@ is a literal @,
// and - is stdin. What a source holds is read once and never scanned again, so
// a file opening with @ sends as written rather than being read as a path.
// --image and --file are left out on purpose: they already take a path, and @
// in front of one has no second reading to pick from.
func resolveBody(flag string, v *string, in io.Reader) error {
	raw := *v
	if raw == "" {
		return nil
	}
	if rest, ok := strings.CutPrefix(raw, "@@"); ok {
		*v = "@" + rest
		return nil
	}
	src := raw
	if raw != "-" {
		path, ok := strings.CutPrefix(raw, "@")
		if !ok {
			return nil
		}
		if src = strings.TrimSpace(path); src == "" {
			return fmt.Errorf("%s: no file path after @", flag)
		}
	}
	body, err := readSource(flag+" "+raw, src, in)
	if err != nil {
		return err
	}
	*v = body
	return nil
}

// readSource reads a body out of the file src names, or out of stdin when it
// is -. label is how the caller spelled it, so the sentence points at what
// the reader typed rather than at the path it expanded to.
func readSource(label, src string, in io.Reader) (string, error) {
	var data []byte
	var err error
	if src == "-" {
		data, err = io.ReadAll(in)
	} else {
		var abs string
		if abs, err = expandPath(src); err == nil {
			data, err = os.ReadFile(abs)
		}
	}
	if err != nil {
		return "", fmt.Errorf("%s: %w", label, err)
	}
	body := cleanBody(data)
	if strings.TrimSpace(body) == "" {
		return "", fmt.Errorf("%s holds no message", label)
	}
	return body, nil
}

// cleanBody makes a read fit to send. A BOM would ride along as the message's
// invisible first character, and the newline a text file ends with would draw
// as a trailing blank line nobody typed.
func cleanBody(data []byte) string {
	return strings.TrimRight(strings.TrimPrefix(string(data), "\ufeff"), "\r\n")
}

// outgoing resolves the flags into a body. An image path is uploaded here
// because lark-cli's own --image refuses an absolute path.
func (o outgoingFlags) outgoing(ctx context.Context, client larkcli.Client, fetch sync.Fetcher) (larkcli.Outgoing, error) {
	switch {
	case strings.TrimSpace(o.markdown) != "":
		body, err := uploadMarkdownImages(ctx, client, fetch, o.markdown)
		if err != nil {
			return larkcli.Outgoing{}, err
		}
		return larkcli.Markdown(body), nil
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
	case strings.TrimSpace(o.file) != "":
		if larkcli.IsFileKey(o.file) {
			return larkcli.File(o.file), nil
		}
		path, err := expandPath(o.file)
		if err != nil {
			return larkcli.Outgoing{}, err
		}
		key, err := client.UploadFile(ctx, path)
		if err != nil {
			return larkcli.Outgoing{}, err
		}
		return larkcli.File(key), nil
	default:
		return larkcli.Text(o.text), nil
	}
}

// uploadMarkdownImages puts the pictures a markdown body names on Feishu and
// writes each reference to the key it came back as. The composer does this
// for a draft, and a body arriving through --markdown has the same pictures
// to send: without it the reference reaches Feishu naming a file only this
// machine can open. A reference inside a code span or a fence is left alone,
// because larkmd reads the body rather than matching it.
func uploadMarkdownImages(ctx context.Context, client larkcli.Client, fetch sync.Fetcher, src string) (string, error) {
	var err error
	out := larkmd.ReplaceImages(src, func(ref larkmd.ImageRef) string {
		if err != nil || larkcli.IsImageKey(ref.Dest) {
			return ref.Dest
		}
		key, ferr := uploadMarkdownImage(ctx, client, fetch, ref.Dest)
		if ferr != nil {
			// The line is what turns this back into a place to look: a body
			// read out of a file can name a great many pictures.
			err = fmt.Errorf("line %d: %w", ref.Line, ferr)
			return ref.Dest
		}
		return key
	})
	if err != nil {
		return "", err
	}
	return out, nil
}

// uploadMarkdownImage sends one picture up, downloading it first when the
// reference names a remote one: an upload takes a path, not an address.
func uploadMarkdownImage(ctx context.Context, client larkcli.Client, fetch sync.Fetcher, ref string) (string, error) {
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		path, err := sync.FetchToTemp(ctx, fetch, ref)
		if err != nil {
			return "", err
		}
		defer os.Remove(path)
		return client.UploadImage(ctx, path)
	}
	path, err := expandPath(ref)
	if err != nil {
		return "", err
	}
	// Answering here rather than letting the upload fail is what names the
	// reference the sender mistyped instead of the path it expanded to.
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("no such file: %s", ref)
	}
	return client.UploadImage(ctx, path)
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
	var idemKey string
	cmd := &cobra.Command{
		Use:               "reply <message_id> --text|--markdown|--image <body>",
		Short:             "Reply to a message, optionally inside its thread",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: a.completeMessageID,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := body.check(); err != nil {
				return &usageError{err}
			}
			if err := body.resolve(a.In); err != nil {
				return &usageError{err}
			}
			st, err := a.openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := context.Background()
			client := a.client()
			msg, err := body.outgoing(ctx, client, sync.HTTPFetch)
			if err != nil {
				return err
			}
			a.warnMarkdown(msg)
			sent, err := client.Reply(ctx, args[0], msg, inThread, idempotencyKey(idemKey))
			if err != nil {
				return err
			}
			a.ingestSent(ctx, st, sent)
			return a.printSent(sent, "")
		},
	}
	body.register(cmd)
	cmd.Flags().BoolVar(&inThread, "in-thread", false, "reply in the message's thread instead of the main chat")
	registerIdempotencyKey(cmd, &idemKey)
	return cmd
}

// warnMarkdown says what a markdown body loses on the way to Feishu. It never
// fails a send: what it names is content the sender chose, and it goes to
// stderr so a script reading the JSON on stdout is untouched.
func (a *App) warnMarkdown(msg larkcli.Outgoing) {
	for _, f := range larkmd.Lint(msg.Markdown) {
		fmt.Fprintf(a.Err, "larkim: %d:%d [%s] %s\n", f.Line, f.Column, f.Rule, f.Message)
		if f.Hint != "" {
			fmt.Fprintln(a.Err, "  hint:", f.Hint)
		}
	}
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
