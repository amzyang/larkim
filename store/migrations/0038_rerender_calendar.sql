-- A calendar message rendered to lark-cli's XML block, and its tags and
-- attributes went into content, the chat list and the FTS index — share_token
-- among them, which is the credential that joins the event. Clearing
-- rendered_at re-runs them in process; no API call is involved.
UPDATE messages SET rendered_at = 0
WHERE msg_type IN ('calendar', 'share_calendar_event', 'general_calendar') AND rendered_at <> 0;
