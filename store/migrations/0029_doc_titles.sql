-- A Feishu document link pasted into a chat arrives as a bare URL: the
-- message body carries nothing else. The preview the client draws beside it
-- is rendered there and then, out of a callback the owning app answers, and
-- no API hands it to anyone else — so the only way to say what a link points
-- at is to ask drive/v1/metas/batch_query for the title ourselves.
--
-- The identity is (doc_type, token) as the URL spells it, not the token the
-- server resolves it to: a /wiki/ link is unwrapped on the way through, and
-- the next message carrying that same wiki URL has to find this row.

CREATE TABLE doc_titles (
    doc_type        TEXT NOT NULL,            -- as the URL path spells it: docx | doc | sheet | bitable | wiki | file | mindnote | slides | folder
    token           TEXT NOT NULL,
    title           TEXT NOT NULL DEFAULT '',
    resolved_type   TEXT NOT NULL DEFAULT '', -- what a wiki node turned out to be, which is what picks the glyph
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','done','denied')),
    -- Two jobs in one column: 0 on a pending row means ask now, a done row
    -- carries when its title is worth re-reading, and a denied row is out of
    -- the running whatever it holds. Documents get renamed, so a title that
    -- is never re-read eventually lies about what it names.
    next_attempt_at INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (doc_type, token)
);

CREATE TRIGGER doc_titles_rev_ai AFTER INSERT ON doc_titles BEGIN
    UPDATE data_rev SET rev = rev + 1;
END;

-- Only what a reader can see: the retry clock moving is not a redraw.
CREATE TRIGGER doc_titles_rev_au AFTER UPDATE ON doc_titles
WHEN (NEW.title, NEW.resolved_type, NEW.status)
 IS NOT (OLD.title, OLD.resolved_type, OLD.status)
BEGIN
    UPDATE data_rev SET rev = rev + 1;
END;
