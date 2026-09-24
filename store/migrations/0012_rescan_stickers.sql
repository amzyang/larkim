-- A sticker's picture lives in the Lark client's own storage, which
-- ExtractResources only learned to register after the back-scan cursor had
-- passed every stored message. Rewinding the cursor registers the stickers
-- already in the database; AddPendingResources skips keys that have a row, so
-- re-walking earlier messages is free.
UPDATE sync_state SET value = '0' WHERE key = 'resource_scan_id';
