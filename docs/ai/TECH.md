# AI panel — TECH

Implements [PRD.md](PRD.md); behavior lives there. This is how the shipped
panel works and the measured facts it rests on.

## Where things live

- `tui/assistant.go` — the panel: sessions, turns, cards, snippets, sends,
  the stream-to-chat writer, and the keys the column owns.
- `ai/ai.go` — the ACP client: one process per question, the system-prompt
  variants (plain, message, history), and the tool gate. `ai/split.go` — the
  `<reply>` parser. `ai/history.go` — the taught commands and the
  allowlist the gate enforces.
- `store/ai.go` + migrations `0046_ai_sessions.sql`, `0047_ai_turn_card.sql` —
  `ai_sessions`/`ai_turns`, TUI-owned like `drafts` (see docs/SCHEMA.md).
- `larkcli` — `Outgoing.Card` (msg_type interactive, one markdown element),
  `PatchMessage` (the whole-card rewrite), and the width-one `LaneCard` the
  rewrites queue in.
- `config` — `ai.snippets`, `ai.history`; `:config` documents both.

## The stream writer

One card per streamed answer: posted at once under the user's own name (a
reply inside the anchor's thread when there is one), then rewritten whole
with the text so far. Feishu has no sequence for concurrent rewrites of one
message and meters them per message, so one write is in flight per card and
each write carries the full text — latest wins, last write is the final
text. A stopped or failed answer closes the card with `(interrupted)`, past
30 KB with a note that the rest is in larkim. The posted message id rides on
the turn (`ai_turns.card_id`), so Jump and Recall reach the card after a
restart.

## The history gate

`ai.history: true` teaches the agent the exact read-only
`larkim --config … messages list … --json` invocations (the same continuation
command `Y` copies, `--query` for full-text) and passes only those, scoped to
the session's chat: any other command, any shell metacharacter that would
run more than the one command, any tool that is not the shell, cancels the
turn. Off means no tools at all. Run the agent with `--tools bash` when on
and `--no-tools` when off; the gate holds regardless of argv.

## Measured constraints

### 2026-10-01 (omp 18.4.6, acp-go-sdk v0.13.5, self p2p)

1. `--tools` and `--no-tools` are honoured under `--mode acp`. A prompt under
   `--no-tools` produced 0 tool calls; under `--tools bash` exactly 1.
2. **RequestPermission is not guaranteed.** With default settings omp's shell
   ran with no permission request at all — enforcement therefore keys off the
   `tool_call` update, never off permissions.
3. A user-identity **reply** with `msg_type: interactive` accepts `PATCH`
   rewrites, so a streamed card can quote-reply and still stream.
4. ctrl+s reaches a pane through kitty + herdr.
5. Serial rewrites of one message run at about 1.7/s without tripping the
   frequency limit; 8 concurrent rewrites were refused — hence the serial
   writer.

### 2026-10-02 (live walk, dev data dir)

1. `lark-cli api` reads a stdin body only with `--data -`; without the flag
   the request goes out empty and the message PATCH fails with
   `99992402 field validation failed` — the failure is silent from the card's
   point of view, so verify `update_time` advances, not just the send.
2. A y/n armed from INSERT mode does not own the keys: arming must leave the
   box, or the answer's first letter lands in the question.
3. The streamed card's expansion (`body.content`) is always the
   「请升级至最新版本客户端」 placeholder; the real text is only in the raw
   card content (`card_msg_content_type=raw_card_content`).
