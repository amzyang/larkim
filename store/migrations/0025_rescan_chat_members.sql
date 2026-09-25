-- Rosters were fetched from an endpoint that answers with users alone, so no
-- chat records the bots in it. Drop the stamps and let the members pass
-- rebuild every list from the bucketed one.
UPDATE chats SET members_synced_at = 0;
