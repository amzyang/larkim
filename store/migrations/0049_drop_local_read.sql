-- Feishu mark-read now lands through the web gateway. Unread is the
-- remote receipt alone; a local overlay made the badge disagree with
-- the official client.
DROP TRIGGER read_state_rev_au;
CREATE TRIGGER read_state_rev_au AFTER UPDATE ON read_state
WHEN NEW.is_read_remote IS NOT OLD.is_read_remote
BEGIN
    UPDATE data_rev SET rev = rev + 1;
END;

DROP INDEX read_state_unread;
CREATE INDEX read_state_unread ON read_state(is_read_remote, message_id);

ALTER TABLE read_state DROP COLUMN local_read_at;
