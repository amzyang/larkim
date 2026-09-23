-- The API's `updated` flag is not the client's "edited" badge: Feishu patches a
-- message seconds after it is sent (mention resolution, link and time-phrase
-- enrichment, image post-processing) and every patch sets it. Measured against
-- the live API, such a patch bumps update_time while leaving body.content
-- byte-identical, so the only trustworthy edit signal is a body we watched
-- change between two syncs.
--
-- Backfilled history therefore never carries an edit: we never saw its earlier
-- body.
ALTER TABLE messages ADD COLUMN edited_at INTEGER NOT NULL DEFAULT 0;
