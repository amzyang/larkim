DELETE FROM draft_candidates
WHERE mid IN (SELECT message_id FROM messages WHERE deleted = 1);
