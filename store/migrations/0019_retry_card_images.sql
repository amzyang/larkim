-- Card images are never in lark-cli's batch download, so they reached
-- fetchAside, which until now refused every type but a video's cover. Each one
-- burned its five attempts against that refusal and stopped being retried.
-- Rewinding the scan cursor cannot revive them the way 0008 did: the keys
-- already have rows, and AddPendingResources skips those. Clearing the retry
-- state is what puts them back in the queue, now that fetchAside fetches them.
UPDATE resources
   SET status = 'pending', attempts = 0, next_attempt_at = 0, last_error = ''
 WHERE status = 'failed' AND last_error LIKE 'not returned by lark-cli%';
