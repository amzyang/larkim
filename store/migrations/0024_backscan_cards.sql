-- The back-scan is the only way a card stored before ExtractResources could
-- read json_attachment ever gets registered: the ingest path does not re-run
-- for a message already in the database, and repair reaches recent history
-- alone. Its query selects cards now, so rewinding the cursor picks up the
-- ones it walked past. AddPendingResources skips keys that already have a row,
-- so re-walking earlier messages is free.
UPDATE sync_state SET value = '0' WHERE key = 'resource_scan_id';
