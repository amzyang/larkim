-- A child of a bundle is a real message of its own chat, so Feishu answers
-- reactions/batch_query for it by id. The expansion endpoint does not carry
-- them, which is why they are asked for separately and kept here rather than
-- read off raw_json.
--
-- Only while the reader is still in the source chat: a forward drawn from a
-- conversation they never joined answers no_permission, and those children
-- keep an empty summary for good.
ALTER TABLE forwarded_messages ADD COLUMN reactions_json TEXT NOT NULL DEFAULT '';

-- Every bundle already expanded predates the reaction call and is owed one.
-- Re-expanding is idempotent — SaveForwarded replaces the whole tree — and a
-- bundle Feishu refused stays settled.
UPDATE forwarded_roots SET fetched_at = 0, next_attempt_at = 0 WHERE last_error = '';
