-- The Feishu web client names a chat by a numeric id that the OpenAPI never
-- hands out, and mark_read.mode: web can only address a chat by it. The only
-- way to learn it is to match the web client's inbox against stored messages,
-- which needs one message both sides hold at the same moment; kept here, a
-- chat matched once stays addressable when the inbox later drops it (Done) or
-- lists it by a message not synced yet.
--
-- It belongs to the consumers that mark chats read, like local_read_at, and
-- the daemon never writes it. Empty means not matched yet.
ALTER TABLE chats ADD COLUMN web_chat_id TEXT NOT NULL DEFAULT '';
