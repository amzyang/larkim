-- The completion state of a todo message's task. A todo's body never says
-- whether its task finished, and the rendering that does display it is
-- rewritten by the render queue, so the state is kept here where both the
-- render path and the TUI read it as data.
CREATE TABLE todo_done (
    message_id TEXT PRIMARY KEY,  -- om_xxx of the todo message
    done       INTEGER NOT NULL   -- 1 the task is finished
);

-- The icon reads this table, so a write that changes no rendering text must
-- still tell consumers to re-read the row.
CREATE TRIGGER todo_done_rev_ai AFTER INSERT ON todo_done BEGIN
    UPDATE data_rev SET rev = rev + 1;
END;

CREATE TRIGGER todo_done_rev_au AFTER UPDATE ON todo_done BEGIN
    UPDATE data_rev SET rev = rev + 1;
END;
