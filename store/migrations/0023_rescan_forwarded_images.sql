-- A forwarded bundle names its pictures only in its rendering, which the
-- back-scan neither read nor selected the bundle's msg_type for. Rewinding the
-- cursor registers them; AddPendingResources skips keys that already have a
-- row, so re-walking earlier messages is free.
UPDATE sync_state SET value = '0' WHERE key = 'resource_scan_id';
