-- A video's cover is the picture the client draws the message as, and
-- ExtractResources only learned to register it after the back-scan cursor had
-- passed every stored message. Rewinding the cursor registers the covers of
-- the videos already in the database; AddPendingResources skips keys that have
-- a row, so re-walking earlier messages is free.
UPDATE sync_state SET value = '0' WHERE key = 'resource_scan_id';
