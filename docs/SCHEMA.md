# larkim SQLite schema

Database: `larkim db path` (default `~/.larkim/larkim.db`). WAL mode; readers never block the daemon. The authoritative DDL is `larkim schema` (embedded migrations in `store/migrations/`). All timestamps are Unix **milliseconds** in UTC unless the column name says otherwise.

Ownership: the daemon (or an embedded syncer holding `daemon.lock`) writes every table except `read_state.consumed_at`, which belongs to consumers (CLI, TUI).

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
| `last_msg_type`, `last_content`, `last_content_raw` | that message's type and body; `last_content` is empty until `last_rendered_at` is set, and stays empty for types that render to nothing (`system`) |
| `last_rendered_at`, `last_deleted` | that message's rendering state and recall flag |
| `muted`, `mute_checked_at` | the user's do-not-disturb setting and when it was last answered; 0 means it has never been asked |

A chat first seen only through a message (before the next full listing) exists with an empty name.

`muted` comes from `POST /im/v1/chat_user_setting/batch_get_mute_status` under user identity, since no chat listing carries it. The lookup rides the full chat refresh, covers at most 100 chats per round and only those with a message in the last 30 days, taking the longest unanswered first. Chats the API declines to answer for (non-member, malformed id) keep their previous `muted` and are stamped all the same, so `mute_checked_at` says when a chat was last asked about, not that the answer changed.

The `last_*` columns mirror the newest message whose `message_position` is non-negative, so the list shows what the chat's main flow shows: thread replies are excluded, thread roots are not. `UpsertMessages` and `UpdateRendered` rewrite them in their own transaction, which covers ingest, edits, recalls and rendering. Order chats by `last_message_ms` rather than an aggregate over `messages`.

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
| `content` | human-readable rendering by lark-cli (`+messages-mget`); empty until `rendered_at` is set |
| `create_ms`, `update_ms` | creation and last edit time |
| `message_position` | per-chat monotonic position; negative for thread replies (the API picks the sentinel, `-3` in current data) |
| `updated`, `deleted` | edited / recalled flags as reported by the API |
| `deleted_seen_at` | when the recall was first observed; `content_raw` keeps the last known body |
| `thread_id` | `omt_…` for thread roots and replies |
| `reply_to` | parent message of a direct reply |
| `mentions_json`, `reactions_json` | rendered mentions `[{key,id,name}]` and reaction summary |
| `raw_json` | the API item as received |
| `rendered_at` | 0 = rendering pending (also reset when `update_ms` changes) |

Canonical ordering: `ORDER BY create_ms, message_position, id`.

## read_state

Per-message read state, joined on `message_id`. Rows exist only for messages that were checked or consumed.

| column | meaning |
|---|---|
| `is_read_remote` | NULL unknown, 0 unread, 1 read, as reported by Feishu for the current user |
| `remote_checked_at`, `check_count`, `next_check_at` | polling schedule for the remote flag |
| `consumed_at` | local: a larkim consumer marked the message as seen (`larkim mark consumed`); never written by the daemon |

## resources

Attachments of a message (`image_key` / `file_key`), one row per key.

| column | meaning |
|---|---|
| `type` | `image` or `file` |
| `local_path` | path relative to the data dir once `status = done` |
| `status` | `pending`, `done`, `failed`, `skipped` (over `resources.max_bytes`) |
| `attempts`, `next_attempt_at`, `last_error` | retry bookkeeping |

## messages_fts

FTS5 external-content index over `messages(content, sender_name)` with the trigram tokenizer, kept in step by triggers. Query it with `MATCH` for terms of three or more characters (`SELECT rowid FROM messages_fts WHERE messages_fts MATCH '"发布计划"'`); shorter terms need `instr()` on `messages.content`.

## contacts, chat_members

`contacts` caches users and bots seen as chat members or senders (`open_id`, `name`, `email`, `p2p_chat_id`, `avatar_url`, `avatar_path`). `avatar_url = 'none'` means the user has no fetchable avatar; `avatar_path = '-'` means the download failed and is not retried. `chat_members` maps `chat_id` → `member_id` with the time the membership was last confirmed; group member lists refresh daily.

`enterprise_email`, `department` and `is_cross_tenant` come from a separate identity lookup, marked by `detail_checked_at`; a non-zero `detail_checked_at` with empty fields means the lookup ran and the tenant did not return that user. The number ending the `enterprise_email` local part (`chenjianwei01`) is the tenant's own disambiguator for same-named colleagues.

Avatar coverage depends on the app's directory scope: users outside it keep `avatar_url = 'none'`, because the only endpoint carrying avatar URLs rejects them. The identity fields have no such limit.

## sync_state, sync_runs

`sync_state` is a key/value table: `watermark_ms` (end of the last fully searched window), `self_open_id`, `status` (`running` / `needs_login` / `error`), `last_error`, `last_tick_at`, `chats_refreshed_at`, `slow_path_at`. `sync_runs` keeps the newest 1000 ticks with timing, counts and error text.

## Example queries

```sql
-- Unread-by-me messages from the last day, newest first
SELECT m.message_id, m.chat_id, m.sender_name, m.content
FROM messages m LEFT JOIN read_state r ON r.message_id = m.message_id
WHERE m.create_ms > (unixepoch() - 86400) * 1000 AND r.is_read_remote = 0 AND m.deleted = 0
ORDER BY m.create_ms DESC;

-- A thread in order
SELECT message_id, sender_name, content FROM messages
WHERE thread_id = 'omt_xxx' ORDER BY create_ms, message_position, id;

-- Chats by recent activity, with what each one last said
SELECT chat_id, name, last_sender_name, last_content, last_message_ms
FROM chats WHERE left_at = 0 ORDER BY last_message_ms DESC LIMIT 20;
```
