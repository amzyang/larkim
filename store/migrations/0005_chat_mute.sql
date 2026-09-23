-- Do-not-disturb, a per-user setting that no chat listing carries: it comes
-- from a batch lookup of its own (im/v1/chat_user_setting/batch_get_mute_status)
-- that rides the full chat refresh.
--
-- mute_checked_at stamps the last answer, including for chats the API declined
-- to answer for: those keep whatever was last known rather than being asked
-- again every tick.
ALTER TABLE chats ADD COLUMN muted INTEGER NOT NULL DEFAULT 0;
ALTER TABLE chats ADD COLUMN mute_checked_at INTEGER NOT NULL DEFAULT 0;
