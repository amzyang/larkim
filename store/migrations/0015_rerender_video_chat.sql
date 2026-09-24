-- A video_chat message rendered to lark-cli's "[Video call]", which names
-- neither the meeting nor how long it ran, until the renderer learned to
-- read its body. Clearing rendered_at re-runs them in process; no API call
-- is involved.
UPDATE messages SET rendered_at = 0 WHERE msg_type = 'video_chat' AND rendered_at <> 0;
