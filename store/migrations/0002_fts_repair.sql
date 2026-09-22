-- Full-text search over rendered content and sender names. The trigram
-- tokenizer matches substrings, which is what CJK text needs (unicode61 would
-- treat a whole Chinese run as one token). Queries need at least 3 characters.
CREATE VIRTUAL TABLE messages_fts USING fts5(
    content, sender_name,
    content='messages', content_rowid='id',
    tokenize='trigram'
);
CREATE TRIGGER messages_fts_ai AFTER INSERT ON messages BEGIN
    INSERT INTO messages_fts(rowid, content, sender_name) VALUES (new.id, new.content, new.sender_name);
END;
CREATE TRIGGER messages_fts_ad AFTER DELETE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, content, sender_name) VALUES ('delete', old.id, old.content, old.sender_name);
END;
CREATE TRIGGER messages_fts_au AFTER UPDATE OF content, sender_name ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, content, sender_name) VALUES ('delete', old.id, old.content, old.sender_name);
    INSERT INTO messages_fts(rowid, content, sender_name) VALUES (new.id, new.content, new.sender_name);
END;
INSERT INTO messages_fts(messages_fts) VALUES ('rebuild');

-- Repair pass bookkeeping: when a chat's recent history was last re-listed to
-- pick up edits and recalls.
ALTER TABLE chats ADD COLUMN repaired_at INTEGER NOT NULL DEFAULT 0;
