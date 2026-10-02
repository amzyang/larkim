# AI panel

The assistant is a panel of its own, keyed to the open chat: `a` opens it without generating anything, sessions
isolate conversations per chat, the context a question is about is visible and editable, and every answer can be
sent, replied with, or put into the composer in one action. Terminology follows the Lark client (Send, Reply,
Insert, Replace, Copy, Regenerate, Stop); an AI conversation is called a *session*, since "topic" already means
topic groups.

## Opening

- `a` opens the AI column on the chat's latest session, cursor in the AI input (INSERT). Nothing is generated.
  With no session yet it opens an empty one, which is stored only when its first question is asked.
- `A` does the same with a new session.
- The anchor is the message the next question is about:
  - from the messages or thread pane: the message under the cursor;
  - from a composer: that composer's quote;
  - from the chats pane: none, and `a` opens the highlighted chat first.
- In VISUAL, `a` takes the selection as context and leaves VISUAL.
- In Unread, the session belongs to the chat of the message under the cursor.
- `:ai` alone is `a`. `:ai <snippet>` or `:ai <text>` starts a new session and asks at once. Completion offers the
  snippet names.
- `a` while the column is open moves focus to its input and re-takes the anchor.
- The column draws over a thread frame without closing it: Esc closes the panel and the thread is back with its
  draft, focused. Opening another frame hides the panel, and so does the chats cursor landing on a thread row. Its
  answers keep running.
- Agent not installed: the panel opens and shows its sessions, and asking shows the "assistant off" notice.
  No chat open: "open a chat first".

## Layout

```
╭ AI · Summary · Draft 张三 · + ───────╮   header: this chat's sessions
│ ↩ 张三: 发布单合了吗 · ▤ last 10       │   context chips for the next question
│ ─────────────────────────────────── │
│ You 14:35  ↩ 张三 · ▤ 80              │   the question and what it carried
│ 帮他们写个回复                         │
│ AI 14:35                Regenerate  │   head: Stop / Regenerate / Retry
│ ┌─────────────────────────────────┐ │
│ │ 好的，今晚合 #4412                │ │   a <reply> block is a card
│ └ Send · Reply · Insert · Copy ───┘ │
╰─────────────────────────────────────╯
╭─────────────────────────────────────╮
│                         / snippets  │   snippet popup
│ > Ask about this chat…              │   AI input
╰─────────────────────────────────────╯
```

- The header has one tab per session, in creation order. The current tab is highlighted and an answering tab shows
  a spinner. Tabs that don't fit collapse to `‹ n` / `n ›`. `+` starts a new session.
- Context chips:
  - `↩ <sender>: <first line>` anchor;
  - `⤷ thread · n replies` when the anchor is in a thread or is a thread root;
  - `▤ last N` window;
  - `✎ draft` when the anchor's composer holds text;
  - `☰ n selected` from VISUAL.

  Each has `×` except the window. They wrap to at most two rows.
- Snippets take one row of the band. Those that don't fit are reached with `/`.
- An empty session shows only the placeholder "Ask about this chat…".

## Sessions

- A session belongs to one chat_id. The header lists only that chat's sessions, and the panel follows the open
  chat.
- Sessions are kept in larkim's database. The TUI owns them, like drafts: the daemon never writes them, and
  docs/SCHEMA.md documents the tables.
- The title is the first line of the first question, or the snippet name, plus the anchor's sender.
- `[` / `]` or a click switches sessions. `D` deletes one after y/n. Rename, search and export are not built.

## Asking

- Enter asks. Each session has at most one answer in flight; sessions don't block each other.
- Each turn the agent gets, all as data rather than instructions:
  - the chat's latest `ai.context` messages (default 10), read from the store when you ask, so a follow-up sees
    messages that arrived since;
  - the text read out of pictures;
  - the turn's chips: the anchor, its thread replies, the anchor's composer draft, the VISUAL selection;
  - the session's last 10 questions and answers;
  - the question.
- Each question records its context and shows it as a dim marker line. The record holds the anchor, the thread, the
  window size, the draft's text and the selection.
- Later actions on its answer, Retry and Regenerate included, use that record and the window as it stood when the
  question was asked. They never use the live cursor.
- Chips carry over to the next question until dropped (`×`, ctrl+r) or re-taken with `a`. The draft chip follows the
  composer.
- An answer keeps going after Esc, a chat switch or a hidden panel, and finishes into its session. A status notice
  says when an answer finishes out of view.
- `x` stops an answer. A failed answer shows the error, and a stopped one shows "stopped"; both offer Retry.
- Quitting stops answers in flight. Their questions stay, and each answer shows "interrupted · Retry".
- An answer has three minutes, as today.

## Answers

