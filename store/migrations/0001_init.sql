-- All timestamps are Unix milliseconds in UTC unless the column name says otherwise.

CREATE TABLE sync_state (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE chats (
    chat_id           TEXT PRIMARY KEY,          -- oc_xxx
    name              TEXT NOT NULL DEFAULT '',
    description       TEXT NOT NULL DEFAULT '',
    chat_mode         TEXT NOT NULL DEFAULT '',  -- group | topic | p2p
    chat_status       TEXT NOT NULL DEFAULT '',  -- normal | dissolved | dissolved_save
    owner_id          TEXT NOT NULL DEFAULT '',
    external          INTEGER NOT NULL DEFAULT 0,
    p2p_target_id     TEXT NOT NULL DEFAULT '',  -- ou_ (user) or bot open id for p2p chats
    p2p_target_type   TEXT NOT NULL DEFAULT '',
    avatar_url        TEXT NOT NULL DEFAULT '',
    avatar_path       TEXT NOT NULL DEFAULT '',  -- relative to the data dir
    cursor_ms         INTEGER NOT NULL DEFAULT 0, -- newest create_time pulled by a per-chat list
    backfill_done_at  INTEGER NOT NULL DEFAULT 0,
    members_synced_at INTEGER NOT NULL DEFAULT 0,
    first_seen_at     INTEGER NOT NULL,
    last_seen_at      INTEGER NOT NULL,
    left_at           INTEGER NOT NULL DEFAULT 0, -- set when a full chat refresh no longer lists the chat
    sync_error        TEXT NOT NULL DEFAULT '',   -- last permanent API rejection for this chat (e.g. restricted mode)
    raw_json          TEXT NOT NULL DEFAULT ''
);
CREATE INDEX chats_name ON chats(name);

CREATE TABLE messages (
    id               INTEGER PRIMARY KEY,        -- ingest order; tie-breaker for sorting
    message_id       TEXT NOT NULL UNIQUE,       -- om_xxx
    chat_id          TEXT NOT NULL,
    msg_type         TEXT NOT NULL DEFAULT '',   -- text | post | image | file | interactive | system | ...
    sender_id        TEXT NOT NULL DEFAULT '',   -- ou_ for users, cli_ app id for bots
    sender_type      TEXT NOT NULL DEFAULT '',   -- user | app
    sender_name      TEXT NOT NULL DEFAULT '',
    content_raw      TEXT NOT NULL DEFAULT '',   -- OpenAPI body.content JSON string, msg_type specific
    content          TEXT NOT NULL DEFAULT '',   -- human-readable rendering (lark-cli convert_lib); empty until rendered
    create_ms        INTEGER NOT NULL,
    update_ms        INTEGER NOT NULL DEFAULT 0,
    message_position INTEGER NOT NULL DEFAULT 0, -- per-chat monotonic position; -1 for thread replies
    updated          INTEGER NOT NULL DEFAULT 0,
    deleted          INTEGER NOT NULL DEFAULT 0, -- recalled
    deleted_seen_at  INTEGER NOT NULL DEFAULT 0,
    thread_id        TEXT NOT NULL DEFAULT '',   -- omt_xxx when the message has or belongs to a thread
    reply_to         TEXT NOT NULL DEFAULT '',   -- parent message id for direct replies
    mentions_json    TEXT NOT NULL DEFAULT '',
    reactions_json   TEXT NOT NULL DEFAULT '',
    raw_json         TEXT NOT NULL,              -- OpenAPI item as received
    rendered_at      INTEGER NOT NULL DEFAULT 0,
    first_seen_at    INTEGER NOT NULL,
    last_seen_at     INTEGER NOT NULL
);
CREATE INDEX messages_chat_create ON messages(chat_id, create_ms);
CREATE INDEX messages_create ON messages(create_ms);
CREATE INDEX messages_sender_create ON messages(sender_id, create_ms);
CREATE INDEX messages_thread ON messages(thread_id) WHERE thread_id <> '';
CREATE INDEX messages_unrendered ON messages(rendered_at) WHERE rendered_at = 0;

-- Read/consumed state lives apart from messages so the daemon's message upsert
-- can never clobber what the TUI or CLI wrote.
CREATE TABLE read_state (
    message_id        TEXT PRIMARY KEY,
    is_read_remote    INTEGER,                   -- NULL unknown, 0 unread, 1 read (Feishu side)
    remote_checked_at INTEGER NOT NULL DEFAULT 0,
    check_count       INTEGER NOT NULL DEFAULT 0,
    next_check_at     INTEGER NOT NULL DEFAULT 0,
    consumed_at       INTEGER NOT NULL DEFAULT 0 -- local: a larkim consumer has seen it
);

CREATE TABLE resources (
    message_id      TEXT NOT NULL,
    file_key        TEXT NOT NULL,
    type            TEXT NOT NULL DEFAULT '',    -- image | file
    local_path      TEXT NOT NULL DEFAULT '',    -- relative to the data dir
    size_bytes      INTEGER NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','done','failed','skipped')),
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (message_id, file_key)
);

CREATE TABLE contacts (
    open_id     TEXT PRIMARY KEY,
    name        TEXT NOT NULL DEFAULT '',
    email       TEXT NOT NULL DEFAULT '',
    is_bot      INTEGER NOT NULL DEFAULT 0,
    p2p_chat_id TEXT NOT NULL DEFAULT '',
    avatar_url  TEXT NOT NULL DEFAULT '',
    avatar_path TEXT NOT NULL DEFAULT '',
    updated_at  INTEGER NOT NULL,
    raw_json    TEXT NOT NULL DEFAULT ''
);

CREATE TABLE chat_members (
    chat_id     TEXT NOT NULL,
    member_id   TEXT NOT NULL,
    member_type TEXT NOT NULL DEFAULT '',
    seen_at     INTEGER NOT NULL,
    PRIMARY KEY (chat_id, member_id)
);

CREATE TABLE sync_runs (
    id          INTEGER PRIMARY KEY,
    kind        TEXT NOT NULL,                   -- tick | backfill | repair
    started_at  INTEGER NOT NULL,
    finished_at INTEGER NOT NULL,
    ok          INTEGER NOT NULL,
    fetched     INTEGER NOT NULL DEFAULT 0,
    upserted    INTEGER NOT NULL DEFAULT 0,
    error       TEXT NOT NULL DEFAULT ''
);
