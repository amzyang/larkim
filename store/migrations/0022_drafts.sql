-- A draft belongs to the chat it was typed in, not to the composer: the
-- composer is one widget shared by every chat, so without this table what was
-- half-typed in one chat follows the reader into the next one and is sent to
-- the wrong person.
--
-- Consumer-owned, like read_state.local_read_at: the daemon never writes it.
-- Deliberately outside the data_rev triggers — a draft is written by the same
-- process that displays it, so counting it would make saving a draft tell that
-- process to reload.
CREATE TABLE drafts (
    chat_id    TEXT PRIMARY KEY,
    text       TEXT    NOT NULL DEFAULT '',
    reply_to   TEXT    NOT NULL DEFAULT '',
    in_thread  INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL DEFAULT 0
);
