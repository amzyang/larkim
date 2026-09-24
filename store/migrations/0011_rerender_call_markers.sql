-- Feishu closes a call with a system message whose template is a single
-- space, so it rendered to nothing until the renderer learned to read the
-- length off the video_chat message the call left behind. Clearing
-- rendered_at re-runs them in process; no API call is involved.
UPDATE messages SET rendered_at = 0 WHERE msg_type = 'system' AND rendered_at <> 0 AND content = '';
