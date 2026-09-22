// Package ai is the TUI assistant: it turns a chat transcript plus an
// instruction into a streamed Claude answer.
package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/amzyang/larkim/store"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// Client streams completions from Claude.
type Client struct {
	api   anthropic.Client
	model string
}

// New builds a client; model defaults to Claude Opus 5.
func New(apiKey, model string) *Client {
	if model == "" {
		model = "claude-opus-5"
	}
	return &Client{api: anthropic.NewClient(option.WithAPIKey(apiKey)), model: model}
}

// Chunk is one streamed piece of an answer; Done closes the stream.
type Chunk struct {
	Text string
	Err  error
	Done bool
}

const system = `You are an assistant embedded in a Feishu/Lark IM client. You are shown a transcript of one chat, newest last, with the user's own messages marked (me). Answer in the language the chat mostly uses (Chinese if unsure). Be concrete and brief: names, decisions, deadlines, open questions. When asked to draft a reply, output only the reply text the user would send, nothing else.`

// Stream sends prompt with the chat transcript as context and streams the
// answer. Refusals are re-served by the API's default fallback model.
func (c *Client) Stream(ctx context.Context, transcript, prompt string) <-chan Chunk {
	out := make(chan Chunk, 64)
	go func() {
		defer close(out)
		ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
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
					select {
					case out <- Chunk{Text: td.Text}:
					case <-ctx.Done():
						return
					}
				}
			}
		}
		if err := stream.Err(); err != nil {
			out <- Chunk{Err: fmt.Errorf("claude: %w", err), Done: true}
			return
		}
		out <- Chunk{Done: true}
	}()
	return out
}

// Transcript renders messages (oldest first) as plain lines for the model.
func Transcript(chatName string, msgs []store.Message, self string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "chat: %s\n", chatName)
	for _, m := range msgs {
		if m.Deleted {
			continue
		}
		who := m.SenderName
		if who == "" {
			who = m.SenderID
		}
		if m.SenderID == self {
			who += " (me)"
		}
		text := m.Content
		if text == "" {
			text = m.ContentRaw
		}
		fmt.Fprintf(&b, "[%s] %s: %s\n", time.UnixMilli(m.CreateMs).Local().Format("01-02 15:04"), who, strings.TrimSpace(text))
	}
	return b.String()
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
