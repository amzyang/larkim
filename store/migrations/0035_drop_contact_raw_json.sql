-- contacts.raw_json was never written: a contact row is assembled from several
-- API shapes (chat members, a user search, a bot lookup) and the largest
-- source of all — a message's sender block — has no contact item behind it, so
-- there is no single "item as received" to keep. The column held '' on every
-- row.
ALTER TABLE contacts DROP COLUMN raw_json;
