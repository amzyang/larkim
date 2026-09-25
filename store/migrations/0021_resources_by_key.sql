-- A Feishu resource key is globally unique, so whether the bytes arrived and
-- where they landed are properties of the key. Keeping them per (message,
-- key) meant one notification card's header picture — referenced by every
-- card its sender posts — was fetched once per message, with its own retry
-- ledger each time.
--
-- The key is the identity; it is not the fetch coordinate. Asking Feishu for
-- an IM resource needs a message to ask under
-- (/open-apis/im/v1/messages/{message_id}/resources/{file_key}), so the
-- references move to their own table and any row in it is a way in.

CREATE TABLE resources_by_key (
    file_key        TEXT PRIMARY KEY,
    type            TEXT NOT NULL DEFAULT '',    -- image | file | cover | sticker
    local_path      TEXT NOT NULL DEFAULT '',    -- relative to the data dir
    size_bytes      INTEGER NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','done','failed','skipped')),
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT NOT NULL DEFAULT ''
);

-- Collapse the existing rows onto their key, keeping the most settled one:
-- min() makes SQLite take the bare columns from the row it matched.
INSERT INTO resources_by_key
    (file_key, type, local_path, size_bytes, status, attempts, next_attempt_at, last_error)
SELECT file_key, type, local_path, size_bytes, status, attempts, next_attempt_at, last_error FROM (
    SELECT file_key, type, local_path, size_bytes, status, attempts, next_attempt_at, last_error,
           min(CASE status WHEN 'done' THEN 0 WHEN 'skipped' THEN 1 WHEN 'failed' THEN 2 ELSE 3 END)
      FROM resources GROUP BY file_key
);

CREATE TABLE message_resources (
    message_id TEXT NOT NULL,
    file_key   TEXT NOT NULL,
    PRIMARY KEY (message_id, file_key)
);

INSERT INTO message_resources (message_id, file_key)
SELECT message_id, file_key FROM resources;

DROP TRIGGER resources_rev_ai;
DROP TRIGGER resources_rev_au;
DROP TABLE resources;
ALTER TABLE resources_by_key RENAME TO resources;

-- A message's pictures are read by key, so the reverse direction is the one
-- that needs an index: which messages can be asked for this key.
CREATE INDEX message_resources_key ON message_resources (file_key);

CREATE TRIGGER resources_rev_ai AFTER INSERT ON resources BEGIN
    UPDATE data_rev SET rev = rev + 1;
END;

CREATE TRIGGER resources_rev_au AFTER UPDATE ON resources
WHEN (NEW.type, NEW.local_path, NEW.size_bytes, NEW.status, NEW.last_error)
 IS NOT (OLD.type, OLD.local_path, OLD.size_bytes, OLD.status, OLD.last_error)
BEGIN
    UPDATE data_rev SET rev = rev + 1;
END;

-- A message gaining a reference makes its pictures drawable, which the watch
-- has to see.
CREATE TRIGGER message_resources_rev_ai AFTER INSERT ON message_resources BEGIN
    UPDATE data_rev SET rev = rev + 1;
END;
