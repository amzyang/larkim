-- A draft belongs to the box it was typed in, and there are two: the one under
-- the message panes, which is the chat's, and the one the right column carries
-- for the frame standing in it. Without the second key a thread's half-written
-- answer and the chat's half-written message would overwrite each other.
--
-- frame_id is the right-column frame the draft belongs to: `omt_…` for a
-- thread, the tree's root `om_…` for a reply tree, and '' for the chat's own
-- box. SQLite cannot widen a primary key in place, so the table is rebuilt and
-- the rows it had become the chat's.
CREATE TABLE drafts_new (
    chat_id    TEXT    NOT NULL,
    frame_id   TEXT    NOT NULL DEFAULT '',
    text       TEXT    NOT NULL DEFAULT '',
    reply_to   TEXT    NOT NULL DEFAULT '',
    in_thread  INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (chat_id, frame_id)
);

INSERT INTO drafts_new (chat_id, frame_id, text, reply_to, in_thread, updated_at)
    SELECT chat_id, '', text, reply_to, in_thread, updated_at FROM drafts;

DROP TABLE drafts;
ALTER TABLE drafts_new RENAME TO drafts;
