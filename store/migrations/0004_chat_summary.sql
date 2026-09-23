-- The chat list renders the newest message of every chat on every change
-- batch, which correlated subqueries cannot carry. These columns hold that
-- message; store.UpsertMessages and store.UpdateRendered keep them in step.
--
-- Thread replies are excluded (message_position is negative for them), so the
-- list always shows what the chat's main flow shows. Thread roots have a
-- non-negative position and do count.
ALTER TABLE chats ADD COLUMN last_message_id TEXT NOT NULL DEFAULT '';
ALTER TABLE chats ADD COLUMN last_message_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE chats ADD COLUMN last_sender_id TEXT NOT NULL DEFAULT '';
ALTER TABLE chats ADD COLUMN last_sender_name TEXT NOT NULL DEFAULT '';
ALTER TABLE chats ADD COLUMN last_sender_type TEXT NOT NULL DEFAULT '';
ALTER TABLE chats ADD COLUMN last_msg_type TEXT NOT NULL DEFAULT '';
ALTER TABLE chats ADD COLUMN last_content TEXT NOT NULL DEFAULT '';
ALTER TABLE chats ADD COLUMN last_content_raw TEXT NOT NULL DEFAULT '';
ALTER TABLE chats ADD COLUMN last_rendered_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE chats ADD COLUMN last_deleted INTEGER NOT NULL DEFAULT 0;

WITH latest AS (
    SELECT chat_id, message_id, create_ms, sender_id, sender_name, sender_type,
           msg_type, content, content_raw, rendered_at, deleted,
           ROW_NUMBER() OVER (PARTITION BY chat_id
               ORDER BY create_ms DESC, message_position DESC, id DESC) AS rn
    FROM messages
    WHERE message_position >= 0
)
UPDATE chats SET
    last_message_id  = latest.message_id,
    last_message_ms  = latest.create_ms,
    last_sender_id   = latest.sender_id,
    last_sender_name = latest.sender_name,
    last_sender_type = latest.sender_type,
    last_msg_type    = latest.msg_type,
    last_content     = latest.content,
    last_content_raw = latest.content_raw,
    last_rendered_at = latest.rendered_at,
    last_deleted     = latest.deleted
FROM latest
WHERE latest.chat_id = chats.chat_id AND latest.rn = 1;
