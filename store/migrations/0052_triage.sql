-- triage is the verdict on each fresh arrival the process holding daemon.lock
-- judged: how urgent it is and why, and how far the work it set off has got.
-- One row per judged message, written once; the stamps after it move forward
-- only, so a restart resumes exactly where the last process stopped.
--
-- Outside the data_rev triggers: no TUI draws it, and the drafts it leads to
-- reach the TUI through draft_candidates, which is counted.

CREATE TABLE triage (
    message_id  TEXT PRIMARY KEY,           -- om_… judged
    chat_id     TEXT NOT NULL,
    level       TEXT NOT NULL,              -- P0 | P1 | drop
    reason      TEXT NOT NULL,              -- the rule or judge that decided
    jev_p       REAL,                       -- Jev's attention probability; NULL when a rule decided
    judged_ms   INTEGER NOT NULL,           -- Unix ms UTC
    drafted_ms  INTEGER NOT NULL DEFAULT 0, -- P0 only: 0 = drafting still owed
    draft_tries INTEGER NOT NULL DEFAULT 0,
    notified_ms INTEGER NOT NULL DEFAULT 0  -- P0 only: 0 = banner still owed; set when one was suppressed too
);

CREATE INDEX triage_to_notify ON triage(judged_ms) WHERE level = 'P0' AND notified_ms = 0;
CREATE INDEX triage_to_draft ON triage(judged_ms) WHERE level = 'P0' AND drafted_ms = 0;
