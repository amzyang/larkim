-- Identity fields from `contact +search-user`, which resolves tenant-wide
-- while contact/v3/users/{id} is limited to the app's directory scope.
ALTER TABLE contacts ADD COLUMN enterprise_email TEXT NOT NULL DEFAULT '';
ALTER TABLE contacts ADD COLUMN department TEXT NOT NULL DEFAULT '';
ALTER TABLE contacts ADD COLUMN is_cross_tenant INTEGER NOT NULL DEFAULT 0;
ALTER TABLE contacts ADD COLUMN detail_checked_at INTEGER NOT NULL DEFAULT 0;
