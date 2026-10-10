# larkim SQLite schema

Database: `larkim db path` (default `~/.larkim/larkim.db`). WAL mode; readers never block the daemon. The authoritative DDL is `larkim schema` (embedded migrations in `store/migrations/`). All timestamps are Unix **milliseconds** in UTC unless the column name says otherwise.

Ownership: `daemon.lock` names the one process that runs the sweep — discovery, backfill, and the global cursors in `sync_state`. Everything a reader reaches for is a write any larkim process may make: a send, a reaction, an expanded forward, a page of older history and a cold search hit all name ids Feishu just answered for and upsert the same rows whoever asks. `drafts` belong to the TUI. How far a reader outside larkim has got is its own state, not a column here; see [Consumer cursors](#consumer-cursors).

## chats

One row per chat the user is (or was) in, from `GET /im/v1/chats` with `types=p2p,group`.

| column | meaning |
|---|---|
| `chat_id` | `oc_…` primary key |
| `name` | group name; for p2p, the peer's display name |
| `description` | the group's description as the client shows it; empty for p2p |
| `chat_mode` | `group`, `topic` or `p2p` |
| `chat_status` | `normal`, `dissolved`, `dissolved_save` |
| `owner_id` | the group owner's open id; empty for p2p and for groups the listing gave no owner for |
| `external` | 1 when the chat crosses tenants |
| `p2p_target_id`, `p2p_target_type` | peer open id and `user`/`bot` for p2p chats |
| `avatar_url`, `avatar_path` | group avatar URL and local copy (relative to the data dir); empty for p2p |
| `cursor_ms` | newest `create_ms` pulled by a per-chat listing; the slow path resumes from here minus overlap |
| `backfill_done_at` | set once the historical pull (`backfill_days`) finished |
| `history_floor_ms` | oldest `create_ms` the chat has been pulled back to, and 0 once the whole of it is stored. Meaningless while `backfill_done_at` is 0, which is what tells that zero from this one |
| `left_at` | non-zero when a full listing no longer contains the chat; reset when it reappears |
| `sync_error` | last permanent API rejection (e.g. restricted-mode chats cannot be listed); such chats still receive messages via search. Feishu 2200 is not stored here: it is a retryable internal error |
| `repaired_at` | when the last repair pass re-listed the chat's recent week |
| `first_seen_at` | when the chat was first stored, whether by a full listing or by a message arriving from one larkim had not listed yet; never rewritten |
| `last_seen_at` | when the chat was last confirmed present: a full listing restamps every row it carries. `left_at` is set by comparing this against the stamp of the listing that just finished, so a chat missing from one is the chat whose `last_seen_at` stayed behind |
| `raw_json` | the API item as received |
| `last_message_id`, `last_message_ms` | the chat's newest main-flow message; empty and 0 when it has none |
| `last_sender_id`, `last_sender_name`, `last_sender_type` | that message's sender |
| `last_msg_type`, `last_content`, `last_content_raw` | that message's type and body; `last_content` is empty until `last_rendered_at` is set |
| `last_mentions_json` | that message's mentions, the shape `messages.mentions_json` holds |
| `last_reactions_json` | that message's reaction block, the shape `messages.reactions_json` holds; empty while nobody has reacted |
| `last_rendered_at`, `last_deleted` | that message's rendering state and recall flag |
| `last_unsilenced_ms` | the newest main-flow message no silence rule matched, `system` messages left out unless the chat has no other main-flow message, and the key the list orders on; 0 when every non-`system` message is silenced |
| `silence_settled_pos` | the read watermark `silence_sync` last settled for the chat; messages at or below it never queue or settle again. The settle moves the feed's unread count only, never the per-message `is_read_remote` |
| `muted`, `mute_checked_at` | the user's do-not-disturb setting and when it was last answered; 0 means it has never been asked |
| `web_chat_id` | the Feishu web client's numeric id for the chat, which no OpenAPI response carries; empty until matched. Written by the processes that clear Feishu badges, never by the daemon |

A chat first seen only through a message (before the next full listing) exists with an empty name.

`web_chat_id` is learned by matching: the web client's inbox lists each chat with its newest message, and a message whose `(create_ms, message_position)` pair belongs to exactly one stored chat names that chat. A pair shared by two chats teaches nothing.

`muted` comes from `POST /im/v1/chat_user_setting/batch_get_mute_status` under user identity, since no chat listing carries it. The lookup rides the full chat refresh, covers at most 100 chats per round and only those with a message in the last 30 days, taking the longest unanswered first. Chats the API declines to answer for (non-member, malformed id) keep their previous `muted` and are stamped all the same, so `mute_checked_at` says when a chat was last asked about, not that the answer changed.

The `last_*` columns mirror the newest message whose `message_position` is non-negative, so the list shows what the chat's main flow shows: thread replies are excluded, thread roots are not. `UpsertMessages` and `UpdateRendered` rewrite them in their own transaction, which covers ingest, edits, recalls and rendering. Order chats by `last_unsilenced_ms` rather than by `last_message_ms` or an aggregate over `messages`: the `last_*` columns say what arrived last, the sort key says what last mattered, and the two differ exactly where a silence rule matched or a `system` message (a member joining, a rename, a call ending) came last. `ListChats` orders on `last_unsilenced_ms DESC, last_message_ms DESC, name, chat_id`; read status is not part of it, and `chat_id` closes it because both ms keys are 0 for every chat with no message yet.

## messages

One row per message id, from the raw message API (`create_ms` is millisecond precision).

| column | meaning |
|---|---|
| `id` | ingest order; `max(id)` is a cheap change detector |
| `message_id` | `om_…`, unique |
| `chat_id` | owning chat, also for thread replies |
| `msg_type` | `text`, `post`, `image`, `file`, `audio`, `media`, `sticker`, `interactive`, `share_chat`, `merge_forward`, `system`, … |
| `sender_id` | user `ou_…`; for bots the bot's open id |
| `sender_type` | `user` or `app` |
| `sender_name` | server-provided display name; may be empty for system messages |
| `content_raw` | `body.content` JSON string, shape depends on `msg_type` (`{"text":"…"}`, post blocks, `{"image_key":…}`, card JSON) |
| `content` | human-readable rendering; empty until `rendered_at` is set. every msg_type larkim knows is rendered in process — `text`, `post`, `interactive`, `image`, `file`, `audio`, `media`, `video`, `sticker`, `system`, `video_chat`, `calendar`, `share_calendar_event`, `general_calendar`, `share_chat`, `share_user`, `location`, `folder`, `vote`, `hongbao`, `todo`, `merge_forward` — in the shapes lark-cli renders them to, with four deliberate exceptions: a card is rendered from its own JSON and so keeps the block structure lark-cli's flattening runs together, a call names its meeting and how long it ran, a calendar event names its summary and span, and a todo is a checkbox plus summary because lark-cli's XML-shaped rendering does not carry completion. A `folder` renders to the single line lark-cli falls back to when it cannot expand one; larkim never expands. Only a msg_type with no renderer here goes to lark-cli (`+messages-mget`) |
| `create_ms`, `update_ms` | creation, and the last time the API's copy of the message changed for any reason |
| `message_position` | per-chat monotonic position; negative for thread replies (the API picks the sentinel, `-3` in current data) |
| `updated`, `deleted` | the API's own flags; `updated` also covers Feishu's post-send patches (mention resolution, link and time-phrase enrichment), so it is not an edit badge |
| `silenced` | a configured silence rule matched; the message is stored, listed and read like any other, but carries no badge and does not move its chat up the list |
| `edited_at` | when a sync first saw the body of a `text`/`post` message change alongside an `update_ms` change; 0 means never observed changing, which is also every backfilled message. The `update_ms` rider keeps a text send's answer (which echoes the request body) from reading as an edit when the stored body is Feishu's normalized one |
| `deleted_seen_at` | when the recall was first observed; `content_raw` keeps the last known body |
| `thread_id` | `omt_…` for thread roots and replies |
| `reply_to` | parent message of a direct reply |
| `mentions_json` | the message's mentions, `[{id,key,name}]`, taken from the body as it arrives rather than from its rendering, so it is set as soon as the row is. `key` is the `@_user_n` placeholder the body spells the mention as. Stored minified, like every JSON column here, so an id can be matched as text |
| `reactions_json` | reaction summary, `{counts:[{reaction_type,count}], details:[{emoji_type,operator:{operator_id,operator_type},action_time,…}]}`; empty when the message carries none. `count` and `action_time` are **strings**, the latter in Unix seconds. `counts` is the server's total and arrives alphabetically; `details` is one page of the individual reactions, so it may not name every reactor. The client's own order is by each emoji's earliest `action_time` |
| `raw_json` | the API item as received |
| `rendered_at` | 0 = rendering pending (also reset when `update_ms` changes) |
| `first_seen_at` | when the message was first stored; never rewritten. `first_seen_at - create_ms` is how long it took larkim to find the message, which is the only end-to-end latency the database records |
| `last_seen_at` | when a sync last saw the message in an API response. Every listing re-reads an overlap and the repair pass re-lists a week, so this moves on messages nothing about which changed |

`silenced` is derived from the `silence` rules of the config the writing process loaded: a rule's `chat`, `sender` and `contains` fields are an AND, the rules are an OR, and `contains` reads `content` once the rendering lands and `content_raw` until then. Whichever process stores a message stamps the flag, and again when a rendering lands; the one holding `daemon.lock` rebuilds the whole column when the rule set changes, so an edited config reaches the rows already stored once that process restarts.

A `system` message is its `template` with the values the same body carries filled in (`from_user`, `to_chatters`, `divider_text`). Feishu ships no value for the remaining slots, so `{old_group_name}`, `{count}` and the like read as `…` rather than as the placeholder.

A `video_chat` message is a call, and its body (`topic`, `meet_number`, `start_time`, `end_time`) is all the rendering needs: `[Video call] 站会的视频会议 · 100000000 · 32s`, leaving out whatever the body does not carry. `end_time` arrives with the update that closes the call, so a message carrying none is a call still running and its rendering has no length yet.

The three calendar types are an event, and their bodies (`summary`, `start_time`, `end_time`, `open_calendar_id`, `open_event_id`) are all the rendering needs: `[Event] 平台组周会 · 2026-08-31 10:30 ~ 12:00`, or `[Shared Event] …` for a `share_calendar_event`. Timestamps are Unix milliseconds written as strings, and read as seconds when they are too small to be milliseconds. The closing half of a span drops its date when the event ends on the day it started. `share_calendar_event` and `general_calendar` bodies also carry a `share_token`, which is the credential that joins the event and is deliberately left out of the rendering.

Feishu closes a call with a `system` message whose template is a single space. The API carries no text for it, so the rendering comes from the newest `video_chat` message before it in the same chat, whose `end_time` falls within five seconds of the marker's `create_ms`: `Meeting ended: 32s`, over the two largest units (`32s`, `24m28s`, `1h52m`). A p2p call leaves no `video_chat` message behind, so those read `Call ended`.

`reactions_json` is the one column that keeps changing after a message is rendered, and it does not ride the rendering pass: Feishu leaves `update_ms` alone when somebody reacts, so `rendered_at` is never reset and the renderer never comes back. Two passes refresh it on its own. Every sync tick asks about the newest message of the 20 liveliest p2p chats, which is one batched call and what keeps `chats.last_reactions_json` current for the chat list. Opening a chat in the TUI asks about its newest 20 messages, at most once every 30 seconds per chat. Anything outside both keeps whatever summary its rendering left, so a consumer that needs current reactions must ask Feishu itself rather than trust an old row.

Canonical ordering: `ORDER BY create_ms, message_position, id`.

## forwarded_messages, forwarded_roots

A `merge_forward` message is a container: its body is the literal string `Merged and Forwarded Message`, and what it carries is a bundle of other messages. Those are not copies. `GET /open-apis/im/v1/messages/{root_id}` answers with the *original* messages — the same `message_id`, the `chat_id` of the chat each was actually sent to, which is usually not the chat the bundle landed in and is often one larkim has never synced. They cannot go in `messages`: filed there they would either overwrite a real message's `message_position` with the one the bundle context reports, or conjure a chat's whole flow out of messages nobody ever listed.

| `forwarded_messages` column | meaning |
|---|---|
| `root_message_id` | the bundle, a real row in `messages` |
| `upper_message_id` | the direct parent: the bundle's own id at the top level, a nested bundle's id below it |
| `message_id` | the ORIGINAL message's id, which also exists in `messages` when its chat is synced |
| `seq` | order among siblings, by create time |
| `chat_id` | the ORIGINAL chat. **It does not mean larkim holds that chat**; do not union this table with `messages` |
| `msg_type`, `sender_id`, `sender_name`, `create_ms`, `content_raw`, `mentions_json`, `raw_json` | the message as the bundle reports it |
| `reactions_json` | who reacted to the ORIGINAL message, in the same `{counts, details}` shape as `messages.reactions_json`. Asked for by `reactions/batch_query` alongside the expansion, because the expansion itself carries none. Empty for a child of a chat the reader is not in: Feishu answers `no_permission` for those |

The primary key is `(root_message_id, upper_message_id, message_id)`. One message can sit at two depths of the same bundle — forwarded alone, and again inside a stretch of history that was forwarded whole — and both belong.

There is no `content` column: a child's text comes from `content_raw`, rendered by the same per-type renderers a message in a chat gets. The bundle's own `messages.content` is that text, laid out one child per `[timestamp] sender:` header with its body indented four spaces and wrapped in `<forwarded_messages>` — a nested bundle opens in place, indented with the message carrying it. Its attachments are registered against the **bundle's** id in `message_resources`, because the resource endpoint refuses a child message's id.

A bundle is rendered only once `forwarded_roots.fetched_at` is set, which happens both when the children land and when Feishu refuses to hand them over; a refused bundle renders to `<forwarded_messages/>` and settles.

`reactions_json` is a snapshot taken when the bundle expanded. The messages inside a forward are frozen, but reactions on the originals are not, and nothing comes back for them: re-expanding the bundle is what refreshes them.

`forwarded_roots` is the expansion queue, one row per bundle.

| column | meaning |
|---|---|
| `root_message_id` | the bundle; primary key, and a foreign key into `messages` |
| `fetched_at` | when the bundle stopped being owed, whether it expanded or Feishu refused it for good; `0` means still queued |
| `child_count` | the top level alone — the children of the bundle itself, not of the bundles nested in it — which is what one screen of the TUI lists |
| `attempts`, `next_attempt_at`, `last_error` | retry bookkeeping. A refusal is settled by `fetched_at`, not by `next_attempt_at`, which goes back to `0` |

A refusal is an answer rather than a failure to retry: a forward is frozen, so the reader having left the source chat will read the same way tomorrow.

```sql
-- one level of a bundle, which is one screen
SELECT sender_name, msg_type, content_raw FROM forwarded_messages
WHERE root_message_id = 'om_xxx' AND upper_message_id = 'om_xxx' ORDER BY seq;
```

## read_state

Per-message read state, joined on `message_id`. A row exists for a message whose remote flag has been checked, and for one stored unread on arrival: somebody else's message, neither a system notice nor recalled, first stored within two minutes of being sent, which the client would show unread from the moment it landed.

| column | meaning |
|---|---|
| `is_read_remote` | NULL unknown, 0 unread, 1 read. Feishu OpenAPI read-status polling sets it from answers; a successful web-gateway mark-read watermark sets it to 1 for main-flow messages up to the posted position (`AcceptRemoteRead`). While `remote_checked_at` is 0 after such a write, the row is accepted but unconfirmed until the read-status probe confirms or reverts it. Messages older than the 7-day polling horizon go back to NULL, since nothing can refresh them |
| `remote_checked_at`, `check_count`, `next_check_at` | polling schedule for the remote flag; `remote_checked_at` is 0 on a row stored unread on arrival until Feishu first answers for it, and again after an accepted watermark until the probe confirms |

A chat's badge counts live rows where `is_read_remote` is 0 on an unsilenced message with a non-negative `message_position`; thread replies are left out. That is the same set `larkim read-all` walks for Feishu red dots. Silenced main-flow messages still flip on a successful watermark POST even though they do not increment the badge.

## todo_done

Per-message task completion, joined on `message_id`. A todo message's body never carries whether its task finished, and Feishu rewrites the message when it does, so the completion is kept here where the render queue reads it as data rather than off the rendering's glyph.

| column | meaning |
|---|---|
| `done` | 1 the task is finished. Written when a rendering is rewritten with the completion the task list last reported; a task the listing stops naming keeps the row it has |

Derived state: rebuild it from a fresh task listing matched to todo message bodies' `task_id`.

## silence_settle_queue

Chats a silence flag flip left with server-side unread, waiting for the sweep to push the web client's read watermark past the silenced messages (config `silence_sync: true`). Rows are written inside the transaction that flips the flags, by whichever process stores the message; drained by the `daemon.lock` holder, a few chats per tick. The row only names the chat — the watermark is re-derived from `read_state` when the row is drained.

| column | meaning |
|---|---|
| `attempts` | failed settles counted; past 10 the row is dropped and a warn logged, and the next silence flip in the chat queues it again |

Derived state: rebuild it by re-queuing every chat with a silenced message still unread server-side above `chats.silence_settled_pos`.

## drafts

What the reader has typed but not sent, one row per composer. Consumer-owned:
the daemon never writes it, and it is outside
the `data_rev` triggers, since the process that writes a draft is the one that
displays it.

| column | meaning |
|---|---|
| `chat_id` | `oc_…`, first half of the primary key |
| `frame_id` | which of the chat's composers this is, and second half of the key: `omt_…` for a thread, the tree's root `om_…` for a reply tree, and empty for the chat's own |
| `text` | the unsent composer contents; a row exists only while this is non-empty |
| `reply_to` | the message the draft answers, empty for none |
| `in_thread` | whether that reply lands inside the thread rather than the main flow |
| `updated_at` | last write |

The TUI draws a composer under each conversation column — the chat's own, and
one for the thread or reply tree standing in the right column — so a chat's
half-written message and an answer inside one of its threads are separate
drafts and are keyed apart here. Within one composer the widget is still shared
by every chat, which is what this table keeps a half-written message from
following the reader through. It is written when the reader leaves a chat or a
frame, when the terminal loses focus and on quit — not on every keystroke. Two
TUIs on one chat: last write wins, and nothing detects the conflict. Clearing a
composer deletes its row rather than storing an empty one, so the chat list has
nothing to draw a marker from — and nothing to stand in the row's gist line,
which falls back to the chat's last message.

A send that Feishu refused is not kept here. It stays in the TUI's in-memory
outbox as a `(failed)` bubble the reader resends with `.` or drops with `x`, so
it does not survive the process.

## draft_candidates

The drafts written for a P0 message (see `triage`): replies offered at the TUI
composer, and reactions the TUI puts on the source message. The process holding
`daemon.lock` writes them once the agent answers; the TUI reads them and clears
a message's row after a send left a composer it filled, after Feishu took a
reaction it suggested, or once the reader has dismissed the last draft it
offered. A row the reader has answered — spoken in the chat after `created_ms`,
or reacted to `mid` — stays in the table but no longer counts as pending.
One row per source message, so a re-draft overwrites. Unlike `drafts` this
table is inside the `data_rev` triggers — the writer is not the displayer —
and it is the only table here with a DELETE trigger.

| column | meaning |
|---|---|
| `mid` | the source message the drafts answer, and the primary key |
| `chat_id` | its chat |
| `draft` | reply 0, the one recommended; `''` on a row holding only reactions |
| `format` | `text` or `markdown`, applying to every reply of the mid |
| `extras` | minimal JSON array holding replies 1..n |
| `reactions` | minimal JSON array of `emoji_type` keys, each one Feishu takes as a reaction, most fitting first |
| `created_ms` | when the drafts were written, Unix ms UTC |

## triage

The verdict on each fresh arrival the process holding `daemon.lock` judged, and
how far the work it set off has got. A message is judged once, when it is
rendered, live, unsilenced and at most 15 minutes old; nothing older is ever
judged, so a backfill never reads as news. Rules decide first — the reader's
own messages, bots and empty bodies are `drop`; calls, p2p, @me, the
`notifications.watch` ids and the `notifications.keywords` patterns are `P0` —
and Jev decides what they leave open, a muted chat or a missing key leaving it
at `P1`. Outside `data_rev`: no TUI draws it. `larkim triage list` reads it.

| column | meaning |
|---|---|
| `message_id` | the judged message, and the primary key |
| `chat_id` | its chat |
| `level` | `P0`, `P1` or `drop` |
| `reason` | what decided: `self`, `non-user`, `empty`, `vc`, `p2p`, `at-me`, `watch-user`, `watch-chat`, `keyword:<pattern>`, `muted`, `p1` (no judge), `jev:<pick>` (`reply`, `act`, `fyi`, `chatter`), `jev:others` (a `reply` or `act` meant for someone else), `jev-error` |
| `jev_p` | Jev's probability that the message cannot wait; NULL when a rule decided |
| `jev_json` | Jev's whole answer as compact JSON: `model` (the version that answered, e.g. `jev-1.13.0`), `pick` (probability per `reply`/`act`/`fyi`/`chatter`), `fits` (same as `jev_p`), `nouls` (`to_reader`: probability the message is meant for the reader). A `jev:<pick>` reason means `reply`+`act` reached 0.5 together, naming the larger, or else the top option. NULL when a rule decided or the call failed |
| `judged_ms` | Unix ms UTC |
| `drafted_ms` | `P0` only: when drafting finished, whatever it produced; 0 while owed |
| `draft_tries` | failed drafting attempts; drafting stops at 2 |
| `notified_ms` | `P0` only: when the banner was settled — raised, or held back because the message was read elsewhere, already answered, older than 3 minutes, or the reader was in the client or a focused larkim; 0 while owed |

## reminders

Banners a P0 message asked for at a later time it named, planned with its
drafts and raised by the process holding `daemon.lock` once due. One per
message; a replan moves it.

| column | meaning |
|---|---|
| `message_id` | the message that named the time, and the primary key |
| `chat_id` | its chat |
| `fire_ms` | when the banner is due, Unix ms UTC |
| `title` | the banner's text |
| `fired_ms` | when it was raised, or dropped for being over 15 minutes late; 0 while owed |

## ai_sessions, ai_turns

The assistant panel's own conversations: one session per chat per
conversation, and one turn per question with its answer and the recorded
context the question was asked in — the anchor, the window size, the
composer text, the selection. Later actions on an answer (Retry and
Regenerate included) replay the recorded context rather than the live
cursor, so that record is what an answer's meaning rests on.

TUI-owned, like `drafts`: the daemon never writes these tables, and they sit
outside the `data_rev` triggers — the process that writes a turn is the one
that displays it. Ids are uuids minted in the TUI, so a stream key exists
before its row is inserted; turn rows are upserted whole, so an out-of-order
write cannot leave half a turn behind. A session is stored when its first
question is asked, never when it is merely opened.

| `ai_sessions` column | meaning |
|---|---|
| `id` | uuid, the primary key |
| `chat_id` | `oc_…` the session belongs to; the panel lists only the open chat's sessions |
| `title` | first line of the first question |
| `created_ms` | when the session was opened, Unix ms UTC; orders a chat's sessions |

| `ai_turns` column | meaning |
|---|---|
| `id` | uuid, the primary key |
| `session_id` | the session the question belongs to |
| `seq` | question order within the session |
| `ask` | the question as the reader typed it, which the list shows |
| `sent` | what the model was asked; differs when a snippet's text stands behind the name the reader typed |
| `anchor_id` | the message the question was about, '' for none; the message itself may since be gone |
| `thread_id` | the frame standing under the panel at ask time, '' for none |
| `window` | how many chat messages the question carried, 0 for the default |
| `compose` | the anchor's composer text at ask time |
| `sel` | minimal JSON array of message ids a VISUAL range left |
| `state` | 0 asking, 1 done, 2 failed, 3 stopped, 4 interrupted |
| `answer` | the answer text so far, Markdown |
| `err` | the failure's message on a failed answer |
| `at_ms` | when the question was asked, Unix ms UTC |
| `card_id` | the message an answer streamed into the chat as, '' when it stayed in the panel |

A row still `asking` at load is an answer the previous run never finished —
the TUI reads it as `interrupted`, and nobody owes it further.

## resources, message_resources

A Feishu resource key is globally unique, so `resources` holds one row per key: what the bytes are and whether they arrived. `message_resources` holds the references — which messages name which key — and many messages routinely share one, because a notification card's header picture is in every card its sender posts.

| `resources` column | meaning |
|---|---|
| `file_key` | the key, and the primary key |
| `type` | `image`, `file`, `cover` (a video's frame) or `sticker` |
| `local_path` | path relative to the data dir once `status = done`; stickers land in `resources/stickers/<file_key>`, with the extension of the picture's format appended when the bytes name one |
| `status` | `pending`, `done`, `failed`, `skipped` (over `resources.max_bytes`) |
| `attempts`, `next_attempt_at`, `last_error` | retry bookkeeping; `next_attempt_at = 0` on a `failed` row means nothing will try again |

The key identifies the bytes but is not enough to fetch them: an IM resource is served under a message (`/open-apis/im/v1/messages/{message_id}/resources/{file_key}`), so a row in `message_resources` is what makes a key reachable, and any of them will do. One fetch therefore answers every message that names the key, refusals included, which is the difference between one call and hundreds.

A message's attachments are read through the references:

```sql
SELECT r.file_key, r.type, r.local_path, r.status
FROM message_resources mr JOIN resources r ON r.file_key = mr.file_key
WHERE mr.message_id = 'om_xxx' ORDER BY r.file_key;
```

Feishu's resource API refuses a sticker's `file_key` (`234002 Unauthorized`) under every identity, so a sticker picture is copied out of the Lark client's own storage on this machine instead; a sticker the client has never drawn stays `failed`.

## resource_text

The writing in a picture, one row per key, read once through `/open-apis/optical_char_recognition/v1/image/basic_recognize` from the bytes `resources` already put on disk. A file key names fixed bytes, so a settled row is final: unlike a document title there is nothing to re-read.

| column | meaning |
|---|---|
| `file_key` | the picture's key, and the primary key; it joins `resources` and `message_resources` |
| `text` | the regions the recognizer returned, one per line, and empty when it found no writing |
| `status` | `done` (read, `text` may be empty), `failed`, `skipped` (past the recognizer's 5 MB limit) |
| `attempts`, `next_attempt_at`, `last_error` | retry bookkeeping; `next_attempt_at = 0` on a `failed` row means nothing will try again |

There is no queue and no scan cursor: what is owed is `resources` left-joined onto this table, so a row appears only once an attempt has been made. Only pictures are read — `type IN ('image','cover')` with `status = 'done'`.

Nothing on screen is drawn from this table. It is read into the transcript the assistant and the reaction suggester are given, where a picture's writing follows its message on a line of its own.

```sql
SELECT mr.message_id, t.text FROM resource_text t
JOIN message_resources mr ON mr.file_key = t.file_key
WHERE t.text <> '' AND mr.message_id = 'om_xxx' ORDER BY t.file_key;
```

## doc_titles

A Feishu document link arrives in a message as a bare URL and nothing else: the preview the client draws beside it is rendered there and then, out of a callback the owning app answers, and no API hands it to anyone else. `doc_titles` is what larkim reads instead, one row per document, so a link shared around a dozen chats is named once.

| column | meaning |
|---|---|
| `doc_type` | the type the URL path spells: `docx`, `doc`, `sheet`, `bitable`, `wiki`, `file`, `mindnote`, `slides`, `folder`, `baseform`, `minutes` |
| `token` | the token after it; with `doc_type` this is the primary key |
| `title` | the document's name once `status = done` |
| `resolved_type` | what a `wiki` node turned out to wrap, which is the type worth showing; empty until resolved |
| `status` | `pending`, `done`, `denied` |
| `next_attempt_at` | when the title is worth reading again on a `done` row, `0` on a `pending` one, unused on a `denied` one |

`baseform` and `minutes` are named one at a time, by `/open-apis/base/v3` and `/open-apis/minutes/v1` respectively: the batch endpoint below has no `doc_type` for a published Base form or a Minutes recording. Their `resolved_type` only ever repeats `doc_type`, because neither wraps anything the way a `wiki` node does.

The identity is the URL's spelling, not the document the server resolves it to: `/open-apis/drive/v1/metas/batch_query` takes `doc_type: "wiki"` and answers with the document the node holds, so the resolved token is knowable only after the call while the next message carrying that wiki URL has to find this row before one.

`denied` covers all three ways the endpoint refuses a document — an unsupported type (`970002`), no permission (`970003`), and no such document (`970005`). None of them changes by asking again, and a reader is told the same thing by each, so a `denied` row is an answer rather than a failure to retry.

```sql
SELECT doc_type, token, title, resolved_type FROM doc_titles WHERE status = 'done';
```

## messages_fts

FTS5 external-content index over `messages(content, sender_name)` with the trigram tokenizer, kept in step by triggers. Query it with `MATCH` for terms of three or more characters (`SELECT rowid FROM messages_fts WHERE messages_fts MATCH '"发布计划"'`); shorter terms need `instr()` on `messages.content`.

## contacts, chat_members

`contacts` caches users and bots seen as chat members or senders (`open_id`, `name`, `email`, `p2p_chat_id`, `avatar_url`, `avatar_path`, `is_bot`). `is_bot` is 1 for an app rather than a person, and it is the only thing that tells them apart: both carry an open id and a name. `avatar_url = 'none'` means the user has no fetchable avatar; `avatar_path = '-'` means the download failed and is not retried. `chat_members` maps `chat_id` → `member_id` with `member_type` (`user` or `bot`) and the time the membership was last confirmed; group member lists refresh daily. A p2p chat keeps no rows — its pair is `chats.p2p_target_id` and the reader. `chats.members_truncated` marks a chat whose roster the tenant's security config caps: the rows held for it are a part of the membership, so a consumer must not read their count as the size of the chat.

`enterprise_email`, `department` and `is_cross_tenant` come from a separate identity lookup, marked by `detail_checked_at`; a non-zero `detail_checked_at` with empty fields means the lookup ran and the tenant did not return that user. The number ending the `enterprise_email` local part (`liming01`) is the tenant's own disambiguator for same-named colleagues.

Avatar coverage depends on the app's directory scope: users outside it keep `avatar_url = 'none'`, because the only endpoint carrying avatar URLs rejects them. The identity fields have no such limit.

## apps

`apps` names the apps that reacted to a message (`app_id`, `name`, `checked_at`). A reaction names an app reactor by its app id (`cli_…`, `operator_type = 'app'` in `reactions_json`), not by the open id the same bot sends under in `contacts`, and nothing maps one to the other. A row appears once a stored reaction block names the app; `checked_at = 0` means its name has not been looked up yet, and a non-zero `checked_at` with an empty `name` means the tenant would not show the app.

## data_rev

A single row (`id = 1`) whose `rev` counts the changes a reader cares about in `messages`, `chats`, `read_state`, `resources`, `message_resources`, `contacts` and `apps`, advanced by triggers. Poll it to know that rows already read have gone stale: `max(messages.id)` moves only on insert, so it misses renderings, read-status flips, cards a bot rewrote in place and attachments that finished downloading.

Inserts always count, except in `apps`, where a row starts unnamed and only its name coming in counts. An update counts when it moves a column something renders; the columns that pace the syncer do not (`*_seen_at`, `cursor_ms`, `backfill_done_at`, `history_floor_ms`, `members_synced_at`, `mute_checked_at`, `repaired_at`, `raw_json`, `remote_checked_at`, `check_count`, `next_check_at`, `attempts`, `next_attempt_at`, `detail_checked_at`, `updated_at`). A full chat listing restamps `last_seen_at` on every row, so without that rule one refresh over unchanged data would tell every reader to re-read the whole list.

```sql
SELECT rev FROM data_rev;  -- changed since last poll? re-read what you display
```

## Consumer cursors

How far a consumer has got through the messages is its own state; nothing here records it, so two consumers never move each other's position.

`messages.id` is ingest order, handed out by the single writer in commit order. A consumer that keeps `SELECT max(id) FROM messages` and comes back with `WHERE m.id > ?` sees everything that landed since, a message whose `create_ms` is older than one it already holds included — which is what a backfill and the slow path produce.

`larkim messages list --after <message_id> --order asc --json` pages from an exclusive anchor on the canonical sort key instead, so it walks in conversation order, and `--before` walks back. A message ingested after the cursor had already passed its timestamp never appears in a later page, so a consumer that must drop none takes the ingest id.

## sync_state, sync_runs, events

`sync_state` is a key/value table: `watermark_ms` (end of the last fully searched window), `self_open_id`, `status` (`running` / `needs_login` / `error`), `last_error`, `last_tick_at`, `chats_refreshed_at`, `slow_path_at`, `silence_rev` (fingerprint of the silence rules the stored flags came from). `sync_runs` keeps the newest 1000 ticks with timing, counts and error text.

`events` keeps the newest 1000 decisions the syncer made that no other table records: `at_ms` (Unix ms UTC), `kind`, `subject` (the ids it is about) and a one-line `detail`. Kinds:

| kind | subject | what it means |
|---|---|---|
| `resource_gone` | `<file_key>` | Feishu will not serve these bytes again, under any message, so nothing retries them. The row in `resources` stays `failed` with `next_attempt_at = 0`. |

## Example queries

```sql
-- Unread-by-me messages from the last day, newest first, on the badge's rule
SELECT m.message_id, m.chat_id, m.sender_name, m.content
FROM messages m LEFT JOIN read_state r ON r.message_id = m.message_id
WHERE m.create_ms > (unixepoch() - 86400) * 1000 AND r.is_read_remote = 0 AND m.deleted = 0 AND m.message_position >= 0 AND m.silenced = 0
ORDER BY m.create_ms DESC;

-- A thread in order
SELECT message_id, sender_name, content FROM messages
WHERE thread_id = 'omt_xxx' ORDER BY create_ms, message_position, id;

-- Chats by recent activity, with what each one last said
SELECT chat_id, name, last_sender_name, last_content, last_message_ms
FROM chats WHERE left_at = 0 ORDER BY last_unsilenced_ms DESC LIMIT 20;
```
