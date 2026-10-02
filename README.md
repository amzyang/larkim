# larkim

Feishu/Lark IM synced to local SQLite, with a CLI and a TUI on top. Runs as a background service, keeps your chats, messages, contacts and attachments in `~/.larkim/larkim.db`, and answers queries without touching the network.

larkim drives the official [lark-cli](https://github.com/larksuite/cli) for authentication and every API call; it never stores credentials itself.

## Install

```sh
brew install amzyang/tap/larkim
npm install -g @larksuite/cli
lark-cli auth login --domain im,contact
lark-cli config keychain-downgrade
brew services start larkim
```

`keychain-downgrade` moves lark-cli's master key out of the macOS keychain into a file under `~/Library/Application Support/lark-cli/`, readable only by you. Without it every lark-cli call runs `/usr/bin/security` about four times, which costs 60–80 ms and about half the CPU of each of the several calls a second larkim makes while you are looking at it.

The formula installs the bash, zsh and fish completions; reached any other way, `larkim completion <shell>` prints them.

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
larkim unread                                  # every waiting chat as one page, parted by chat
larkim read-all --dry-run                      # how many chats still have a red dot in Feishu
larkim read-all                                # take them all as read, and clear each chat's red dot in Feishu
larkim --set applink_pace_ms=1500 read-all     # override one config key for this run; repeatable
larkim silence                                 # configured silence rules and what each one matches
larkim sync --backfill-days 7                  # one tick in the foreground (daemon must be stopped)
larkim db path && larkim schema                # for direct SQLite consumers, see docs/SCHEMA.md
larkim emoji list                              # the emoji table: names, search terms, panel order, pictures
larkim emoji add 摸鱼 划水 --image ~/Desktop/a.png  # keep a picture of your own; the clipboard's without --image
larkim emoji list --custom && larkim emoji rm 摸鱼
```

Output is a table on a terminal and JSON when piped or with `--json`.

```sh
larkim send --to linlan@example.com --text "hi"     # email, name or ou_ id
larkim send --chat "项目协作群" --text "hi"          # chat name or oc_ id
larkim reply om_xxx --text "ok" --in-thread
larkim send --chat "平台组" --markdown $'## 发布说明\n\n- 修复了 A'   # rich-text post
larkim send --chat "平台组" --markdown @./notes.md                  # or -; the pictures it names go up too
larkim lint ./notes.md                               # what that body loses on the way to Feishu
larkim send --chat "平台组" --image ~/Desktop/shot.png              # uploaded, then sent
larkim send --chat "平台组" --file ~/Desktop/发布说明.pdf            # any other file, same way
larkim react om_xxx --emoji DONE                     # an emoji Feishu refuses is replied with instead
larkim watch --chat "项目协作群"                     # stream new messages
larkim
```

## TUI

`larkim` (or `larkim tui`) shows chats, the selected chat's messages and, when opened, a right-hand pane, plus a composer. The chats list has a column of its own running the full height of the terminal, the way the client draws it; the composer sits under the message panes beside it. The chat header names the chat, marked with a glyph for its kind and tagged `external` or `dissolved` where it is either, and a rule under it parts the header from the list. If no daemon holds the data-dir lock the TUI syncs in-process. Colours follow the terminal palette and its light or dark background. Below 114 columns the thread or assistant pane takes the place of the messages pane; the TUI needs at least 78×12. A reply carries the message it answers quoted above its body — sender and gist on one line.

A message that holds other messages takes one line in the list and opens in the right pane. A thread root carries how many replies are under it and the last of them, the replies themselves having left the chat's flow; a merged forward carries how many messages it holds and the first of them, in place of the tagged, timestamped tree it would otherwise print. Clicking the line, or `Enter` or `t` on it, opens the pane. Opening a forward from inside the pane stacks it over what is there — a forwarded bundle inside a thread, a bundle inside a bundle — and `Esc` peels one layer off, closing the column on the last. `h` peels the same layers but never closes the column: with nothing left underneath it goes back to being the key that moves focus one pane left. Messages inside a forward belong to their own chat: they can be read and copied but not answered, reacted to or recalled. A thread's replies are taken as read when its pane is opened, not when the chat is, and until then the root's line carries the unread dot and the chat wears a `⤷` where its count would go — but only for a thread you have a stake in, having spoken in it or been named. A thread nobody asked you about is somebody else's conversation, which is why the badge leaves replies out in the first place. `n` and `N` do not follow the marker: the queue they clear is the badge's.

A message somebody answered carries a line under it counting the whole tree it started — an answer to an answer included, the way the client counts one. The answers themselves stay in the chat's flow where they were said, each quoting what it replies to; `t` on any message of the tree opens the Details pane, which gathers the message the conversation started from and every live answer under it in time order. `Enter` there still answers the message rather than opening the pane, because a reply lands in the flow beside it.

The message list is split where the calendar day changes, and each message is headed by its sender — the selected one also spells out its time. Official emoji are drawn as emoji, interactive cards as a titled block with their buttons, and system messages centred and muted. On kitty, images are drawn in place once they have been downloaded; elsewhere they read as `[Image]`. An attachment is carded the way the client draws one: a video as its cover frame under how long it runs, a voice message as that length, and any other file as its name beside its size. `o` or a click opens the downloaded file. What a message leads somewhere — a link, a Feishu document, an attachment card, a call's Join, a card's buttons — is a terminal hyperlink as well as a click target: kitty shows the target under the pointer, underlines the run while it is there, and opens it on `Ctrl+Shift+click` even though larkim holds the mouse.

| keys | action |
|---|---|
| `j` `k` `gg` `G` `Ctrl+d` `Ctrl+u` | move in the focused list |
| `Tab` `Shift+Tab` `h` `l` | change focused pane. In the right pane `h` first steps out of a stacked frame, the column staying open |
| `Enter` | open chat · open the container under the cursor · reply |
| `i` `r` `R` | write · reply · reply in thread (Enter sends, Shift+Enter newline, Esc back). The right column carries a composer of its own whenever it shows a thread or a reply tree, the way the client's Thread sidebar does: the keys go to the box under the focused column, each box keeps its own draft and its own quote, and an answer written in the right one lands in the frame it belongs to. |
| composer | the draft is markdown: one that uses any of it goes out as a Feishu rich-text post, anything else as plain text, one that is a single `![](…)` goes out as an image and one that is a single `[](…)` naming a local file goes out as that file. The badge under the draft names which, along with the files it will upload or the path it cannot find. |
| rich text | a post is sent exactly as typed, and drawn from the elements it was written from: a paragraph per line and an empty one as the blank line it is, the sender's own emphasis and links, mentions, code blocks in the language they were tagged with, and pictures where they stand. The markdown blocks the client itself writes are drawn as documents — heading levels, `•`/`◦` bullets with indent, a quote gutter, rules and real tables. Words typed into a post stay literal, as do plain text messages. |
| files | write `[名字](~/Desktop/报告.pdf)` — an ordinary markdown link, the `!`-less form of an image reference. A draft that is one such link and nothing else goes out as a file message; the target has to be a file this machine holds, so `[点这里](https://…)` stays an ordinary link in a post. A `file_…` key Feishu already holds is used as-is. Feishu caps an attachment at 30 MB. Video and voice go as plain files, not as a player or a voice bar |
| images | write `![alt](~/Desktop/shot.png)` or `![alt](./a.png)`; the file is uploaded when Enter is pressed and the picture draws in the message list straight away. A `https://` address is downloaded and uploaded at send time, and an `img_…` key Feishu already holds is used as-is. Feishu caps a message image at 10 MB, and a remote one at 8 MB. |
| `@` `:` `[` | completion, without leaving the draft: `@` offers who this chat reaches, `:` and `[` offer emoji. The popup opens over the writing area as you type and narrows on Chinese, pinyin, initials or the character itself pasted in, the way `/` does; `Tab` or `Enter` accepts, `Ctrl+n`/`Ctrl+p` move, `Esc` dismisses and leaves what you typed as text. Rows carry the face or the emoji and the name; what tells the focused one apart — a colleague's department and email, an emoji's key and the pinyin that reached it — stands in a box beside the list. A trigger inside a word opens nothing, so `http://`, `14:30` and an email address are left alone, and an emoji trigger waits for two letters, so a lone `:` or `[` is punctuation |
| `@` | the people in the chat, the bots in it and you, with `@All` ahead of them in a group. A chat of two offers the pair it is, and no `@All` — there is nobody else there to shout at. Accepting writes the plain `@名字` you would have typed, and the tag Feishu actually notifies on is built from it at send time — so the draft stays ordinary text you can keep editing |
| `:` `[` | an emoji goes in as its character where one carries it, and as the bracketed Chinese name Feishu's own text messages spell — `[完成]` — where none does. `[` is the same completion reached the way that spelling reads. Feishu's whole set is offered, plus the Unicode emoji it has no answer for and a few written rather than drawn — `¯\_(ツ)_/¯` and the table flip, which go in as the text they are; `颜文字` reaches those four together |
| `Ctrl+o` | toggle the preview, which draws a post or an image draft the way the message list will. The writing area grows with the draft up to ten rows; neither takes rows the message panes need. |
| `Ctrl+g` | hand the draft to `$VISUAL` or `$EDITOR` (else `vi`) as a `.md` file, so a long message is written with markdown highlighting. Saving brings the text back; quitting without saving leaves the draft alone. |
| `Ctrl+v` / `Cmd+v` | paste whatever the clipboard holds: a screenshot is staged under `~/.larkim/resources/pasted/` and referenced as an image, a file copied in Finder is referenced where it already sits — as a picture when it is one, otherwise as an attachment, and text lands at the cursor. A selection copied out of a browser, a doc or the Feishu client carries formatting too, and goes in as the markdown that stands for it — headings, lists, quotes, tables, bold, strikethrough, links and remote images; a copy that turns out to carry no formatting goes in as the plain text it was, so code copied out of an editor that highlights it stays code. Staged images are pruned after a week. In the composer, `Cmd+v` re-reads the pasteboard even when kitty already pasted plain text, so rich HTML still converts. For **image-only** clipboards in kitty, add `map --when-focus-on var:in_larkim super+v` (no action) to `kitty.conf` beside the global `paste_from_clipboard` binding — larkim sets `in_larkim` while it runs. |
| `Ctrl+r` | drop the quote from the open draft; the quoted message is named above the composer and marked `↩replying` in the list |
| `n` `N` | jump to the next or previous chat with something waiting and open it, wrapping round the list; muted chats are skipped, as they are in the header's count |
| `I` | the open chat's own card in the right pane, in place of the thread: what kind of chat it is, its description, `external` and `dissolved` badges, and the members the daily refresh last saw, with the owner marked — a roster the server caps is drawn as `partial` rather than as a count. A chat of two draws the person instead — enterprise email, department, and whether they are outside this tenant |
| `f` | forward the selected message. The chooser lists chats first, then the people no chat reaches yet, filtered the same way `/` filters; the message being sent on is named under the list. Unlike `D` it asks nothing further — picking a destination is already the deliberate step. Merge-forward is not offered: that API takes bot identity only |
| `D` | recall your own message. It asks `y/n` first, because a recall is visible to everyone who was in the chat and cannot be undone; whether the window has closed is Feishu's answer, not a guess made here |
| `E` | recall your own text or post message and take its text back into the composer, aimed where the message was, so a correction is typed over the original. The resend is an ordinary send and lands at the bottom of the chat. Editing in place is not offered: that API takes bot identity only and demands the caller be the sender, and a message sent under your own token is reachable by neither identity. A post comes back as the rendered markdown rather than the source that was sent, which is why it lands in the composer to be read before it goes out |
| `t` | open the thread, forwarded bundle or reply tree under the cursor in the right pane, or close it |
| `.` `x` | a message appears as `(sending)` the moment Enter is pressed; one Feishu refused is marked `(failed)` — `.` sends it again under the same idempotency key, `x` drops it |
| `Y` `yy` `yr` `yc` `v` | copy the agent context · the message id · its raw json · its text · start a range selection (`j`/`k` extend, `Y` copies, `Esc` cancels) |
| `o` | open what the message draws: a call to join, an attachment's own file, else the message in the Feishu client |
| `e` | react to the selected message: an empty filter opens on the emoji you reach for most, the way the client's own panel opens on its frequently used band, type to narrow — a name, its pinyin, or the character pasted straight in — `Enter` puts the highlighted one on, `Esc` leaves; a digit goes into the query first, so `666`, `100` and `+1` type the way they read, and picks the numbered row only when the query it makes answers nothing. Ten of Feishu's emoji are no longer reactions — six the client withdrew, four belonging to another tenant — and are marked `图`: choosing one replies with the picture instead, which is the only way it still reaches the other side. The emoji you kept yourself (`larkim emoji add`) are offered here too, reached by their names and pinyin like any other, and travel the same way — Feishu has no key for a picture it has never seen |
| `/` | filter chats |
| `Ctrl+f` / `:search <text>` | search messages, chats and people in one panel, all three under the same cursor: the store answers as the query is typed and Feishu is asked once it stands still, `Enter` opens the hit, `Esc` leaves. What was searched for is marked wherever it stands in a body or a sender name — a dotted underline, the same mark `/` puts on a filtered chat name, so it is not read as a link or as text the sender underlined |
| `a` / `A` | the assistant panel on this chat, over whatever the right column is showing, keys in its own box under the column — `a` the chat's latest session, `A` a new one, and nothing is asked until Enter. `[`/`]` switch the chat's sessions, `x` stops an answer, Esc closes the panel and uncovers what it stood over; an answer keeps streaming into its session either way |
| `Unread` | the row at the head of the chats list — the client's own unread ring in the avatar column — and `:unread`. It lays every chat still waiting out as one page, a section each: the chat that has waited longest first, its messages running from the oldest one the badge counts through to the newest, with what was read in between still in place. The rule over the pane names the section the top of the viewport is in and follows it down, each rule carrying what the chat's own row would have said: how much is waiting, an `@` where any of it names you, and the bell where the chat is muted; the title names the chat the composer is aimed at, which is the cursor's own unless words or a quote have pinned it — a half-written answer keeps the chat it was begun in as the cursor walks on, and `r` on a message elsewhere carries both the words and the title to that message's chat. `n`/`N` step between sections, `Enter` leaves for the chat itself, landing on the message it was pressed on, and `Esc` goes back to the chat that was open. Nothing is taken as read by being seen here: settling a chat is yours to say, either by going into it or by taking it as read where it stands — the single check closing its rule, or `m` on the section the cursor is in, which drops that chat's badge and clears the Feishu client's own dot for the whole chat, past what the section shows, by the lever `mark_read.mode` names. The double check in the chats header does the same for every chat at once. Muted chats are in, unlike `n`'s queue and the header's count: what silencing refuses is being pulled at, and opening this page is you doing the pulling. A chat that starts waiting while the page is up is counted at its foot rather than inserted, because the sections are anchored for the visit. `larkim unread` prints the same page without the cursor |
| `:mentions` | everything that @'d you, across every chat, newest first; Enter jumps to it. A chat holding an unread mention wears an `@` badge in the list until it is read, however many messages have landed since |
| `:read-all` | take every chat as read and clear the Feishu client's red dots, after a `y/n`. The same button the chats header draws |
| `:` `;` | command line: `:copy <200\|7d\|all>` `:goto <chat>` `:send <chat\|ou_> <text>` `:react <emoji>` `:todoist` `:mentions` `:unread` `:read-all` `:set <option>[=<value>]` `:config [<key>]` `:preview` `:sync` `:q`. A name resolves by prefix once it reaches one command alone, so `:cop` is `:copy` and `:cf` is `:config`, and the offers matching what is typed stand over the line: `Tab` or `Ctrl+n`/`Ctrl+p` walks them and writes the one under the cursor into the line, `Esc` dismisses them, and the box beside the list says what the row under the cursor is: a command's usage and help, an option's value and how far a change reaches, the id a chat or person goes by. A command's first argument is offered too — the chats and people `:goto` and `:send` take, the emoji `:react` takes, and the fixed words of `:copy` and `:ai`, the options `:set` takes and the keys `:config` takes |
| `:set` | try an option for this session, leaving the config file alone, in vim's forms: a bare `:set` lists them, `name?` reports one, `name&` restores its default, `name=value` sets it. It reaches the keys a change takes effect on mid-session — `applink_pace_ms`, the gap between two navigations of the Feishu client, which to raise when a `:read-all` sweep leaves red dots behind; `mark_read.mode` and `mark_read.browser` (see below); the `ai.*` and `todoist.*` keys; and, where the TUI syncs in-process, the sweep's own pacing (`poll_interval_ms`, `overlap`, `active_top_k`, `chats_refresh_every`, `slow_path_every`, `repair_every`, `silence_sync`), which a daemon holding the lock keeps for itself. A key with a fixed set of values offers them after the `=` |
| `:config` | the config file's editor: every key with the value this session is running, filtered by `/`, `Enter` to edit the row and `&` to restore its default. A committed value is written to the file at once, and the row says how far it reaches: *takes effect now*, *the daemon rereads it* for a sweep key while a daemon owns the sweep, or *next start* for `data_dir`, `lark_cli_path`, `backfill_days` and `resources.max_bytes`. `:config <key>` opens on that row |
| mouse | click focuses and selects, double-click opens, click a quote to land on the message it names, click a reaction to add yours or take it back, click the double check in the chats header to take every chat as read, wheel scrolls |

Shift+Enter needs a terminal with the kitty keyboard protocol (kitty, Ghostty, WezTerm); elsewhere use Alt+Enter or Ctrl+J for newlines.

The badges — bot, mute, draft, reply count, chat kind, mark-all — are Nerd Font glyphs, which advance a full em where a monospaced text font advances 0.6. A terminal draws one at that size only when the cell after it is blank, so each carries an en-space and takes two columns; no font or terminal setting is needed.

### Agent context

`Y` puts the conversation on the clipboard in a fixed format built for pasting into a coding agent: a header naming the chat, the people in it and who you are, then one tagged block per message carrying its id, time, sender, mentions, reply and thread links, reactions and attachment paths. Message bodies are copied verbatim, so the block boundary carries a random suffix generated per copy. The export ends with a `larkim messages list --before …` command the agent can run to page further back.

What `Y` covers depends on the focus: the message under the cursor, the whole `v` selection, or — from the chats pane — the highlighted chat's last day, at most ten messages. `:copy 200`, `:copy 7d` and `:copy all` always cover the open chat, whatever has focus. Thread replies are folded out of a chat export and counted on their root instead; to copy a thread's contents, open it and press `Y` there.

## How it syncs

New messages are found by watching the chat list's order by activity: a chat that has just seen a message moves to the top, so each cycle reads the first 30 chats and lists the ones that moved, and the top three whether they moved or not, each on its own so that a slow chat holds up no other. When the TUI syncs in-process, the cycles run back to back while its window has focus, which puts a message in a chat near the top on screen within about a second; otherwise, and always under the daemon, one runs every `poll_interval_ms` (default 3s). A chat that has been quiet for a while takes Feishu itself several seconds to move up the ordering (about 8s measured), however often it is read. The sweep runs beside it at its own pace, `poll_interval_ms` between passes: every 30 seconds a cross-chat message search over a sliding window catches edits, recalls and anything the ordering missed, and every 10 minutes the full chat list is refreshed and the 30 most active chats are reconciled from their cursors. Messages are fetched with millisecond timestamps and raw bodies, then rendered to readable text, by larkim where it can and through lark-cli where it cannot. History is backfilled a few chats per tick (default 30 days); scrolling past the oldest message a chat holds pulls the page behind it, so a conversation can be walked back to its start. Edits and recalls update in place; recalled messages keep their last known body.

Attachments (images, files, audio, video, post-embedded media) are downloaded under `~/.larkim/resources/` up to `resources.max_bytes`, with retries. The writing in a downloaded picture is read once through Feishu's recognizer into `resource_text`, keyed by the picture rather than by the message, so a screenshot shared around several chats is read once; it needs the app scope `optical_char_recognition:image`. For messages from others in the last 7 days the daemon asks Feishu whether you have read them (`is_read_remote`), rechecking on a widening schedule until they are read; that is the only read signal Feishu exposes and it cannot be written. Opening a chat in the TUI takes its waiting messages as read locally, which is what drops the badge drawn here, and clears the Feishu client's own red dot by the lever `mark_read.mode` names. `applink` (the default) walks the desktop client onto that chat in the background, and the client sends the receipt. `web` posts the chat's read watermark to Feishu's web client using the Feishu login in `mark_read.browser` (default `chrome`), and Feishu answers for each chat; the first read of a process asks for Keychain access to that browser's cookies. The web client names chats by an id the OpenAPI never hands out, so larkim matches each chat once, through its newest message, and keeps the match in `chats.web_chat_id`. A message landing in the chat you are reading relights that dot and drops it again; a chat with nothing waiting is never touched.

Every 6 hours the chats active in the last week are re-listed so edits and recalls are reflected; group member lists refresh daily into `chat_members` / `contacts`, and chat and contact avatars are stored under `~/.larkim/resources/avatars/`.

When the user token expires the daemon stops calling the API, reports `needs_login` in `larkim status`, and resumes after `lark-cli auth login`.

## Configuration

`~/.larkim/config.yaml`, every key optional. [`config.example.yaml`](config.example.yaml) lists them all with their defaults and what each one is for; copy it and edit. The TUI's `:config` edits the same file in place, keeping the comments around what it rewrites, and `--set key=value` overrides a key for one run without touching it.

A running daemon rereads the file when its modification time changes, between two ticks, and takes the new pacing — `poll_interval_ms`, `overlap`, `backfill_days`, `active_top_k`, `chats_refresh_every`, `slow_path_every`, `repair_every`, `resources.max_bytes`, `silence_sync` — and the `silence` rules on the next one; `--set` still wins over the file it reread, a file that stops parsing leaves it on what it has, and the keys it cannot honour mid-run (`data_dir`, `lark_cli_path`) are logged as needing a restart. `backfill_days` and `resources.max_bytes` bound work not yet done: raising either does not go back for the chats already backfilled or the attachments already marked too large.

`silence` rules take noise out of the way without hiding it: a matching message keeps its place in the chat but carries no unread badge and never moves its chat up the list ([docs/silence](docs/silence/PRD.md)). The process that holds the sync lock is the only one that rebuilds the stored flags, once per change to the rules and over every message, so an edit in the Silence tab reaches the messages already stored on that process's next tick: at once when the TUI syncs in-process, within one `poll_interval_ms` under a daemon.

## Assistant

`a` opens the assistant panel over the right column — over an open thread, without closing it — with the keys in its own box. Nothing is asked until Enter. A question carries the chat's latest `ai.context` messages (default 80), read from the store when you ask so a follow-up sees what has arrived since, the writing read out of their pictures, what the question is about (the message under the cursor, its thread, the composer's draft), and the session's earlier questions and answers. Each chat keeps its own sessions: `[`/`]` switch them, `A` starts one, and an answer keeps streaming into its session when the panel closes, is covered, or you change chats. `x` stops an answer. `:ai <summary|draft|todo>` or `:ai <question>` starts a new session and asks at once; the draft form still puts the finished reply in the composer. The panel draws over the right column without touching it: no key reaches the frame beneath, and Esc brings it back as it stood.

The agent is the command in `ai.agent` (default `omp --mode acp`), started once per question and holding its own login; if the command is not installed the panel opens and asking says so. `ai.model` (default `cursor/composer-2.5-fast`) picks a value of the agent's model option, by exact id or by a part that names one model; empty keeps the agent's default. Both change live through `:config`. The agent is offered no file or terminal access and runs in an empty directory. Refusing permissions is not what keeps it out of local files: omp runs read, search and fetch tools without asking at all, so any tool call it makes ends the answer (`stopped: agent used …`), and running it with `--no-tools` is the quiet configuration. Only the material of the question leaves the machine.

With `ai.history: true` the agent may read the chat's synced history itself: the system prompt teaches the exact `larkim --config … messages list … --json` invocations (the same continuation command `Y` copies, and `--query` for full-text search), and the tool gate passes only those, scoped to the session's chat — any other command, or any other tool, cancels the turn. Each call that runs leaves a dim trace line (`⌕ messages list · 40 rows`). Run the agent as `omp --mode acp --tools bash` with history on and `--no-tools` with it off; larkim never rewrites the configured argv, and the gate holds either way.

The reaction picker asks a second model, at a second vendor. `e` sends the open chat's last dozen messages, the message being reacted to and the reactions already standing on it to TypeSafe's System One (`ai.jev_endpoint`), which ranks the emoji you reach for most: the ones it judges to fit are marked `✦` in the grid's first rows, the cells beside them filled from your own order as before, and a message it says nobody would react to leaves `nothing to react to` on the query line instead. The transcript goes up built as the assistant's is, the writing read out of pictures included, and the row is gone as soon as a query narrows the grid. It needs a key in the environment variable named by `ai.jev_key_env` (default `TYPESAFE_API_KEY`); without one no row is drawn and nothing is sent. A message is asked about once and its answer kept for the session.

## Diagnostics

Every process writes to `~/.larkim/larkim.log` — the daemon, the TUI and one-off commands alike, so each line carries the pid and the command that wrote it. It keeps the ticks and discovery cycles that landed something, the lark-cli calls that failed with Feishu's own error code and `log_id`, and the failures the TUI has no room to show. The file is rolled aside once at 64 MB.

`--debug` adds the call detail: one line for every lark-cli request and one for its response, paired by a call number, carrying the full argument vector, the lane the call waited in and how long it waited, then its duration, the bytes it returned and the pages it fetched. On an ordinary command the log also goes to stderr; the TUI owns the screen, so there it goes to the file alone.

```sh
larkim --debug sync
tail -f ~/.larkim/larkim.log
```

The log holds message bodies verbatim, because that is what was sent. Scrub it before pasting it anywhere.

## Telemetry

Release builds carry a Sentry DSN and report crashes and unexpected errors (never message content, host name or identity). Expected failures such as missing login, network errors or rate limits are not sent. Opt out with `DO_NOT_TRACK=1`, `SENTRY_DSN=` (empty) or `--sentry-dsn ""`; local builds have telemetry off.

## Library

`store`, `sync`, `larkcli` and `config` are importable Go packages. A consumer that acquires `sync.TryLock` may run `sync.Syncer.Run` in-process; without it the sweep belongs to the lock holder, while pulls that name ids Feishu just answered for upsert the same rows from any process.

## Development

```sh
just test
just build && ./larkim --config ./dev.yaml sync
```
