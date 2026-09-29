// Package ai is the TUI assistant: it turns a chat transcript plus an
// instruction into a streamed Claude answer.
package ai

import (
	"cmp"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/amzyang/larkim/card"
	"github.com/amzyang/larkim/store"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// Client streams completions from Claude.
type Client struct {
	api   anthropic.Client
	model string
}

// New builds a client for the given Claude model id.
func New(apiKey, model string) *Client {
	return &Client{api: anthropic.NewClient(option.WithAPIKey(apiKey)), model: model}
}

// Chunk is one streamed piece of an answer; Done closes the stream.
type Chunk struct {
	Text string
	Err  error
	Done bool
}

const system = `You are an assistant embedded in a Feishu/Lark IM client. You are shown a transcript of one chat, newest last, with the user's own messages marked (me). A line starting with [image] is writing read out of the picture on the message above it, which the message text does not repeat. Answer in the language the chat mostly uses (Chinese if unsure). Be concrete and brief: names, decisions, deadlines, open questions. When asked to draft a reply, output only the reply text the user would send, nothing else.`

// Stream sends prompt with the chat transcript as context and streams the
// answer. Refusals are re-served by the API's default fallback model.
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
		user := "<transcript>\n" + transcript + "\n</transcript>\n\n" + prompt
		stream := c.api.Beta.Messages.NewStreaming(ctx, anthropic.BetaMessageNewParams{
			Model:        anthropic.Model(c.model),
			MaxTokens:    4096,
			System:       []anthropic.BetaTextBlockParam{{Text: system}},
			Messages:     []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(user))},
			OutputConfig: anthropic.BetaOutputConfigParam{Effort: anthropic.BetaOutputConfigEffortMedium},
			Fallbacks:    anthropic.BetaFallbacksParamOfDefault(),
			Betas:        []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01},
		})
		for stream.Next() {
			ev := stream.Current()
			if delta, ok := ev.AsAny().(anthropic.BetaRawContentBlockDeltaEvent); ok {
				if td, ok := delta.Delta.AsAny().(anthropic.BetaTextDelta); ok && td.Text != "" {
					if !send(Chunk{Text: td.Text}) {
						return
					}
				}
			}
		}
		if err := stream.Err(); err != nil {
			send(Chunk{Err: fmt.Errorf("claude: %w", err), Done: true})
			return
		}
		send(Chunk{Done: true})
	}()
	return out
}

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
	text := m.Content
	if c, ok := card.Parse(m.ContentRaw); ok {
		text = c.Markdown()
	} else if text == "" {
		text = m.ContentRaw
	}
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
