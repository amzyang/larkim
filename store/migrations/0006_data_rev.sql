-- data_rev is the counter consumers poll to learn that what they display has
-- gone stale. The ingest id only moves on insert, so a consumer watching it
-- never hears about a rendering that landed, a read status that flipped or a
-- card a bot rewrote in place: those are updates to rows it already read.
--
-- Triggers rather than call sites, because a write path added later picks the
-- counter up for free. Nothing deletes from these tables (recalls set
-- messages.deleted), so insert and update cover every write.
CREATE TABLE data_rev (
    id  INTEGER PRIMARY KEY CHECK (id = 1),
    rev INTEGER NOT NULL DEFAULT 0
);

INSERT INTO data_rev (id, rev) VALUES (1, 0);

CREATE TRIGGER messages_rev_ai AFTER INSERT ON messages BEGIN
    UPDATE data_rev SET rev = rev + 1;
END;

CREATE TRIGGER messages_rev_au AFTER UPDATE ON messages BEGIN
    UPDATE data_rev SET rev = rev + 1;
END;

CREATE TRIGGER chats_rev_ai AFTER INSERT ON chats BEGIN
    UPDATE data_rev SET rev = rev + 1;
END;

CREATE TRIGGER chats_rev_au AFTER UPDATE ON chats BEGIN
    UPDATE data_rev SET rev = rev + 1;
END;

CREATE TRIGGER read_state_rev_ai AFTER INSERT ON read_state BEGIN
    UPDATE data_rev SET rev = rev + 1;
END;

CREATE TRIGGER read_state_rev_au AFTER UPDATE ON read_state BEGIN
    UPDATE data_rev SET rev = rev + 1;
END;

CREATE TRIGGER resources_rev_ai AFTER INSERT ON resources BEGIN
    UPDATE data_rev SET rev = rev + 1;
END;

CREATE TRIGGER resources_rev_au AFTER UPDATE ON resources BEGIN
    UPDATE data_rev SET rev = rev + 1;
END;

CREATE TRIGGER contacts_rev_ai AFTER INSERT ON contacts BEGIN
    UPDATE data_rev SET rev = rev + 1;
END;

CREATE TRIGGER contacts_rev_au AFTER UPDATE ON contacts BEGIN
    UPDATE data_rev SET rev = rev + 1;
END;
