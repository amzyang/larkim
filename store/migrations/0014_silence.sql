-- Silence rules (config key `silence`) name the messages a reader asked not
-- to be pulled by: they keep their place in the chat and in every query, but
-- are left out of the unread badge and of the key the chat list orders on.
--
-- Both columns are derived from the configured rules. store.ReapplySilence
-- rebuilds them whenever the rules change, so nothing here has to survive an
-- edit of the config.
ALTER TABLE messages ADD COLUMN silenced INTEGER NOT NULL DEFAULT 0;
ALTER TABLE chats ADD COLUMN last_unsilenced_ms INTEGER NOT NULL DEFAULT 0;

-- Without rules the two keys are the same, which is what keeps an existing
-- list in the order it already had.
UPDATE chats SET last_unsilenced_ms = last_message_ms;
