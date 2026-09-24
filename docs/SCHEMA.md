# larkim SQLite schema

Database: `larkim db path` (default `~/.larkim/larkim.db`). WAL mode; readers never block the daemon. The authoritative DDL is `larkim schema` (embedded migrations in `store/migrations/`). All timestamps are Unix **milliseconds** in UTC unless the column name says otherwise.

Ownership: the daemon (or an embedded syncer holding `daemon.lock`) writes every table except `read_state.consumed_at` and `read_state.local_read_at`, which belong to consumers (CLI, TUI).

## chats

One row per chat the user is (or was) in, from `GET /im/v1/chats` with `types=p2p,group`.

| column | meaning |
|---|---|
| `chat_id` | `oc_…` primary key |
| `name` | group name; for p2p, the peer's display name |
| `chat_mode` | `group`, `topic` or `p2p` |
| `chat_status` | `normal`, `dissolved`, `dissolved_save` |
| `p2p_target_id`, `p2p_target_type` | peer open id and `user`/`bot` for p2p chats |
| `avatar_url`, `avatar_path` | group avatar URL and local copy (relative to the data dir); empty for p2p |
| `cursor_ms` | newest `create_ms` pulled by a per-chat listing; the slow path resumes from here minus overlap |
| `backfill_done_at` | set once the historical pull (`backfill_days`) finished |
| `left_at` | non-zero when a full listing no longer contains the chat; reset when it reappears |
| `sync_error` | last permanent API rejection (e.g. restricted-mode chats cannot be listed); such chats still receive messages via search |
| `repaired_at` | when the last repair pass re-listed the chat's recent week |
| `raw_json` | the API item as received |
| `last_message_id`, `last_message_ms` | the chat's newest main-flow message; empty and 0 when it has none |
| `last_sender_id`, `last_sender_name`, `last_sender_type` | that message's sender |
| `last_msg_type`, `last_content`, `last_content_raw` | that message's type and body; `last_content` is empty until `last_rendered_at` is set |
| `last_mentions_json` | that message's rendered mentions, the shape `messages.mentions_json` holds; empty until the rendering lands |
| `last_reactions_json` | that message's reaction block, the shape `messages.reactions_json` holds; empty while nobody has reacted |
| `last_rendered_at`, `last_deleted` | that message's rendering state and recall flag |
| `last_unsilenced_ms` | the newest main-flow message no silence rule matched, and the key the list orders on; 0 when every message is silenced |
| `muted`, `mute_checked_at` | the user's do-not-disturb setting and when it was last answered; 0 means it has never been asked |

A chat first seen only through a message (before the next full listing) exists with an empty name.

`muted` comes from `POST /im/v1/chat_user_setting/batch_get_mute_status` under user identity, since no chat listing carries it. The lookup rides the full chat refresh, covers at most 100 chats per round and only those with a message in the last 30 days, taking the longest unanswered first. Chats the API declines to answer for (non-member, malformed id) keep their previous `muted` and are stamped all the same, so `mute_checked_at` says when a chat was last asked about, not that the answer changed.

