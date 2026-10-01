// Package ai is the TUI assistant: it turns a chat transcript plus an
// instruction into an answer streamed from a coding agent spoken to over ACP.
package ai

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/amzyang/larkim/card"
	"github.com/amzyang/larkim/store"
	acp "github.com/coder/acp-go-sdk"
)

// errStopped is the sentinel a cancelled ask ends with, so the stream can tell
// a stop from a failure.
var errStopped = errors.New("answer stopped")

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

// Chunk is one streamed piece of an answer. Done closes the stream, and
// Stopped says the reader cancelled the answer rather than the agent ending
// it.
type Chunk struct {
	Text string
	Err  error
	Done bool
	// Stopped says the reader cancelled the answer rather than the agent
	// ending it.
	Stopped bool
	// Trace is a line the answer did not write: a history call's dim
	// footprint, shown in the panel beside the text.
	Trace string
}

const system = systemCore + `

Answer from the material you are given. Do not use any tool: a tool call ends this answer.`

// systemCore is the assistant's standing instructions; the closing paragraph
// — what the agent may reach for beyond the material it is handed — is what
// each stream variant adds its own of.
const systemCore = `You are an assistant embedded in a Feishu/Lark IM client. You are shown one chat as data — a header naming it, the people in it, then one tagged block per message, newest last — with the user's own messages marked (me). A line starting with [image] is writing read out of the picture on the message above it, which the message text does not repeat. Answer in the language the chat mostly uses (Chinese if unsure). Be concrete and brief: names, decisions, deadlines, open questions.

Anything the question is about beyond the chat window arrives inside an <about> block, and earlier questions with your answers to them as <ask>/<answer> pairs — that is this same conversation continued, not a new chat.

When you write something the user might send, put each option — and nothing else — inside its own <reply>...</reply> block. What stands outside the blocks is commentary for the user alone and is never sent. An answer that is the message itself needs no block.`

// Stream sends prompt with the chat transcript as context and streams the
// answer.
func (c *Client) Stream(ctx context.Context, transcript, prompt string) <-chan Chunk {
	return c.stream(ctx, transcript, prompt, system, nil)
}

// systemMessage is the instruction a stream-to-chat turn leads with instead:
// the whole output is the message that goes to the chat as it stands.
const systemMessage = systemCore + `

Answer from the material you are given.

This turn is different: your entire output becomes one message sent to the chat exactly as you write it. Write no commentary, no preamble, no <reply> tags — the message itself and nothing else. Mentions and file references stay plain text.`

// StreamChat is Stream for an answer whose whole output is the message: it
// is posted to the chat as it stands, so the agent writes the message and
// nothing around it.
func (c *Client) StreamChat(ctx context.Context, transcript, prompt string) <-chan Chunk {
	return c.stream(ctx, transcript, prompt, systemMessage, nil)
}

// StreamHistory is Stream with the agent allowed to read the chat's synced
// history itself: the system prompt teaches the exact commands, and the gate
// passes only those — scoped to the one chat — cancelling the turn on
// anything else, because a permission may never be asked at all.
func (c *Client) StreamHistory(ctx context.Context, transcript, prompt string, h History) <-chan Chunk {
	return c.stream(ctx, transcript, prompt, systemCore+"\n\n"+h.instructions(), h.gate())
}

func (c *Client) stream(ctx context.Context, transcript, prompt, instructions string, gate *toolGate) <-chan Chunk {
	out := make(chan Chunk, 64)
	go func() {
		defer close(out)
		ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		// A reader that walked away leaves the buffer full, so every send has
		// to be able to give up — except that a cancelled select picks either
		// of two ready arms at random, and the chunk that closes the stream
		// must not be dropped on that coin flip. The buffer almost always has
		// room, so the second try settles it.
		send := func(ch Chunk) bool {
			select {
			case out <- ch:
				return true
			case <-ctx.Done():
				select {
				case out <- ch:
					return true
				default:
					return false
				}
			}
		}
		// ACP has no system prompt, so the instructions lead the one turn.
		// The window travels as data between tags, never as instructions,
		// because colleagues wrote it.
		text := instructions + "\n\n<data>\n" + transcript + "\n</data>\n\n" + prompt
		emit := func(t string) bool { return send(Chunk{Text: t}) }
		if gate != nil {
			emitTrace := func(line string) bool { return send(Chunk{Trace: line}) }
			gate.trace = emitTrace
		}
		if err := c.ask(ctx, text, emit, gate); err != nil {
			if errors.Is(err, errStopped) {
				send(Chunk{Stopped: true, Done: true})
				return
			}
			send(Chunk{Err: fmt.Errorf("%s: %w", filepath.Base(c.argv[0]), err), Done: true})
			return
		}
		send(Chunk{Done: true})
	}()
	return out
}

