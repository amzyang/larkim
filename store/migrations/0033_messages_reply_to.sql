-- The reply tree is walked from a page of messages both ways — up to each
-- message's topmost stored ancestor, down over that root's descendants — and
-- every level of the walk is its own join on reply_to. Without this each
-- level is a full scan of messages.
CREATE INDEX messages_reply_to ON messages(reply_to) WHERE reply_to <> '';