The `last_*` columns mirror the newest message whose `message_position` is non-negative, so the list shows what the chat's main flow shows: thread replies are excluded, thread roots are not. `UpsertMessages` and `UpdateRendered` rewrite them in their own transaction, which covers ingest, edits, recalls and rendering. Order chats by `last_unsilenced_ms` rather than by `last_message_ms` or an aggregate over `messages`: the `last_*` columns say what arrived last, the sort key says what last mattered, and the two differ exactly where a silence rule matched. `ListChats` puts the chats carrying an unread, unsilenced main-flow message ahead of that, on the same rule the TUI badge counts by.

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
| `content` | human-readable rendering; empty until `rendered_at` is set. `system` messages are rendered in process from bodies already on disk, every other type by lark-cli (`+messages-mget`) |
| `create_ms`, `update_ms` | creation, and the last time the API's copy of the message changed for any reason |
| `message_position` | per-chat monotonic position; negative for thread replies (the API picks the sentinel, `-3` in current data) |
| `updated`, `deleted` | the API's own flags; `updated` also covers Feishu's post-send patches (mention resolution, link and time-phrase enrichment), so it is not an edit badge |
| `silenced` | a configured silence rule matched; the message is stored, listed and read like any other, but carries no badge and does not move its chat up the list |
| `edited_at` | when a sync first saw the body of a `text`/`post` message change; 0 means never observed changing, which is also every backfilled message |
| `deleted_seen_at` | when the recall was first observed; `content_raw` keeps the last known body |
| `thread_id` | `omt_…` for thread roots and replies |
| `reply_to` | parent message of a direct reply |
| `mentions_json` | rendered mentions, `[{key,id,name}]` |
| `reactions_json` | reaction summary, `{counts:[{reaction_type,count}], details:[{emoji_type,operator:{operator_id,operator_type},action_time,…}]}`; empty when the message carries none. `count` and `action_time` are **strings**, the latter in Unix seconds. `counts` is the server's total and arrives alphabetically; `details` is one page of the individual reactions, so it may not name every reactor. The client's own order is by each emoji's earliest `action_time` |
| `raw_json` | the API item as received |
| `rendered_at` | 0 = rendering pending (also reset when `update_ms` changes) |

`silenced` is derived from the `silence` rules of the writer's config: a rule's `chat`, `sender` and `contains` fields are an AND, the rules are an OR, and `contains` reads `content` once the rendering lands and `content_raw` until then. The process holding `daemon.lock` stamps the flag as messages arrive and again when a rendering lands, and rebuilds the whole column when the rule set changes, so the column states what that process's config says — a reader that edits the config sees nothing until the writer restarts.

A `system` message is its `template` with the values the same body carries filled in (`from_user`, `to_chatters`, `divider_text`). Feishu ships no value for the remaining slots, so `{old_group_name}`, `{count}` and the like read as `…` rather than as the placeholder.

Feishu closes a call with a `system` message whose template is a single space. The API carries no text for it, so the rendering comes from the newest `video_chat` message before it in the same chat, whose `end_time` falls within five seconds of the marker's `create_ms`: `Meeting ended: 32s`, over the two largest units (`32s`, `24m28s`, `1h52m`). A p2p call leaves no `video_chat` message behind, so those read `Call ended`.

`reactions_json` is the one column that keeps changing after a message is rendered, and it does not ride the rendering pass: Feishu leaves `update_ms` alone when somebody reacts, so `rendered_at` is never reset and the renderer never comes back. Two passes refresh it on its own. Every sync tick asks about the newest message of the 20 liveliest p2p chats, which is one batched call and what keeps `chats.last_reactions_json` current for the chat list. Opening a chat in the TUI asks about its newest 20 messages, at most once every 30 seconds per chat. Anything outside both keeps whatever summary its rendering left, so a consumer that needs current reactions must ask Feishu itself rather than trust an old row.

Canonical ordering: `ORDER BY create_ms, message_position, id`.

## read_state

Per-message read state, joined on `message_id`. Rows exist only for messages that were checked or consumed; `local_read_at` updates rows that already exist and never creates one.

| column | meaning |
|---|---|
| `is_read_remote` | NULL unknown, 0 unread, 1 read, as reported by Feishu for the current user. The flag is a per-message read receipt, which flips only on messages the user actually viewed; it is not the client's chat-level badge. Messages older than the 7-day polling horizon go back to NULL, since nothing can refresh them |
| `remote_checked_at`, `check_count`, `next_check_at` | polling schedule for the remote flag |
| `local_read_at` | local: the reader had the message in front of them in larkim, which is set for a whole chat at once when it is opened. Feishu offers no way to write a read receipt, so this is what lets a badge fall without leaving larkim; the Feishu client's own red dot is unaffected |
| `consumed_at` | local: a larkim consumer marked the message as seen (`larkim mark consumed`), a processing cursor for scripts rather than a record of a person reading; never written by the daemon |