- Answers render as Markdown, through the renderer posts use.
- The agent puts text meant for sending inside `<reply>…</reply>`, one block per option, and each block becomes a
  card. An answer with no block is a single card.
- A card shows its text the way Send would post it, through the composer preview's rendering.
- Actions appear once the answer is finished, so streaming text can't move a target under the mouse.

## Actions

| Action | Key | Where shown | Result |
| --- | --- | --- | --- |
| Send | `s` | every card | y/n `send to 平台组?`, then posts a new message |
| Reply | `S` | cards whose question had an anchor | y/n `reply to 张三?`, then replies to the anchor, inside its thread when it is in one |
| Insert / Replace | Enter, `r` | every card | puts the text in a box with the anchor quoted and focuses it; Enter there sends |
| Insert in thread | `R` | key only, with an anchor | the same, with the quote set to reply in thread |
| Copy | `yy` | every card | card text to the clipboard. `Y` copies the whole answer as Markdown |
| Regenerate | `.` | head of the last answer | replaces that answer |
| Stop | `x` | head while answering | stops the answer |

- `r` and `R` start a reply, as they do on a message. Only `s` and `S` send from the panel.
- The y/n prompt names the chat and the target, shows the first line of the text, and says when it mentions @all.
- Insert fills:
  - the thread frame's box, when the anchor's thread is the frame under the panel;
  - otherwise the chat's box, quoting the anchor (in thread when the anchor is in one).

  When the layout is folded, the panel and the frame under it close so the chat's box is on screen.
- The label is Replace when the question carried the draft chip, and the box's text is swapped. Otherwise an empty
  box is filled and a non-empty one gets the text at the cursor.
- Send and Reply go through the same path Enter in the composer uses: the same conversion of the text, the same
  pending bubble, and `.` / `x` when the send fails. Two differences:
  - The text names a local file: the prompt shows it, and the choice is yours — `y` sends with the upload, `n`
    sends without it (the reference stays text), Esc cancels. Nothing uploads without the `y`.
  - `@name` resolves only when the destination is the open chat and its members are loaded. Otherwise the name stays
    text, and the prompt says so.
- A sent card shows `✓ sent 14:36` until the panel closes. Sending it again asks again.

## Snippets

| Name | Inserts |
| --- | --- |
| Summary | Summarize this chat: what was discussed, decisions, action items with owners, and what needs my reply. |
| Draft | Draft my reply to the message I'm replying to. |
| Options | Draft three different replies to the message I'm replying to, each in its own reply block. |

- `ai.snippets` in the config (a list of `{name, text}`) replaces the built-ins when it is set.
- Inserting never sends. There are three ways in: click a chip, press `1`-`9` with the AI column focused, or type `/`
  at the start of the AI input, which opens a popup filtered as you type (the matching used for chat names, pinyin
  included). Tab or Enter picks from the popup and Esc dismisses it.
- `:ai summary` and `:ai draft` map to the snippets of those names.

## Stream to chat

- ctrl+s in the AI input asks y/n `stream the answer into 平台组 as a reply to 张三?`, then asks the agent. Without an
  anchor the answer goes out as a new message.
- The answer is posted at once as one card under your name. The card is rewritten with the text so far about twice a
  second (stream-card route A).
- The agent is told the whole output is the message itself, so it writes no commentary and no `<reply>` tags.
  Mentions and local file references in a streamed answer stay text.
- The final state is the full text. A stopped or failed answer is marked `(interrupted)`. Past 30 KB the card closes
  with a note that the rest is in larkim. Nothing is left half-written.
- In the panel the card shows `● live in chat`, then Jump · Copy · Recall. Recall goes through the existing Recall
  confirmation.
- If the first post fails, the answer stays in the panel marked "not posted" and offers Send. If the final rewrite
  fails, it offers Retry. Recipients see an ordinary card with no "edited" mark.

## History tools

- `ai.history` (default `off`) lets the agent read this chat's synced history itself, through larkim's own CLI:
  - the system prompt teaches the exact commands — `larkim --config <path> messages list --chat <id> --before …
    --json`, the same continuation command `Y` puts on the clipboard, and search;
  - the only tool call allowed is the shell running one of those, with `--chat` matching the session. Every other
    tool call stops the answer.
- Permissions are not the gate: omp may not ask at all (measured: a bash call ran with no permission request), so
  the answer watches its own tool calls, and anything but an allowlisted command cancels the turn.
- With `ai.history` on, run the agent as `omp --mode acp --tools bash`: the agent is left with nothing but the
  shell. With it off, `--no-tools` is the quiet configuration.
