# larkim

Feishu/Lark IM synced to local SQLite, with a CLI and a TUI on top. Runs as a background service, keeps your chats, messages, contacts and attachments in `~/.larkim/larkim.db`, and answers queries without touching the network.

larkim drives the official [lark-cli](https://github.com/larksuite/cli) for authentication and every API call; it never stores credentials itself.

## Install

```sh
brew install amzyang/tap/larkim
npm install -g @larksuite/cli
lark-cli auth login --domain im,contact
brew services start larkim
```

## Use

```sh
larkim status                                  # sync state, counts, recent ticks
larkim chats list --search 协作
larkim messages list --chat "项目协作群" --since 24h --limit 20
larkim messages list --sender ou_xxx --type image --json
larkim messages show om_xxx                    # rendering, raw body, downloaded attachments
larkim messages thread om_xxx
larkim messages list --query "发布 计划"         # full-text search (every term must match; CJK substrings work)
larkim messages list --unread                  # Feishu says you have not read these yet
larkim messages list --unconsumed              # not yet processed by a local consumer
larkim mark consumed om_xxx om_yyy             # local flag only; Feishu's red dot is untouched
larkim silence                                 # configured silence rules and what each one matches
larkim sync --backfill-days 7                  # one tick in the foreground (daemon must be stopped)
larkim db path && larkim schema                # for direct SQLite consumers, see docs/SCHEMA.md
```

Output is a table on a terminal and JSON when piped or with `--json`.

```sh
larkim send --to linlan@example.com --text "hi"     # email, name or ou_ id
larkim send --chat "项目协作群" --text "hi"          # chat name or oc_ id
larkim reply om_xxx --text "ok" --in-thread
larkim watch --chat "项目协作群"                     # stream new messages
larkim tui
```

## TUI

`larkim tui` shows chats, the selected chat's messages and, when opened, a thread pane, plus a composer. The chat header carries the chat id. If no daemon holds the data-dir lock the TUI syncs in-process. Colours follow the terminal palette and its light or dark background. Below 114 columns the thread or assistant pane takes the place of the messages pane; the TUI needs at least 60×12. A reply carries the message it answers quoted above its body — sender and gist on one line — unless that message is the one right above it.

The message list is split where the calendar day changes, and each message is headed by its sender — the selected one also spells out its time. Official emoji are drawn as emoji, interactive cards as a titled block with their buttons, and system messages centred and muted. On kitty, images are drawn in place once they have been downloaded; elsewhere they read as `[图片]`.

| keys | action |
|---|---|
| `j` `k` `gg` `G` `Ctrl+d` `Ctrl+u` | move in the focused list |
| `Tab` `Shift+Tab` `h` `l` | change focused pane |
| `Enter` | open chat · open thread · reply |
| `i` `r` `R` | write · reply · reply in thread (Enter sends, Shift+Enter newline, Esc back) |
| `Ctrl+r` | drop the quote from the open draft; the quoted message is named above the composer and marked `↩replying` in the list |
| `t` | toggle the thread pane for the selected message |
| `.` `x` | a message appears as `(sending)` the moment Enter is pressed; one Feishu refused is marked `(failed)` — `.` sends it again under the same idempotency key, `x` drops it |
| `y` `v` | copy the agent context · start a range selection (`j`/`k` extend, `y` copies, `Esc` cancels) |
| `o` | open in the Feishu client |
| `/` | filter chats |
| `:search <text>` | cross-chat full-text search in the messages pane; Enter jumps to the hit, Esc leaves |
| `a` / `:ai …` | assistant in the right pane: `summary`, `draft <how>` (result lands in the composer), `todo`, or any question about the open chat |
| `:` `;` | command line: `:copy <200\|7d\|all>` `:goto <chat>` `:send <chat\|ou_> <text>` `:sync` `:q` |
| mouse | click focuses and selects, double-click opens, wheel scrolls |

Shift+Enter needs a terminal with the kitty keyboard protocol (kitty, Ghostty, WezTerm); elsewhere use Alt+Enter or Ctrl+J for newlines.

### Agent context

`y` puts the conversation on the clipboard in a fixed format built for pasting into a coding agent: a header naming the chat, the people in it and who you are, then one tagged block per message carrying its id, time, sender, mentions, reply and thread links, reactions and attachment paths. Message bodies are copied verbatim, so the block boundary carries a random suffix generated per copy. The export ends with a `larkim messages list --before …` command the agent can run to page further back.

What `y` covers depends on the focus: the message under the cursor, the whole `v` selection, or — from the chats pane — the highlighted chat's last day, at most ten messages. `:copy 200`, `:copy 7d` and `:copy all` always cover the open chat, whatever has focus. Thread replies are folded out of a chat export and counted on their root instead; to copy a thread's contents, open it and press `y` there.

## How it syncs

Every tick (default 3s) a cross-chat message search over a sliding window discovers new message ids; unknown ids are fetched in batches of 50 with millisecond timestamps and raw bodies, then rendered to readable text through lark-cli. Every 10 minutes the full chat list is refreshed and the 30 most active chats are reconciled from their cursors, which catches anything the search index misses. History is backfilled a few chats per tick (default 30 days). Edits and recalls update in place; recalled messages keep their last known body.

Attachments (images, files, audio, video, post-embedded media) are downloaded under `~/.larkim/resources/` up to `resources.max_bytes`, with retries. For messages from others in the last 7 days the daemon asks Feishu whether you have read them (`is_read_remote`), rechecking on a widening schedule until they are read; that is the only read signal Feishu exposes and it cannot be written. Opening a chat in the TUI takes its waiting messages as read locally, which is what drops the badge drawn here, and walks the Feishu desktop client onto that chat in the background so its own red dot falls too. A message landing in the chat you are reading relights that dot and drops it again; a chat with nothing waiting is never touched.

Every 6 hours the chats active in the last week are re-listed so edits and recalls are reflected; group member lists refresh daily into `chat_members` / `contacts`, and chat and contact avatars are stored under `~/.larkim/resources/avatars/`.

When the user token expires the daemon stops calling the API, reports `needs_login` in `larkim status`, and resumes after `lark-cli auth login`.

## Configuration

`~/.larkim/config.yaml`, every key optional. [`config.example.yaml`](config.example.yaml) lists them all with their defaults and what each one is for; copy it and edit.

`silence` rules take noise out of the way without hiding it: a matching message keeps its place in the chat but carries no unread badge and never moves its chat up the list ([docs/silence](docs/silence/PRD.md)). The rules are read by the process that syncs, so a change lands when that process restarts.

## Assistant

`a` (or `:ai …`) sends the open chat's recent messages (`ai.context`, default 80) to Claude and streams the answer into the right pane; `:ai draft <how>` puts the drafted reply in the composer for you to edit and send. It needs an Anthropic API key in the environment variable named by `ai.api_key_env` (default `ANTHROPIC_API_KEY`); without one the command says so and nothing is sent. Model: `ai.model`, default `claude-opus-5`. Only the transcript of the chat you are looking at leaves the machine.

## Telemetry

Release builds carry a Sentry DSN and report crashes and unexpected errors (never message content, host name or identity). Expected failures such as missing login, network errors or rate limits are not sent. Opt out with `DO_NOT_TRACK=1`, `SENTRY_DSN=` (empty) or `--sentry-dsn ""`; local builds have telemetry off.

## Library

`store`, `sync`, `larkcli` and `config` are importable Go packages. A consumer that acquires `sync.TryLock` may run `sync.Syncer.Run` in-process; otherwise it reads the store while the daemon writes.

## Development

```sh
just test
just build && ./larkim --config ./dev.yaml sync
```
