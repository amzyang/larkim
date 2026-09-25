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
larkim silence                                 # configured silence rules and what each one matches
larkim sync --backfill-days 7                  # one tick in the foreground (daemon must be stopped)
larkim db path && larkim schema                # for direct SQLite consumers, see docs/SCHEMA.md
```

Output is a table on a terminal and JSON when piped or with `--json`.

```sh
larkim send --to linlan@example.com --text "hi"     # email, name or ou_ id
larkim send --chat "项目协作群" --text "hi"          # chat name or oc_ id
larkim reply om_xxx --text "ok" --in-thread
larkim send --chat "平台组" --markdown $'## 发布说明\n\n- 修复了 A'   # rich-text post
larkim send --chat "平台组" --image ~/Desktop/shot.png              # uploaded, then sent
larkim send --chat "平台组" --file ~/Desktop/发布说明.pdf            # any other file, same way
larkim watch --chat "项目协作群"                     # stream new messages
larkim tui
```

## TUI

`larkim tui` shows chats, the selected chat's messages and, when opened, a thread pane, plus a composer. The chat header names the chat, marked with a glyph for its kind, and a rule under it parts the header from the list. If no daemon holds the data-dir lock the TUI syncs in-process. Colours follow the terminal palette and its light or dark background. Below 114 columns the thread or assistant pane takes the place of the messages pane; the TUI needs at least 78×12. A reply carries the message it answers quoted above its body — sender and gist on one line — unless that message is the one right above it.

The message list is split where the calendar day changes, and each message is headed by its sender — the selected one also spells out its time. Official emoji are drawn as emoji, interactive cards as a titled block with their buttons, and system messages centred and muted. On kitty, images are drawn in place once they have been downloaded; elsewhere they read as `[图片]`. An attachment is carded the way the client draws one: a video as its cover frame under how long it runs, a voice message as that length, and any other file as its name beside its size. `o` or a click opens the downloaded file.

| keys | action |
|---|---|
| `j` `k` `gg` `G` `Ctrl+d` `Ctrl+u` | move in the focused list |
| `Tab` `Shift+Tab` `h` `l` | change focused pane |
| `Enter` | open chat · open thread · reply |
| `i` `r` `R` | write · reply · reply in thread (Enter sends, Shift+Enter newline, Esc back) |
| composer | the draft is markdown: one that uses any of it goes out as a Feishu rich-text post, anything else as plain text, one that is a single `![](…)` goes out as an image and one that is a single `[](…)` naming a local file goes out as that file. The badge under the draft names which, along with the files it will upload or the path it cannot find. |
| rich text | a post is sent exactly as typed and drawn as a document: heading levels, `•`/`◦` bullets with indent, a quote gutter, rules and real tables. Plain text messages stay literal. |
| files | write `[名字](~/Desktop/报告.pdf)` — an ordinary markdown link, the `!`-less form of an image reference. A draft that is one such link and nothing else goes out as a file message; the target has to be a file this machine holds, so `[点这里](https://…)` stays an ordinary link in a post. A `file_…` key Feishu already holds is used as-is. Feishu caps an attachment at 30 MB. Video and voice go as plain files, not as a player or a voice bar |
| images | write `![alt](~/Desktop/shot.png)` or `![alt](./a.png)`; the file is uploaded when Enter is pressed and the picture draws in the message list straight away. A `https://` address is downloaded and uploaded at send time, and an `img_…` key Feishu already holds is used as-is. Feishu caps a message image at 10 MB, and a remote one at 8 MB. |
| `@` `:` `[` | completion, without leaving the draft: `@` offers who this chat reaches, `:` and `[` offer emoji. The popup opens over the writing area as you type and narrows on Chinese, pinyin or initials, the way `/` does; `Tab` or `Enter` accepts, `Ctrl+n`/`Ctrl+p` move, `Esc` dismisses and leaves what you typed as text. A trigger inside a word opens nothing, so `http://`, `14:30` and an email address are left alone, and an emoji trigger waits for two letters, so a lone `:` or `[` is punctuation |
| `@` | the people in the chat, the bots in it and you, with `@All` ahead of them in a group. A chat of two offers the pair it is, and no `@All` — there is nobody else there to shout at. Accepting writes the plain `@名字` you would have typed, and the tag Feishu actually notifies on is built from it at send time — so the draft stays ordinary text you can keep editing |
| `:` `[` | an emoji goes in as its character where one carries it, and as the bracketed Chinese name Feishu's own text messages spell — `[完成]` — where none does. `[` is the same completion reached the way that spelling reads. Feishu's whole set is offered, plus the Unicode emoji it has no answer for |
| `Ctrl+o` | toggle the preview, which draws a post or an image draft the way the message list will. The writing area grows with the draft up to ten rows; neither takes rows the message panes need. |
| `Ctrl+g` | hand the draft to `$VISUAL` or `$EDITOR` (else `vi`) as a `.md` file, so a long message is written with markdown highlighting. Saving brings the text back; quitting without saving leaves the draft alone. |
| `Ctrl+v` | paste whatever the clipboard holds: a screenshot is staged under `~/.larkim/resources/pasted/` and referenced as an image, a file copied in Finder is referenced where it already sits — as a picture when it is one, otherwise as an attachment, and text lands at the cursor. Staged images are pruned after a week. Use `Ctrl+v` rather than `Cmd+v` for images — kitty turns `Cmd+v` into a text-only paste, so it cannot see image data. |
| `Ctrl+r` | drop the quote from the open draft; the quoted message is named above the composer and marked `↩replying` in the list |
| `n` `N` | jump to the next or previous chat with something waiting and open it, wrapping round the list; muted chats are skipped, as they are in the header's count |
| `I` | the open chat's own card in the right pane, in place of the thread: what kind of chat it is, its description, `external` and `dissolved` badges, and the members the daily refresh last saw, with the owner marked — a roster the server caps is drawn as `partial` rather than as a count. A chat of two draws the person instead — enterprise email, department, and whether they are outside this tenant |
| `f` | forward the selected message. The chooser lists chats first, then the people no chat reaches yet, filtered the same way `/` filters; the message being sent on is named under the list. Unlike `D` it asks nothing further — picking a destination is already the deliberate step. Merge-forward is not offered: that API takes bot identity only |
| `D` | recall your own message. It asks `y/n` first, because a recall is visible to everyone who was in the chat and cannot be undone; whether the window has closed is Feishu's answer, not a guess made here. Editing a sent message is not offered: that API takes bot identity only, so recall-and-resend is the correction path |
| `t` | toggle the thread pane for the selected message |
| `.` `x` | a message appears as `(sending)` the moment Enter is pressed; one Feishu refused is marked `(failed)` — `.` sends it again under the same idempotency key, `x` drops it |
| `Y` `yy` `yr` `yc` `v` | copy the agent context · the message id · its raw json · its text · start a range selection (`j`/`k` extend, `Y` copies, `Esc` cancels) |
| `o` | open what the message draws: a call to join, an attachment's own file, else the message in the Feishu client |
| `e` | react to the selected message: type to filter the emoji, `Enter` puts the highlighted one on, `Esc` leaves |
| `/` | filter chats |
| `Ctrl+f` / `:search <text>` | search messages, chats and people in one panel, all three under the same cursor: the store answers as the query is typed and Feishu is asked once it stands still, `Enter` opens the hit, `Esc` leaves |
| `a` / `:ai …` | assistant in the right pane: `summary`, `draft <how>` (result lands in the composer), `todo`, or any question about the open chat |
| `:mentions` | everything that @'d you, across every chat, newest first; Enter jumps to it. A chat holding an unread mention wears an `@` badge in the list until it is read, however many messages have landed since |
| `:` `;` | command line: `:copy <200\|7d\|all>` `:goto <chat>` `:send <chat\|ou_> <text>` `:react <emoji>` `:mentions` `:preview` `:sync` `:q` |
| mouse | click focuses and selects, double-click opens, wheel scrolls |

Shift+Enter needs a terminal with the kitty keyboard protocol (kitty, Ghostty, WezTerm); elsewhere use Alt+Enter or Ctrl+J for newlines.

### Agent context

`Y` puts the conversation on the clipboard in a fixed format built for pasting into a coding agent: a header naming the chat, the people in it and who you are, then one tagged block per message carrying its id, time, sender, mentions, reply and thread links, reactions and attachment paths. Message bodies are copied verbatim, so the block boundary carries a random suffix generated per copy. The export ends with a `larkim messages list --before …` command the agent can run to page further back.

What `Y` covers depends on the focus: the message under the cursor, the whole `v` selection, or — from the chats pane — the highlighted chat's last day, at most ten messages. `:copy 200`, `:copy 7d` and `:copy all` always cover the open chat, whatever has focus. Thread replies are folded out of a chat export and counted on their root instead; to copy a thread's contents, open it and press `Y` there.

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

## Diagnostics

Every process writes to `~/.larkim/larkim.log` — the daemon, the TUI and one-off commands alike, so each line carries the pid and the command that wrote it. It keeps the ticks that landed something, the lark-cli calls that failed with Feishu's own error code and `log_id`, and the failures the TUI has no room to show. The file is rolled aside once at 8 MB.

`--debug` adds the call detail: one line for every lark-cli request and one for its response, paired by a call number, carrying the full argument vector, the lane the call waited in and how long it waited, then its duration, the bytes it returned and the pages it fetched. On an ordinary command the log also goes to stderr; `larkim tui` owns the screen, so there it goes to the file alone.

```sh
larkim --debug sync
tail -f ~/.larkim/larkim.log
```

The log holds message bodies verbatim, because that is what was sent. Scrub it before pasting it anywhere.

## Telemetry

Release builds carry a Sentry DSN and report crashes and unexpected errors (never message content, host name or identity). Expected failures such as missing login, network errors or rate limits are not sent. Opt out with `DO_NOT_TRACK=1`, `SENTRY_DSN=` (empty) or `--sentry-dsn ""`; local builds have telemetry off.

## Library

`store`, `sync`, `larkcli` and `config` are importable Go packages. A consumer that acquires `sync.TryLock` may run `sync.Syncer.Run` in-process; otherwise it reads the store while the daemon writes.

## Development

```sh
just test
just build && ./larkim --config ./dev.yaml sync
```
