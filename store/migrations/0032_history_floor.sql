-- How far back each chat has been pulled. Read with backfill_done_at: until
-- that is set the column means nothing, after it a positive value is the
-- oldest create_ms asked for and 0 means the whole chat is stored.
ALTER TABLE chats ADD COLUMN history_floor_ms INTEGER NOT NULL DEFAULT 0;

-- Chats already backfilled get the oldest message they hold rather than the
-- window backfill_days actually covered, which nothing recorded. It is the
-- conservative of the two: it never claims to reach further back than it does.
UPDATE chats SET history_floor_ms =
    COALESCE((SELECT MIN(m.create_ms) FROM messages m WHERE m.chat_id = chats.chat_id), backfill_done_at)
  WHERE backfill_done_at > 0;