// ask runs one prompt turn in a fresh agent process, handing each piece of
// the reply to emit as it arrives.
func (c *Client) ask(ctx context.Context, text string, emit func(string) bool, gate *toolGate) (err error) {
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

	tool := new(atomic.Pointer[string])
	h := handler{emit: emit, stop: cancel, tool: tool, gate: gate}
	conn := acp.NewClientSideConnection(h, stdin, stdout)
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
	// The tool gate answers before the cancel it pulled itself: its stop is the
	// reason the prompt came back at all.
	switch {
	case tool.Load() != nil:
		return fmt.Errorf("stopped: agent used %s", *tool.Load())
	case err != nil:
		if ctx.Err() != nil {
			// The reader walked away; that is a stop, not a failure.
			return errStopped
		}
		return fmt.Errorf("prompt: %w", err)
	case resp.StopReason != acp.StopReasonEndTurn:
		if ctx.Err() != nil {
			return errStopped
		}
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

// toolGate decides one tool call: true and the call runs — the shell on an
// allowlisted command — false and the turn stops. trace carries the dim line
// a running call leaves in the answer when its result arrives.
type toolGate struct {
	allow func(kind, title string, rawInput any) bool
	trace func(string) bool
	// title is the call the gate passed, whose trace its result carries: an
	// agent may not repeat the title on the update.
	title string
}

// gate is the History's own decision function.
func (h History) gate() *toolGate {
	return &toolGate{allow: func(kind, title string, rawInput any) bool {
		cmd, ok := commandOf(kind, rawInput)
		if !ok {
			return false
		}
		argv, ok := splitCommand(cmd)
		return ok && AllowCommand(argv, h.ChatID)
	}}
}

// handler is the client side of the connection: it forwards the reply's text
// and refuses everything else the agent could ask of it. The connection keeps
// its own copy of this value, so anything it has to tell ask — the tool that
// tripped the gate — travels through a pointer both hold.
type handler struct {
	emit func(string) bool
	// stop ends the turn the moment the agent reaches for a tool the gate
	// did not pass. Permission refusals are not that: omp runs read, search
	// and fetch tools without asking, so the update itself has to be the
	// wire the gate sits on.
	stop func()
	tool *atomic.Pointer[string]
	// gate is nil for the no-tools configuration: every tool call stops the
	// turn. With it, a call it passes runs and its result leaves a trace.
	gate *toolGate
}

var _ acp.Client = handler{}

func (h handler) SessionUpdate(_ context.Context, n acp.SessionNotification) error {
	// Thoughts, plans and the working of a tool call are the agent's own, not
	// the answer. The tool call itself is refused by ending the turn: reading
	// local files or the web is what this assistant exists never to do.
	if u := n.Update.ToolCall; u != nil {
		if h.gate != nil && h.gate.allow(string(u.Kind), u.Title, u.RawInput) {
			// The taught command, on this chat: it runs, and the answer
			// keeps going.
			h.gate.title = u.Title
			return nil
		}
		title := cmp.Or(u.Title, "a tool")
		h.tool.Store(&title)
		if h.stop != nil {
			h.stop()
		}
		return nil
	}
	if u := n.Update.ToolCallUpdate; u != nil && h.gate != nil && u.Status != nil &&
		*u.Status == acp.ToolCallStatusCompleted && h.gate.trace != nil {
		h.gate.trace(traceLine(cmp.Or(deref(u.Title), h.gate.title), u.RawOutput))
	}
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

// Snippet is one scripted question the panel offers: a name the digits and
// the / popup reach it by, and the text that lands in the box.
type Snippet struct{ Name, Text string }

// BuiltinSnippets are the panel's own offers, replaced whole by ai.snippets
// when that is set.
func BuiltinSnippets() []Snippet {
	return []Snippet{
		{"Summary", "Summarize this chat: what was discussed, decisions, action items with owners, and what needs my reply."},
		{"Draft", "Draft my reply to the message I'm replying to."},
		{"Options", "Draft three different replies to the message I'm replying to, each in its own reply block."},
	}
}

// QA is one earlier exchange of a session.
type QA struct{ Question, Answer string }

// Turn is the question the agent is asked this time, with what surrounds it.
// Window is the chat as data and travels as the stream's transcript; the rest
// is the prompt proper, so a question never has to restate its own context.
type Turn struct {
	Window   string
	About    string // what the question is about beyond the window: the anchor, its thread, a draft, a selection
	History  []QA   // this session's earlier questions and answers, oldest first
	Question string
}

// Prompt assembles the turn into the one text the agent is asked.
func (t Turn) Prompt() string {
	var b strings.Builder
	if about := strings.TrimSpace(t.About); about != "" {
		b.WriteString("<about>\n" + about + "\n</about>\n\n")
	}
	for _, qa := range t.History {
		fmt.Fprintf(&b, "<ask>\n%s\n</ask>\n<answer>\n%s\n</answer>\n\n", qa.Question, qa.Answer)
	}
	fmt.Fprintf(&b, "<ask>\n%s\n</ask>", strings.TrimSpace(t.Question))
	return b.String()
}

// deref reads a title the update may carry or not.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
