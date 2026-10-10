-- reminders are the banners an urgent message asked for at a later time it
-- named ("14:00 前交周报"), planned when the message was drafted for and
-- raised by the process holding daemon.lock once due. One per message: a
-- replan of the same message moves its time rather than adding a second.

CREATE TABLE reminders (
    message_id TEXT PRIMARY KEY,          -- the message that named the time
    chat_id    TEXT NOT NULL,
    fire_ms    INTEGER NOT NULL,          -- Unix ms UTC the banner is due
    title      TEXT NOT NULL,
    fired_ms   INTEGER NOT NULL DEFAULT 0 -- 0 = still owed; set when raised or dropped as too late
);

CREATE INDEX reminders_due ON reminders(fire_ms) WHERE fired_ms = 0;
