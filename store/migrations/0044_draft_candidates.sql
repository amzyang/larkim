-- draft_candidates mirrors lark-watch's pending reply drafts into larkim's
-- own store so the TUI can offer them at the composer. lark-watch owns the
-- writes: one row per pending source message (PK mid, same key as lark-watch's
-- own pending table), upserted on send-card and deleted when the pending
-- resolves (card send or ignore, send-draft, send-text, the 24h TTL sweep).
-- The TUI only reads them and clears one row after the reader answered from a
-- composer it filled; the Feishu confirmation card stays lark-watch's.

CREATE TABLE draft_candidates (
    mid        TEXT PRIMARY KEY,            -- source message the drafts answer
    chat_id    TEXT NOT NULL,
    draft      TEXT NOT NULL,               -- candidate 0
    format     TEXT NOT NULL DEFAULT 'text',
    extras     TEXT NOT NULL DEFAULT '[]',  -- minimal JSON array, candidates 1..n
    created_ms INTEGER NOT NULL             -- Unix ms UTC
);

CREATE INDEX draft_candidates_chat ON draft_candidates(chat_id);

-- Unlike drafts this table is inside the data_rev triggers: the process that
-- writes it (lark-watch) is not the one that displays it (the TUI). It is
-- also the first table here that deletes, so it carries a DELETE trigger.

CREATE TRIGGER draft_candidates_rev_ai AFTER INSERT ON draft_candidates BEGIN
    UPDATE data_rev SET rev = rev + 1;
END;

CREATE TRIGGER draft_candidates_rev_ad AFTER DELETE ON draft_candidates BEGIN
    UPDATE data_rev SET rev = rev + 1;
END;