- Every call shows as a dim line in the answer (`⌕ messages list · 40 rows`).
- Off means no tools at all. A `⌕ history` chip shows when it is on.
- No MCP server, no skill to install, no new dependency: the CLI already exists and the instructions are injected
  per turn.

## Safety

- What reaches the agent: what "Asking" lists, plus what History tools return when on. What leaves the machine: the
  same, sent to the agent's provider.
- With History tools off, every permission is refused and any tool call stops the turn (`stopped: agent used read`).
- With them on, the only tool call allowed is the shell running an allowlisted larkim read-only command for this
  chat. Anything else stops the turn.
- Reason: over ACP, omp 18.4.6 asks permission only for bash, edit, delete and move, and runs read, search, fetch and
  MCP tools without asking (seen in its bundled source, tool table `{bash, edit, delete, move}`) — and even the
  gated four run unasked when omp's own settings allow them. So refusing permissions does not keep an injected
  instruction away from local files.
- Writing to Feishu always takes y/n.

## Keys

NORMAL, AI column focused:

| Keys | Action |
| --- | --- |
| `j` `k` `^d` `^u` `gg` `G` | move between cards, scroll |
| Enter, `r` / `R` | Insert / Insert in thread |
| `s` / `S` | Send / Reply, each after y/n |
| `h` | focus the messages pane; the frame under the panel stays |
| `yy` / `Y` | copy the card / the whole answer |
| `.` / `x` | Regenerate (Retry) / Stop |
| `i` | the AI input |
| `1`-`9` | insert a snippet and focus the input |
| `[` / `]` / `A` / `D` | previous / next / new / delete session |
| `o` | open a link in the card |
| Esc | close the panel |

AI input, INSERT:

| Keys | Action |
| --- | --- |
| Enter | ask |
| shift+enter, alt+enter, ctrl+j | newline |
| ctrl+s | ask into chat |
| `/` at the start | snippet popup |
| ctrl+r | drop the last chip |
| Esc | back to NORMAL |

Paste works as it does in every input.

Mouse: tabs and `+`, chip `×`, snippet chips, card actions, Regenerate, Stop, Jump, Recall. The wheel scrolls the
turn list.

## Not building

- Citations.
- Choosing the agent per session.
- Renaming, searching or exporting sessions.
- Like / Dislike.
- Editing an earlier question.
- Sending images to the agent.
- CardKit typewriter streaming (route B).
- History tools across chats.
- Anything that generates when the panel opens.

## Acceptance

- `a`, `A` and `:ai` alone start no agent (the fake AIStreamer records 0 calls). Entering focuses the AI input, and
  the chips match the anchor rules.
- A question stores its question and answer, and a restart shows them under the right chat. Another chat's header
  doesn't list them.
- The prompt holds the store window, the anchor's id and its thread replies. A follow-up adds the earlier turns, and
  messages that arrived after the first turn are in the second turn's window.
- Esc, a chat switch or the info pane mid-answer don't cancel the request, and the answer lands in its own session.
  `x` cancels it. No path writes model output into a composer except Insert and Replace.
- `<reply>` parsing handles zero blocks, several blocks, an unclosed block while streaming, and a block inside a code
  fence.
- Insert:
  - fills the chat box with the quote set to the anchor (in thread for a thread anchor);
  - fills the frame's box when the anchor's thread is the frame under the panel;
  - Replace swaps the text when the draft chip was carried;
  - `r` on a card never sends.
- Keys on the AI column never act on the hidden frame's message.
- Regenerate rebuilds the same prompt: the window is cut at the time the question was asked.
- Quitting mid-answer cancels the agent, and the turn loads as interrupted.
- Send and Reply:
  - `y` records exactly one send in `larkcli.Fake` with the right chat, reply_to and in_thread;
  - `n` and Esc record none;
  - the pending bubble, `.` and `x` behave as for a typed send;
  - the prompt names @all when the text has it;
  - a card naming a local file asks first, and `n` sends it without any upload;
  - an `@name` sent to a chat other than the open one stays text.
- Snippets:
  - `1`-`9`, a click and the `/` popup insert without asking;
  - a paste into the AI input arrives (`TestForward_EveryInputTakesAPaste` row);
  - `ai.snippets` replaces the built-ins.
- Every new NORMAL key has a help row (`TestHelpEntries_DocumentEveryNormalKey`). The edges of every action zone hit
  and the cells next to them miss.
- Stream to chat:
  - one send, then rewrites carrying the full text so far;
  - the last write is the final text;
  - `x` leaves `(interrupted)`;
  - a failed send keeps the answer in the panel.
- History tools: off stops the turn on any tool call. On lets through only a shell tool call whose command is an
  allowlisted larkim read-only invocation for the session's chat; any other tool call cancels the turn, and each
  call that runs leaves a trace line.
