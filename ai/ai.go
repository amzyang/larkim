// Package ai is the TUI assistant: it turns a chat transcript plus an
// instruction into an answer streamed from a coding agent spoken to over ACP.
package ai

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/amzyang/larkim/card"
	"github.com/amzyang/larkim/store"
	acp "github.com/coder/acp-go-sdk"
)

// Client asks an ACP agent, one process per question.
type Client struct {
	argv  []string
	model string
	cwd   string
	log   *slog.Logger
}

// New builds a client that launches argv for each question and asks it to use
// model, or its own default when model is empty. cwd is the directory the
// agent's session runs in; it should be empty, since nothing there is meant
// to reach the model.
func New(argv []string, model, cwd string, log *slog.Logger) *Client {
	return &Client{argv: argv, model: model, cwd: cwd, log: log}
}

// Chunk is one streamed piece of an answer; Done closes the stream.
type Chunk struct {
	Text string
	Err  error
	Done bool
}

const system = `You are an assistant embedded in a Feishu/Lark IM client. You are shown a transcript of one chat, newest last, with the user's own messages marked (me). A line starting with [image] is writing read out of the picture on the message above it, which the message text does not repeat. Answer in the language the chat mostly uses (Chinese if unsure). Be concrete and brief: names, decisions, deadlines, open questions. When asked to draft a reply, output only the reply text the user would send, nothing else. Answer from the transcript alone: do not use tools.`

// Stream sends prompt with the chat transcript as context and streams the
// answer.
func (c *Client) Stream(ctx context.Context, transcript, prompt string) <-chan Chunk {
	out := make(chan Chunk, 64)
	go func() {
		defer close(out)
		ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		// A reader that walked away leaves the buffer full, so every send has
		// to be able to give up.
		send := func(ch Chunk) bool {
			select {
			case out <- ch:
				return true
			case <-ctx.Done():
				return false
			}
		}
		// ACP has no system prompt, so the instructions lead the one turn.
		text := system + "\n\n<transcript>\n" + transcript + "\n</transcript>\n\n" + prompt
		if err := c.ask(ctx, text, func(t string) bool { return send(Chunk{Text: t}) }); err != nil {
			send(Chunk{Err: fmt.Errorf("%s: %w", filepath.Base(c.argv[0]), err), Done: true})
			return
		}
		send(Chunk{Done: true})
	}()
	return out
}

// ask runs one prompt turn in a fresh agent process, handing each piece of
// the reply to emit as it arrives.
func (c *Client) ask(ctx context.Context, text string, emit func(string) bool) (err error) {
	ctx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(ctx, c.argv[0], c.argv[1:]...)
	cmd.Dir = c.cwd
	// A turn that ended has nothing more to say, so the agent is stopped
	// rather than waited on; SIGTERM lets it take its own children down.
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 3 * time.Second
	stderr := &tail{max: 4 << 10}
	cmd.Stderr = stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return err
	}
	defer func() {
		cancel()
		_ = cmd.Wait()
		// What the agent printed on its way down is usually the reason: a
		// missing login, a model it does not have.
		if err != nil {
			if msg := strings.TrimSpace(stderr.String()); msg != "" {
				err = fmt.Errorf("%w\n%s", err, msg)
			}
		}
	}()

	conn := acp.NewClientSideConnection(handler{emit: emit}, stdin, stdout)
	conn.SetLogger(c.log)
	// No fs or terminal capability is offered: the transcript is all the
	// agent is meant to read.
	if _, err := conn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientInfo:      &acp.Implementation{Name: "larkim", Version: "1"},
	}); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	sess, err := conn.NewSession(ctx, acp.NewSessionRequest{Cwd: c.cwd, McpServers: []acp.McpServer{}})
	if err != nil {
		return fmt.Errorf("new session: %w", err)
	}
	if c.model != "" {
		id, value, err := pickModel(sess.ConfigOptions, c.model)
		if err != nil {
			return err
		}
		if _, err := conn.SetSessionConfigOption(ctx, acp.SetSessionConfigOptionRequest{
			ValueId: &acp.SetSessionConfigOptionValueId{SessionId: sess.SessionId, ConfigId: id, Value: value},
		}); err != nil {
			return fmt.Errorf("set model %s: %w", value, err)
		}
	}
	resp, err := conn.Prompt(ctx, acp.PromptRequest{SessionId: sess.SessionId, Prompt: []acp.ContentBlock{acp.TextBlock(text)}})
	if err != nil {
		return fmt.Errorf("prompt: %w", err)
	}
	if resp.StopReason != acp.StopReasonEndTurn {
		return fmt.Errorf("stopped: %s", resp.StopReason)
	}
	return nil
}

// pickModel finds the value of the agent's model option that want names: the
// exact value id, or else the one value whose id or name contains it.
func pickModel(opts []acp.SessionConfigOption, want string) (acp.SessionConfigId, acp.SessionConfigValueId, error) {
	i := slices.IndexFunc(opts, func(o acp.SessionConfigOption) bool {
		return o.Select != nil && o.Select.Category != nil && *o.Select.Category == acp.SessionConfigOptionCategoryModel
	})
	if i < 0 {
		return "", "", fmt.Errorf("model %q: the agent offers no model choice", want)
	}
	sel := opts[i].Select
	values := selectValues(sel.Options)
	if j := slices.IndexFunc(values, func(v acp.SessionConfigSelectOption) bool { return string(v.Value) == want }); j >= 0 {
		return sel.Id, values[j].Value, nil
	}
	lower := strings.ToLower(want)
	var hits []acp.SessionConfigSelectOption
	for _, v := range values {
		if strings.Contains(strings.ToLower(string(v.Value)), lower) || strings.Contains(strings.ToLower(v.Name), lower) {
			hits = append(hits, v)
		}
	}
	if len(hits) == 1 {
		return sel.Id, hits[0].Value, nil
	}
	have, verb := values, "is not offered"
	if len(hits) > 1 {
		have, verb = hits, "matches several models"
	}
	ids := make([]string, len(have))
	for k, v := range have {
		ids[k] = string(v.Value)
	}
	return "", "", fmt.Errorf("model %q %s; have: %s", want, verb, strings.Join(ids, ", "))
}