A chat's badge counts the rows where `is_read_remote` is 0 and `local_read_at` is 0 on a live, unsilenced message with a non-negative `message_position`; thread replies are left out. Both flags can only witness that a message was seen, so taking either one as read adds no false unread. Marking a chat read is deliberately wider than the badge: it takes every such row, silenced ones included, because the reader had them in front of them too.

## resources

Attachments of a message, one row per key: an `image` or `file` body's key, the keys embedded in a rich-text post, the images an `interactive` card holds in its attachment table, and the picture a `sticker` names.

| column | meaning |
|---|---|
| `type` | `image`, `file` or `sticker` |
| `local_path` | path relative to the data dir once `status = done`; stickers land in `resources/stickers/<file_key>`, with the extension of the picture's format appended when the bytes name one |
| `status` | `pending`, `done`, `failed`, `skipped` (over `resources.max_bytes`) |
| `attempts`, `next_attempt_at`, `last_error` | retry bookkeeping |

Feishu's resource API refuses a sticker's `file_key` (`234002 Unauthorized`) under every identity, so a sticker picture is copied out of the Lark client's own storage on this machine instead; a sticker the client has never drawn stays `failed`.

## messages_fts

FTS5 external-content index over `messages(content, sender_name)` with the trigram tokenizer, kept in step by triggers. Query it with `MATCH` for terms of three or more characters (`SELECT rowid FROM messages_fts WHERE messages_fts MATCH '"发布计划"'`); shorter terms need `instr()` on `messages.content`.

## contacts, chat_members

`contacts` caches users and bots seen as chat members or senders (`open_id`, `name`, `email`, `p2p_chat_id`, `avatar_url`, `avatar_path`). `avatar_url = 'none'` means the user has no fetchable avatar; `avatar_path = '-'` means the download failed and is not retried. `chat_members` maps `chat_id` → `member_id` with the time the membership was last confirmed; group member lists refresh daily.

`enterprise_email`, `department` and `is_cross_tenant` come from a separate identity lookup, marked by `detail_checked_at`; a non-zero `detail_checked_at` with empty fields means the lookup ran and the tenant did not return that user. The number ending the `enterprise_email` local part (`liming01`) is the tenant's own disambiguator for same-named colleagues.

Avatar coverage depends on the app's directory scope: users outside it keep `avatar_url = 'none'`, because the only endpoint carrying avatar URLs rejects them. The identity fields have no such limit.

## data_rev

A single row (`id = 1`) whose `rev` counts every insert and update to `messages`, `chats`, `read_state`, `resources` and `contacts`, advanced by triggers. Poll it to know that rows already read have gone stale: `max(messages.id)` moves only on insert, so it misses renderings, read-status flips, cards a bot rewrote in place and attachments that finished downloading.

```sql
SELECT rev FROM data_rev;  -- changed since last poll? re-read what you display
```

## sync_state, sync_runs

`sync_state` is a key/value table: `watermark_ms` (end of the last fully searched window), `self_open_id`, `status` (`running` / `needs_login` / `error`), `last_error`, `last_tick_at`, `chats_refreshed_at`, `slow_path_at`, `silence_rev` (fingerprint of the silence rules the stored flags came from). `sync_runs` keeps the newest 1000 ticks with timing, counts and error text.

## Example queries

```sql
-- Unread-by-me messages from the last day, newest first, on the badge's rule
SELECT m.message_id, m.chat_id, m.sender_name, m.content
FROM messages m LEFT JOIN read_state r ON r.message_id = m.message_id
WHERE m.create_ms > (unixepoch() - 86400) * 1000 AND r.is_read_remote = 0 AND r.local_read_at = 0 AND m.deleted = 0 AND m.silenced = 0
ORDER BY m.create_ms DESC;

-- A thread in order
SELECT message_id, sender_name, content FROM messages
WHERE thread_id = 'omt_xxx' ORDER BY create_ms, message_position, id;

-- Chats by recent activity, with what each one last said
SELECT chat_id, name, last_sender_name, last_content, last_message_ms
FROM chats WHERE left_at = 0 ORDER BY last_unsilenced_ms DESC LIMIT 20;
```
