-- The assistant's own conversations, one session per chat per conversation,
-- and one row per question with the answer and the context the question was
-- asked in — the anchor, the window size, the draft, the selection — because
-- every later action on an answer (Retry and Regenerate included) replays the
-- recorded context rather than the live cursor.
--
-- TUI-owned, like drafts: the daemon never writes these tables, and they sit
-- outside the data_rev triggers for the same reason — the process that writes
-- a turn is the one that displays it.
--
-- Ids are uuids minted in the TUI, so a stream key exists before its row is
-- inserted and an answer finishing off screen can write itself. Turn rows are
-- upserted whole, so an out-of-order write cannot leave half a turn behind.
-- A row still marked asking at load is an answer the process never finished:
-- the TUI reads it as interrupted.
CREATE TABLE ai_sessions (
    id         TEXT PRIMARY KEY,
    chat_id    TEXT    NOT NULL,
    title      TEXT    NOT NULL DEFAULT '',
    created_ms INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX ai_sessions_chat ON ai_sessions(chat_id, created_ms);

-- state mirrors the TUI's turn states: 0 asking, 1 done, 2 failed,
-- 3 stopped, 4 interrupted. sel is a minimal JSON array of message ids.
CREATE TABLE ai_turns (
    id         TEXT PRIMARY KEY,
    session_id TEXT    NOT NULL,
    seq        INTEGER NOT NULL,
    ask        TEXT    NOT NULL DEFAULT '',
    sent       TEXT    NOT NULL DEFAULT '',
    draft      INTEGER NOT NULL DEFAULT 0,
    anchor_id  TEXT    NOT NULL DEFAULT '',
    thread_id  TEXT    NOT NULL DEFAULT '',
    window     INTEGER NOT NULL DEFAULT 0,
    compose    TEXT    NOT NULL DEFAULT '',
    sel        TEXT    NOT NULL DEFAULT '[]',
    state      INTEGER NOT NULL DEFAULT 0,
    answer     TEXT    NOT NULL DEFAULT '',
    err        TEXT    NOT NULL DEFAULT '',
    at_ms      INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX ai_turns_session ON ai_turns(session_id, seq);

DELETE FROM ai_turns WHERE session_id NOT IN (SELECT id FROM ai_sessions);
