-- A card's rendering keeps a link's label and drops its target, so the links
-- inside cards were never registered while the back-scan read renderings
-- alone. Rewinding the cursor re-registers them; AddPendingDocLinks skips
-- tokens that already have a row, so re-walking earlier messages is free.
UPDATE sync_state SET value = '0' WHERE key = 'doc_scan_id';
