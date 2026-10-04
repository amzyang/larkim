-- 2200 is Feishu's generic internal error: the docs say it usually means the
-- API was called too often, and to slow down or retry. IsPermanent used to
-- treat every non-rate-limit API exit as permanent, so a single 2200 pinned
-- the chat and discovery skipped it forever. Restricted-mode refusals stay
-- (231203 and the rest); only the 2200 pins are lifted.
UPDATE chats SET sync_error = '' WHERE sync_error LIKE '2200:%';
