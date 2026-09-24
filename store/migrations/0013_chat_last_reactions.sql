-- The chat list draws the reactions on a p2p chat's newest message, which the
-- summary columns did not carry. It travels with the other last_* columns:
-- store.UpsertMessages and store.UpdateReactions keep it in step.
ALTER TABLE chats ADD COLUMN last_reactions_json TEXT NOT NULL DEFAULT '';

WITH latest AS (
    SELECT chat_id, reactions_json,
           ROW_NUMBER() OVER (PARTITION BY chat_id
               ORDER BY create_ms DESC, message_position DESC, id DESC) AS rn
    FROM messages
    WHERE message_position >= 0
)
UPDATE chats SET last_reactions_json = latest.reactions_json
FROM latest
WHERE latest.chat_id = chats.chat_id AND latest.rn = 1;
