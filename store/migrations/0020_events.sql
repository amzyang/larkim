-- A durable record of the decisions that are otherwise only in a log file:
-- the file rotates, resources.last_error is overwritten by the next attempt,
-- and neither survives a rebuild of the derived tables. Written only by the
-- process holding daemon.lock, like every other sync table.
CREATE TABLE events (
    id      INTEGER PRIMARY KEY,
    at_ms   INTEGER NOT NULL,
    kind    TEXT NOT NULL,
    subject TEXT NOT NULL DEFAULT '',
    detail  TEXT NOT NULL DEFAULT ''
);

-- Attachments Feishu has deleted answer 400 (14005) forever, but lark-cli
-- reports that as a transport failure, so each one was retried five times.
-- They are permanent as of this migration; retire the rows still in backoff.
UPDATE resources
   SET next_attempt_at = 0
 WHERE status = 'failed' AND next_attempt_at > 0
   AND last_error LIKE '%Resource Has Been Deleted%';
