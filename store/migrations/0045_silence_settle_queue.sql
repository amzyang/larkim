-- The chats a silence flag flip left with server-side unread, waiting for the
-- sweep to settle the Feishu read watermark past the silenced messages so the
-- official clients' dots follow the local rules too. Written inside the same
-- transaction as the flag flip, drained by sync's settle step.
--
-- Derived state: the row's plan is re-derived from the store when it is
-- drained, so nothing here has to survive a read or a later arrival, and a
-- dropped row costs nothing but a dot that stays up until the chat is read.
CREATE TABLE silence_settle_queue (
  chat_id  TEXT PRIMARY KEY,
  attempts INTEGER NOT NULL DEFAULT 0
) WITHOUT ROWID;

-- The watermark the settle last pushed for the chat. The watermark settles
-- the feed's unread count without touching Feishu's per-message read state
-- (measured on the wire), so a silenced message below it keeps
-- is_read_remote = 0 and would re-queue on every listing; this is the floor
-- below which nothing queues or settles again.
ALTER TABLE chats ADD COLUMN silence_settled_pos INTEGER NOT NULL DEFAULT 0;
