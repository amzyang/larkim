-- Card images live in the card's json_attachment table, which ExtractResources
-- only learned to read after the back-scan cursor had already passed every
-- stored message. Rewinding the cursor re-registers them; AddPendingResources
-- skips keys that already have a row, so re-walking earlier messages is free.
UPDATE sync_state SET value = '0' WHERE key = 'resource_scan_id';
