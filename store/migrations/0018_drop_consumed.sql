-- consumed_at was a processing cursor for scripts that never existed, and the
-- TUI wrote it on every page it drew, so it never held what it claimed to. A
-- consumer polling this database keeps its own position instead; see the
-- consumer cursors section of docs/SCHEMA.md.
--
-- The rev trigger names the column, and SQLite refuses to drop a column a
-- trigger reads, so it is rebuilt over the columns that remain.

DROP TRIGGER read_state_rev_au;
CREATE TRIGGER read_state_rev_au AFTER UPDATE ON read_state
WHEN (NEW.is_read_remote, NEW.local_read_at)
 IS NOT (OLD.is_read_remote, OLD.local_read_at)
BEGIN
    UPDATE data_rev SET rev = rev + 1;
END;

ALTER TABLE read_state DROP COLUMN consumed_at;
