-- Feishu exposes no mark-read call, so a badge fed only by the remote receipt
-- never falls while the reader stays inside larkim. local_read_at is the
-- second half of that judgement: a message the reader has had in front of
-- them. It belongs to the consumers, like consumed_at, and the daemon never
-- writes it.
ALTER TABLE read_state ADD COLUMN local_read_at INTEGER NOT NULL DEFAULT 0;