// selectValues flattens a select's options, grouped or not.
func selectValues(o acp.SessionConfigSelectOptions) []acp.SessionConfigSelectOption {
	switch {
	case o.Ungrouped != nil:
		return *o.Ungrouped
	case o.Grouped != nil:
		var all []acp.SessionConfigSelectOption
		for _, g := range *o.Grouped {
			all = append(all, g.Options...)
		}
		return all
	}
	return nil
}

// handler is the client side of the connection: it forwards the reply's text
// and refuses everything else the agent could ask of it.
type handler struct {
	emit func(string) bool
}

var _ acp.Client = handler{}

func (h handler) SessionUpdate(_ context.Context, n acp.SessionNotification) error {
	// Thoughts, tool calls and plans are the agent's working, not the answer.
	if u := n.Update.AgentMessageChunk; u != nil && u.Content.Text != nil && u.Content.Text.Text != "" {
		h.emit(u.Content.Text.Text)
	}
	return nil
}

// RequestPermission turns down every tool call, preferring the agent's own
// reject option so it carries on answering rather than abandoning the turn.
func (handler) RequestPermission(_ context.Context, p acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	i := slices.IndexFunc(p.Options, func(o acp.PermissionOption) bool {
		return o.Kind == acp.PermissionOptionKindRejectOnce || o.Kind == acp.PermissionOptionKindRejectAlways
	})
	if i < 0 {
		return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, nil
	}
	return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected(p.Options[i].OptionId)}, nil
}

func (handler) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, acp.NewMethodNotFound("fs/read_text_file")
}

func (handler) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, acp.NewMethodNotFound("fs/write_text_file")
}

func (handler) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, acp.NewMethodNotFound("terminal/create")
}

func (handler) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, acp.NewMethodNotFound("terminal/kill")
}

func (handler) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, acp.NewMethodNotFound("terminal/output")
}

func (handler) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, acp.NewMethodNotFound("terminal/release")
}

func (handler) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, acp.NewMethodNotFound("terminal/wait_for_exit")
}

// tail keeps the last max bytes written to it: the end of a failing agent's
// stderr is where it says why.
type tail struct {
	max int
	b   []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if over := len(t.b) - t.max; over > 0 {
		t.b = slices.Clone(t.b[over:])
	}
	return len(p), nil
}

func (t *tail) String() string { return string(t.b) }

// Transcript renders messages (oldest first) as plain lines for the model.
// imgText carries the writing read out of each message's pictures, keyed by
// message id; a nil map leaves them as the placeholders the renderer wrote.
func Transcript(chatName string, msgs []store.Message, self string, imgText map[string][]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "chat: %s\n", chatName)
	for _, m := range msgs {
		if line := Line(m, self, imgText); line != "" {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// Line is one message as the model reads it: when it was sent, who sent it and
// what it says, with a card flattened to the document it draws and a body no
// renderer has reached yet falling back to the raw one. A recalled message has
// no line: it is gone from the conversation the reader is looking at too.
func Line(m store.Message, self string, imgText map[string][]string) string {
	if m.Deleted {
		return ""
	}
	who := cmp.Or(m.SenderName, m.SenderID)
	if m.SenderID == self {
		who += " (me)"
	}
	text, _ := card.MessageText(m.ContentRaw, m.Content, m.RenderedAt > 0)
	line := fmt.Sprintf("[%s] %s: %s", time.UnixMilli(m.CreateMs).Local().Format("01-02 15:04"), who, strings.TrimSpace(text))
	// Appended rather than substituted into the placeholder: a picture inside
	// a post, a card or a forwarded bundle is named nowhere the rendering
	// spells out, and this reaches those too.
	for _, t := range imgText[m.MessageID] {
		if t = excerpt(t); t != "" {
			line += "\n" + imageMark + " " + t
		}
	}
	return line
}

// imageMark opens the continuation line a picture's writing arrives on.
const imageMark = "    [image]"

// imageTextMax bounds one picture's contribution in runes. It is a guard
// against the screenshot of a whole document rather than a summary: an
// ordinary screenshot of a console or a schedule comes in well under it, and
// cutting those to a headline would leave the model the window chrome the
// recognizer reads first.
const imageTextMax = 1000

// excerpt flattens a picture's regions onto one line and cuts it to length.
func excerpt(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > imageTextMax {
		s = strings.TrimSpace(string(r[:imageTextMax])) + "…"
	}
	return s
}

// Prompt maps the TUI's :ai forms onto an instruction. draft reports that the
// answer is a reply to place in the composer.
func Prompt(input string) (prompt string, draft bool) {
	input = strings.TrimSpace(input)
	head, rest, _ := strings.Cut(input, " ")
	switch strings.ToLower(head) {
	case "", "summary", "summarize", "总结":
		return "Summarize this chat: what was discussed, decisions, action items with owners, and anything that needs my reply.", false
	case "draft", "reply", "回复":
		instr := strings.TrimSpace(rest)
		if instr == "" {
			instr = "a suitable reply to the latest messages addressed to me"
		}
		return "Draft " + instr + ". Output only the message text.", true
	case "todo", "actions", "待办":
		return "List every action item or request directed at me in this chat, newest first, with who asked and when.", false
	}
	return input, false
}
