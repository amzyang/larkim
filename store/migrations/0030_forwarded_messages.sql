-- A merge_forward's children are the original messages of their own chats:
-- they carry the source chat_id and the source message_id, which already
-- exist as real rows in messages. They cannot live there — writing them would
-- either overwrite a real message's position or file them under a chat larkim
-- never synced — so they get a table of their own.
--
-- The key carries upper_message_id because one message can sit at two depths
-- of the same bundle: someone forwarded it alone, and forwarded a stretch of
-- history containing it, and both went into one merge. Keyed without the
-- parent, the second copy would silently replace the first and a level would
-- come up a row short.

CREATE TABLE forwarded_messages (
    root_message_id  TEXT    NOT NULL,  -- the bundle in messages
    upper_message_id TEXT    NOT NULL,  -- direct parent; the root at the top level
    message_id       TEXT    NOT NULL,  -- the ORIGINAL message's id, not a copy
    seq              INTEGER NOT NULL,  -- order among siblings, by create time
    chat_id          TEXT    NOT NULL,  -- the ORIGINAL chat, often one larkim never synced
    msg_type         TEXT    NOT NULL,
    sender_id        TEXT    NOT NULL,
    sender_name      TEXT    NOT NULL,
    create_ms        INTEGER NOT NULL,
    content_raw      TEXT    NOT NULL,
    mentions_json    TEXT    NOT NULL DEFAULT '',
    raw_json         TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (root_message_id, upper_message_id, message_id)
) WITHOUT ROWID;

CREATE INDEX forwarded_children ON forwarded_messages(root_message_id, upper_message_id, seq);

-- One row per bundle: the work queue, its backoff, and the child count the
-- collapsed summary reads without counting rows. child_count is the top level
-- alone, matching what one frame lists — a number the reader can check
-- against the rows in front of them.
CREATE TABLE forwarded_roots (
    root_message_id TEXT PRIMARY KEY,
    fetched_at      INTEGER NOT NULL DEFAULT 0,
    child_count     INTEGER NOT NULL DEFAULT 0,
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT    NOT NULL DEFAULT '',
    FOREIGN KEY (root_message_id) REFERENCES messages(message_id)
) WITHOUT ROWID;

CREATE TRIGGER forwarded_messages_rev_ai AFTER INSERT ON forwarded_messages BEGIN
    UPDATE data_rev SET rev = rev + 1;
END;

-- Only what a reader can see: the retry clock moving is not a redraw, but the
-- count and the refusal are both on the summary line.
CREATE TRIGGER forwarded_roots_rev_au AFTER UPDATE ON forwarded_roots
WHEN (NEW.child_count, NEW.last_error) IS NOT (OLD.child_count, OLD.last_error)
BEGIN
    UPDATE data_rev SET rev = rev + 1;
END;

-- Backfill: seed the queue with every bundle already stored.
INSERT OR IGNORE INTO forwarded_roots (root_message_id)
  SELECT message_id FROM messages WHERE msg_type = 'merge_forward' AND deleted = 0;
