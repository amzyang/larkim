-- A picture in a chat is content the message body does not repeat: a stack
-- trace, a schedule, a design with its notes. The assistant and the reaction
-- suggester are handed the rendered text, where all of that is the single
-- token [Image: img_…], so none of it reaches them.
--
-- The text is read once per key and never again. A file key names fixed
-- bytes, so unlike a document title there is nothing to re-read: a row here
-- is final whichever of the three states it settles in.
--
-- No queue and no back-scan cursor: resources is already the register of
-- every picture on disk, so what is owed is that table left-joined onto this
-- one. A row appears here only once an attempt has been made.
CREATE TABLE resource_text (
    file_key        TEXT PRIMARY KEY,
    text            TEXT NOT NULL DEFAULT '',  -- the regions the recognizer returned, one per line
    status          TEXT NOT NULL CHECK (status IN ('done','failed','skipped')),
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT NOT NULL DEFAULT ''
);

-- No data_rev trigger: nothing a reader looks at is drawn from this table.
