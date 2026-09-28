-- Interactive cards are rendered in process now, from the card JSON rather
-- than from lark-cli's flattening of it, which ran a heading onto the list
-- below it and spelled every picture and button as scaffolding. Posts are
-- rendered in process too, to the same text lark-cli produced, so only the
-- cards need their stored rendering redone.
UPDATE messages SET rendered_at = 0 WHERE msg_type = 'interactive' AND deleted = 0;
