-- The chat list styles the @ runs of its summary line, which takes the
-- mentions of the message that summary came from. The column travels with the
-- other last_* columns: store.UpsertMessages and store.UpdateRendered keep it
-- in step.
ALTER TABLE chats ADD COLUMN last_mentions_json TEXT NOT NULL DEFAULT '';

WITH latest AS (
    SELECT chat_id, mentions_json,
           ROW_NUMBER() OVER (PARTITION BY chat_id
               ORDER BY create_ms DESC, message_position DESC, id DESC) AS rn
    FROM messages
    WHERE message_position >= 0
)
UPDATE chats SET last_mentions_json = latest.mentions_json
FROM latest
WHERE latest.chat_id = chats.chat_id AND latest.rn = 1;
