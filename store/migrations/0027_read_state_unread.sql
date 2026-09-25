-- The chat list counts a handful of unread messages, but read_state carried no
-- index beyond its primary key, so the planner could only drive that aggregate
-- from messages: the badge cost grew with every message ever synced rather
-- than with the few still unread. Ordinary rather than partial, because a
-- partial index on the same predicate is only picked once ANALYZE has run, and
-- nothing here runs it.
CREATE INDEX read_state_unread ON read_state(is_read_remote, local_read_at, message_id);
