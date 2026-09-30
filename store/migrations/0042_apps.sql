-- A bot that reacts is named in the reaction by its app id (cli_…), not by
-- the open id it speaks under, and nothing on either side maps one to the
-- other. Apps are kept apart from contacts because an app id is nobody a
-- message can be sent to: in contacts it would surface in the forward
-- chooser, the people search and --to completion.
--
-- A row is enrolled when a reaction block naming the app is stored, and
-- checked_at > 0 means the lookup has run: name stays '' for an app this
-- tenant will not show, and it is not asked for again.
CREATE TABLE apps (
    app_id     TEXT PRIMARY KEY,
    name       TEXT NOT NULL DEFAULT '',
    checked_at INTEGER NOT NULL DEFAULT 0
);

-- Only a name coming in changes what a reader draws; enrolment does not.
CREATE TRIGGER apps_rev_au AFTER UPDATE ON apps
WHEN NEW.name IS NOT OLD.name
BEGIN
    UPDATE data_rev SET rev = rev + 1;
END;

INSERT INTO apps (app_id)
SELECT DISTINCT json_extract(d.value, '$.operator.operator_id')
FROM messages m,
     json_each(CASE WHEN json_valid(m.reactions_json) THEN m.reactions_json ELSE '{}' END, '$.details') d
WHERE json_extract(d.value, '$.operator.operator_type') = 'app'
  AND json_extract(d.value, '$.operator.operator_id') <> ''
ON CONFLICT(app_id) DO NOTHING;
