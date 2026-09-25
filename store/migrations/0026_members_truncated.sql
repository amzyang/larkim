-- A tenant's security config caps how many members the API answers with for a
-- large group. The roster that comes back looks complete, so the cap is
-- recorded beside it and the info pane says the list is partial.
ALTER TABLE chats ADD COLUMN members_truncated INTEGER NOT NULL DEFAULT 0;
UPDATE chats SET members_synced_at = 0;
