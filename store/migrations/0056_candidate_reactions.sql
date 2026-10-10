-- A draft can offer reactions beside its replies: the emoji_type keys the
-- drafter suggested, which the TUI puts on the source message. A row that only
-- holds reactions leaves draft as ''.

ALTER TABLE draft_candidates ADD COLUMN reactions TEXT NOT NULL DEFAULT '[]'; -- minimal JSON array of emoji_type keys
