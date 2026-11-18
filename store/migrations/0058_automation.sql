CREATE TABLE automation_rules (
    id          TEXT PRIMARY KEY,
    fingerprint TEXT NOT NULL,
    enabled     INTEGER NOT NULL,
    since_ms    INTEGER NOT NULL
);

CREATE TABLE automation_dirty (
    message_id TEXT PRIMARY KEY,
    version    INTEGER NOT NULL
);

CREATE TABLE automation_jobs (
    id          INTEGER PRIMARY KEY,
    rule_id     TEXT NOT NULL,
    message_id  TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    payload     TEXT NOT NULL,
    callback    TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'unknown', 'cancelled')),
    result      TEXT NOT NULL DEFAULT '',
    created_ms  INTEGER NOT NULL,
    started_ms  INTEGER NOT NULL DEFAULT 0,
    finished_ms INTEGER NOT NULL DEFAULT 0,
    UNIQUE (rule_id, message_id)
);

CREATE INDEX automation_jobs_pending ON automation_jobs(id) WHERE status = 'pending';

CREATE TRIGGER automation_messages_insert AFTER INSERT ON messages
WHEN EXISTS (SELECT 1 FROM automation_rules WHERE enabled = 1)
BEGIN
    INSERT INTO automation_dirty(message_id, version) VALUES (NEW.message_id, 1)
    ON CONFLICT(message_id) DO UPDATE SET version = automation_dirty.version + 1;
END;

CREATE TRIGGER automation_messages_update AFTER UPDATE ON messages
WHEN EXISTS (SELECT 1 FROM automation_rules WHERE enabled = 1)
 AND (NEW.chat_id, NEW.sender_id, NEW.sender_type, NEW.msg_type,
      NEW.content, NEW.content_raw, NEW.raw_json, NEW.rendered_at <> 0,
      NEW.create_ms, NEW.update_ms, NEW.deleted)
  IS NOT
     (OLD.chat_id, OLD.sender_id, OLD.sender_type, OLD.msg_type,
      OLD.content, OLD.content_raw, OLD.raw_json, OLD.rendered_at <> 0,
      OLD.create_ms, OLD.update_ms, OLD.deleted)
BEGIN
    INSERT INTO automation_dirty(message_id, version) VALUES (NEW.message_id, 1)
    ON CONFLICT(message_id) DO UPDATE SET version = automation_dirty.version + 1;
END;
